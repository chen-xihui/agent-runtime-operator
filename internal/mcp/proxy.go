package mcp

import (
	"context"
	"log"

	"github.com/example/agent-runtime-operator/internal/audit"
	"github.com/example/agent-runtime-operator/internal/metrics"
)

// Invoker 底层工具调用器（实际转发到 MCP Server）
type Invoker func(ctx context.Context, tool *Tool, args map[string]any) (map[string]any, error)

// DefaultInvoker 默认调用器：此处占位，MCP Server 实际转发在 M3 集成。
// 生产环境由 MCP Client 按 tool.Endpoint 转发。
func DefaultInvoker(ctx context.Context, tool *Tool, args map[string]any) (map[string]any, error) {
	return map[string]any{
		"tool":     tool.Name,
		"invoked":  true,
		"echoArgs": args,
	}, nil
}

// MemoryProxy 基于内存 Registry 的 MCP 代理（Sandbox Sidecar 内）
// 执行鉴权 → 数据级过滤注入 → 调用 → 脱敏 → 审计
// 注：Proxy 运行在特定租户的 Sandbox 内，故 tenantID 在构造时注入（core-interface 3.1 签名无 tenantID）。
type MemoryProxy struct {
	tenantID string
	registry *MemoryRegistry
	invoker  Invoker
	// audit 审计回调（DLP 全量出网审计，P1-1）
	audit func(tenantID, agentID, toolName string, args, result map[string]any, err error)
	// store 审计存储（落库，P1-1 DLP）
	store audit.Store
}

// NewMemoryProxy 创建内存 MCP 代理
func NewMemoryProxy(tenantID string, registry *MemoryRegistry) *MemoryProxy {
	return &MemoryProxy{
		tenantID: tenantID,
		registry: registry,
		invoker:  DefaultInvoker,
		store:    audit.NoopStore{},
	}
}

// WithInvoker 设置底层工具调用器
func (p *MemoryProxy) WithInvoker(inv Invoker) *MemoryProxy {
	p.invoker = inv
	return p
}

// WithAudit 设置审计回调
func (p *MemoryProxy) WithAudit(a func(tenantID, agentID, toolName string, args, result map[string]any, err error)) *MemoryProxy {
	p.audit = a
	return p
}

// WithAuditStore 设置审计存储（落库 DLP 审计）
func (p *MemoryProxy) WithAuditStore(s audit.Store) *MemoryProxy {
	if s != nil {
		p.store = s
	}
	return p
}

// Invoke 代理调用：鉴权 → 数据级过滤 → 调用 → 脱敏 → 审计
func (p *MemoryProxy) Invoke(ctx context.Context, agentID, toolName string, args map[string]any) (map[string]any, error) {
	// 1. 鉴权 + 获取数据范围（ABAC，P1-4）
	tool, scope, err := p.registry.Authorize(ctx, p.tenantID, agentID, toolName, args)
	if err != nil {
		p.doAudit(agentID, toolName, args, nil, err)
		return nil, err
	}

	// 2. 注入数据范围过滤条件（跨租户数据不可达）
	filteredArgs := InjectScope(args, scope)

	// 3. 调用工具
	result, err := p.invoker(ctx, tool, filteredArgs)
	if err != nil {
		p.doAudit(agentID, toolName, filteredArgs, nil, err)
		return nil, err
	}

	// 4. 返回字段脱敏（DLP）
	if scope != nil {
		result = RedactValues(result, scope.Redact)
	}

	// 5. 审计
	p.doAudit(agentID, toolName, filteredArgs, result, nil)
	return result, nil
}

func (p *MemoryProxy) doAudit(agentID, toolName string, args, result map[string]any, err error) {
	if p.audit != nil {
		p.audit(p.tenantID, agentID, toolName, args, result, err)
	} else {
		log.Printf("mcp audit: tenant=%s agent=%s tool=%s err=%v", p.tenantID, agentID, toolName, err)
	}
	// DLP 全量出网审计落库（P1-1）
	if p.store != nil {
		rec := &audit.Record{
			TenantID: p.tenantID,
			AgentID:  agentID,
			Action:   audit.ActionToolCall,
			Resource: toolName,
			Success:  err == nil,
			Error:    errString(err),
		}
		_ = p.store.Write(context.Background(), rec)
	}

	// 可观测性指标（M5）
	resLabel := "success"
	if err != nil {
		resLabel = "error"
		metrics.ObserveMCPError(p.tenantID, toolName)
	}
	metrics.ObserveToolCall(p.tenantID, toolName, resLabel)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

var _ Proxy = (*MemoryProxy)(nil)
