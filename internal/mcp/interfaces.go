// Package mcp 提供 MCP（Model Context Protocol）工具接入接口。
// 见 design-doc 4.3.1 / core-interface 3.1。
package mcp

import "context"

// Tool 一个 MCP 工具的描述
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Endpoint    string         `json:"endpoint"`  // MCP Server 地址
	Transport   string         `json:"transport"` // stdio | streamable-http
	Auth        string         `json:"auth"`      // 鉴权方式
	RateLimit   RateLimit      `json:"rateLimit,omitempty"`
	Redact      []string       `json:"redact,omitempty"` // 敏感字段脱敏
	Audit       bool           `json:"audit"`
}

// transportName 返回规范化的传输名
func (t *Tool) transportName() string {
	if t.Transport == "" {
		return "stdio"
	}
	return t.Transport
}

type RateLimit struct {
	RPS     int `json:"rps,omitempty"`
	Burst   int `json:"burst,omitempty"`
	Monthly int `json:"monthly,omitempty"`
}

// Registry MCP 工具注册与鉴权中心
type Registry interface {
	Register(ctx context.Context, tool *Tool) error
	Unregister(ctx context.Context, name string) error
	// Authorize 校验租户+Agent 是否有权调用该工具
	// 修订(P1-4): 增加 params 做数据级 ABAC，返回 DataScope 用于注入数据过滤条件
	Authorize(ctx context.Context, tenantID, agentID, toolName string, params map[string]any) (*Tool, *DataScope, error)
	List(ctx context.Context, tenantID string) ([]*Tool, error)
}

// DataScope 数据范围过滤条件，防止跨租户数据可达（ABAC）
type DataScope struct {
	Filter map[string]any `json:"filter"` // 注入到工具请求的过滤条件，如 {tenant: "tenant-a"}
	Redact []string       `json:"redact,omitempty"`
}

// Proxy Sidecar 内的 MCP 代理
type Proxy interface {
	// Invoke 代理调用，执行鉴权/数据级过滤/脱敏/审计
	Invoke(ctx context.Context, agentID, toolName string, args map[string]any) (map[string]any, error)
}
