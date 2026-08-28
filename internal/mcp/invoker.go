package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// MCPInvoker 基于 MCP 协议的工具调用器（转发到真实 Tool Server）
// 按 Tool.Endpoint + Transport 选择 stdio 或 streamable HTTP 传输，并缓存客户端。
type MCPInvoker struct {
	mu        sync.Mutex
	clients   map[string]*MCPClient // key: endpoint+transport
	transports map[string]Transport
	// command 供 stdio 传输使用（本地 MCP Server 启动命令，可选）
	stdioCommand string
}

// NewMCPInvoker 创建 MCP 工具调用器
func NewMCPInvoker() *MCPInvoker {
	return &MCPInvoker{
		clients:   make(map[string]*MCPClient),
		transports: make(map[string]Transport),
	}
}

// WithStdioCommand 设置 stdio 传输的默认启动命令（本地工具）
func (i *MCPInvoker) WithStdioCommand(cmd string) *MCPInvoker {
	i.stdioCommand = cmd
	return i
}

// Invoke 实现 Invoker：通过 MCP 协议调用工具
func (i *MCPInvoker) Invoke(ctx context.Context, tool *Tool, args map[string]any) (map[string]any, error) {
	client, err := i.clientFor(ctx, tool)
	if err != nil {
		return nil, err
	}
	return client.CallTool(ctx, tool.Name, args)
}

// clientFor 获取（或创建）对应端点的 MCP 客户端
func (i *MCPInvoker) clientFor(ctx context.Context, tool *Tool) (*MCPClient, error) {
	transport := tool.transportName()
	key := transport + "|" + tool.Endpoint

	i.mu.Lock()
	defer i.mu.Unlock()
	if c, ok := i.clients[key]; ok {
		return c, nil
	}

	t, err := i.buildTransport(ctx, tool)
	if err != nil {
		return nil, err
	}
	client := NewMCPClient(t)
	// 握手
	if err := client.Initialize(ctx); err != nil {
		t.Close()
		return nil, fmt.Errorf("mcp initialize %s: %w", tool.Endpoint, err)
	}
	i.transports[key] = t
	i.clients[key] = client
	return client, nil
}

func (i *MCPInvoker) buildTransport(ctx context.Context, tool *Tool) (Transport, error) {
	switch strings.ToLower(tool.transportName()) {
	case "stdio":
		cmd := i.stdioCommand
		if cmd == "" {
			// 默认：把 Endpoint 当作可执行命令
			cmd = tool.Endpoint
		}
		return NewStdioTransport(ctx, cmd)
	case "streamable-http", "http", "streamable_http":
		return NewHTTPTransport(tool.Endpoint, "", 30*time.Second), nil
	default:
		return nil, fmt.Errorf("mcp: unsupported transport %q", tool.transportName())
	}
}
