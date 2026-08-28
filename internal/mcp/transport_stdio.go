package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

// StdioTransport 基于 stdio 的 MCP 传输（启动 MCP Server 子进程，走 stdin/stdout）
// 适用于本地 MCP Server（工具以 stdio 方式运行）。
type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex
	id     atomic.Int64
	// 等待中的请求：id -> channel
	pending map[int64]chan json.RawMessage
}

// NewStdioTransport 创建 stdio 传输，command 为 MCP Server 启动命令
func NewStdioTransport(ctx context.Context, command string, args ...string) (*StdioTransport, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// 启动服务器
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mcp server: %w", err)
	}

	t := &StdioTransport{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReader(stdout),
		pending: make(map[int64]chan json.RawMessage),
	}
	go t.readLoop()
	return t, nil
}

// Call 发送 JSON-RPC 请求并等待响应
func (t *StdioTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := t.id.Add(1)
	paramsRaw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	req := JSONRPCRequest{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Method:  method,
		Params:  paramsRaw,
	}
	reqData, _ := json.Marshal(req)

	respCh := make(chan json.RawMessage, 1)
	t.mu.Lock()
	t.pending[id] = respCh
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
	}()

	t.mu.Lock()
	_, err = t.stdin.Write(append(reqData, '\n'))
	t.mu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case raw := <-respCh:
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// readLoop 读取服务器响应并分发给等待中的请求
func (t *StdioTransport) readLoop() {
	dec := json.NewDecoder(t.stdout)
	for {
		var resp JSONRPCResponse
		if err := dec.Decode(&resp); err != nil {
			return
		}
		t.mu.Lock()
		ch, ok := t.pending[resp.ID]
		t.mu.Unlock()
		if !ok {
			continue
		}
		if resp.Error != nil {
			// 错误通过特殊的 JSON 消息传递
			errMsg, _ := json.Marshal(map[string]any{"__error__": resp.Error.Message})
			ch <- errMsg
			continue
		}
		ch <- resp.Result
	}
}

// Close 关闭子进程
func (t *StdioTransport) Close() error {
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.stdin.Close()
		return t.cmd.Process.Kill()
	}
	return nil
}

var _ Transport = (*StdioTransport)(nil)
