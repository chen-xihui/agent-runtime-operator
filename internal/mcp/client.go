package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// ===================== MCP JSON-RPC 消息（MCP 协议，JSON-RPC 2.0） =====================

// JSONRPCRequest MCP JSON-RPC 请求
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse MCP JSON-RPC 响应
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError MCP JSON-RPC 错误
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// 协议常量
const (
	mcpVersion         = "2024-11-05"
	jsonrpcVersion     = "2.0"
	MethodInitialize   = "initialize"
	MethodToolsList    = "tools/list"
	MethodToolsCall    = "tools/call"
	MethodNotifications = "notifications/initialized"
)

// ===================== MCP 工具相关消息 =====================

// ToolInfo MCP 服务器返回的工具描述
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// toolsCallParams tools/call 参数
type toolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// toolsCallResult tools/call 结果（MCP 返回结构化内容）
type toolsCallResult struct {
	Content []struct {
		Type string `json:"type"` // text / image / resource
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

// initParams initialize 参数
type initParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	ClientInfo      map[string]any `json:"clientInfo"`
	Capabilities    map[string]any `json:"capabilities"`
}

// initResult initialize 结果
type initResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      map[string]any `json:"serverInfo"`
	Capabilities    map[string]any `json:"capabilities"`
}

// MCPClient MCP 客户端（stdio / streamable HTTP 传输）
// 负责与 MCP Server 建立会话并调用工具（tools/call）
type MCPClient struct {
	transport Transport
}

// Transport MCP 传输层抽象
type Transport interface {
	// Call 发起一次 JSON-RPC 请求，返回响应
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	// Close 关闭连接
	Close() error
}

// NewMCPClient 创建 MCP 客户端
func NewMCPClient(t Transport) *MCPClient {
	return &MCPClient{transport: t}
}

// Initialize 执行 MCP 握手
func (c *MCPClient) Initialize(ctx context.Context) error {
	raw, err := c.transport.Call(ctx, MethodInitialize, initParams{
		ProtocolVersion: mcpVersion,
		ClientInfo:      map[string]any{"name": "agent-runtime-mcp", "version": "1.0"},
		Capabilities:    map[string]any{},
	})
	if err != nil {
		return fmt.Errorf("mcp initialize: %w", err)
	}
	var res initResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("mcp initialize parse: %w", err)
	}
	return nil
}

// CallTool 调用指定工具，返回解析后的结果
func (c *MCPClient) CallTool(ctx context.Context, toolName string, args map[string]any) (map[string]any, error) {
	raw, err := c.transport.Call(ctx, MethodToolsCall, toolsCallParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("mcp tools/call %q: %w", toolName, err)
	}

	var res toolsCallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp tools/call parse: %w", err)
	}
	if res.IsError {
		return nil, fmt.Errorf("mcp tool %q returned error", toolName)
	}

	// 聚合文本内容
	out := make(map[string]any)
	if len(res.Content) == 1 && res.Content[0].Type == "text" {
		// 尝试将文本解析为结构化结果，失败则原样返回
		var structured map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].Text), &structured); err == nil {
			return structured, nil
		}
		out["result"] = res.Content[0].Text
		return out, nil
	}
	out["content"] = res.Content
	return out, nil
}
