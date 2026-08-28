// Package relay 实现 Sandbox Sidecar 内的 Event Relay + MCP Proxy 统一代理（P0-2）。
//
// 设计原则（design-doc 4.1.4）：沙箱内 Agent 不直接持有任何外部凭证，
// 所有出网经由本 Pod 的 Sidecar 代理。Agent 仅通过本地 unix:// socket 通信。
package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/example/agent-runtime-operator/internal/eventbus"
)

// Config Relay Sidecar 配置
type Config struct {
	LocalSocket    string // e.g. /var/run/agent.sock
	TenantID       string
	AgentID        string
	NATSCredsPath  string // 仅 Relay 持有
	MCPRegistryAddr string
	A2AGatewayAddr  string
	AuditSink       string
}

// Relay Sandbox Sidecar 内的 Event Relay + MCP Proxy 统一代理
type Relay struct {
	cfg       *Config
	mu        sync.Mutex
	conns     map[net.Conn]struct{} // 已连接的 Agent 客户端
	listener  net.Listener
	onEvent   func(ctx context.Context, evt *eventbus.CloudEvent) error
	deliverCh chan *eventbus.CloudEvent
}

// New 创建 Relay 实例
func New(cfg *Config) *Relay {
	return &Relay{
		cfg:       cfg,
		conns:     make(map[net.Conn]struct{}),
		deliverCh: make(chan *eventbus.CloudEvent, 256),
	}
}

// Start 启动本地 socket 服务，等待 Agent 连接。
// 返回的 readyCh 在 socket 监听就绪后关闭（用于就绪探测）。
func (r *Relay) Start(ctx context.Context) (<-chan struct{}, error) {
	readyCh := make(chan struct{})

	dir := filepath.Dir(r.cfg.LocalSocket)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	// 清理可能残留的旧 socket 文件
	_ = os.Remove(r.cfg.LocalSocket)

	ln, err := net.Listen("unix", r.cfg.LocalSocket)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", r.cfg.LocalSocket, err)
	}
	r.listener = ln

	go r.acceptLoop(ctx)
	go r.deliverLoop(ctx)

	close(readyCh)
	return readyCh, nil
}

// OnEvent 注册 Agent 上报事件的处理回调（通常用于转发到事件总线）
func (r *Relay) OnEvent(h func(ctx context.Context, evt *eventbus.CloudEvent) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onEvent = h
}

// DeliverToAgent 将控制面事件投递给沙箱内 Agent（经本地 socket）
func (r *Relay) DeliverToAgent(ctx context.Context, evt *eventbus.CloudEvent) error {
	select {
	case r.deliverCh <- evt:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close 关闭监听与所有连接
func (r *Relay) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for conn := range r.conns {
		_ = conn.Close()
	}
	if r.listener != nil {
		return r.listener.Close()
	}
	return nil
}

func (r *Relay) acceptLoop(ctx context.Context) {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			// 临时错误，继续等待
			continue
		}
		r.mu.Lock()
		r.conns[conn] = struct{}{}
		r.mu.Unlock()
		go r.handleConn(ctx, conn)
	}
}

func (r *Relay) deliverLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-r.deliverCh:
			r.mu.Lock()
			for conn := range r.conns {
				r.writeEvent(conn, evt)
			}
			r.mu.Unlock()
		}
	}
}

// handleConn 处理单个 Agent 连接：读取其上报的事件并回调
func (r *Relay) handleConn(ctx context.Context, conn net.Conn) {
	defer func() {
		r.mu.Lock()
		delete(r.conns, conn)
		r.mu.Unlock()
		_ = conn.Close()
	}()

	dec := json.NewDecoder(conn)
	for {
		evt := &eventbus.CloudEvent{}
		if err := dec.Decode(evt); err != nil {
			return
		}
		r.mu.Lock()
		h := r.onEvent
		r.mu.Unlock()
		if h != nil {
			if err := h(ctx, evt); err != nil {
				// 处理失败，继续读取后续事件
				continue
			}
		}
	}
}

func (r *Relay) writeEvent(conn net.Conn, evt *eventbus.CloudEvent) {
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	// 追加换行作为帧分隔符
	data = append(data, '\n')
	_, _ = conn.Write(data)
}
