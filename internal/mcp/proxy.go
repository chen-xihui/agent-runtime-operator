package mcp

import (
	"context"
	"log"

	"github.com/chen-xihui/agent-runtime-operator/internal/audit"
	"github.com/chen-xihui/agent-runtime-operator/internal/metrics"
)

// Invoker 底层工具调用器（实际转发到 MCP Server）
type Invoker func(ctx context.Context, tool *Tool, args map[string]any) (map[string]any, error)

// DefaultInvoker 默认调用器：仅作为未注入 MCPInvoker 时的回显 fallback（便于测试/调试）。
// 生产环境请用 MCPInvoker（internal/mcp/invoker.go）经 WithInvoker(invoker.Invoke) 注入，
// 由 MCP Client 按 Tool.Endpoint + Transport（stdio / streamable HTTP）转发到真实 Tool Server。
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

// WithAudit 设置审计回调（轻量可观测钩子，供调用方集成；与 WithAuditStore 独立并存）。
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

// doAudit 统一审计出口：一次调用同时驱动三类审计副作用，职责互不替代：
//  1. 审计回调（WithAudit）：供调用方集成（自定义日志/工单/外发），可选；
//  2. 审计落库（WithAuditStore）：DLP 全量出网审计持久化（P1-1），可选；
//  3. 可观测性指标（M5）：工具调用与错误计数。
//
// 三者相互独立：即使配置了回调，落库与指标仍会执行，避免审计链路缺失。
func (p *MemoryProxy) doAudit(agentID, toolName string, args, result map[string]any, err error) {
	// 1) 调用方回调（可选）
	if p.audit != nil {
		p.audit(p.tenantID, agentID, toolName, args, result, err)
	} else {
		// 无回调时退化为标准日志（兜底可观测性）
		log.Printf("mcp audit: tenant=%s agent=%s tool=%s err=%v", p.tenantID, agentID, toolName, err)
	}

	// 2) DLP 审计落库（P1-1，成功与失败均记录）
	if p.store != nil {
		rec := &audit.Record{
			TenantID: p.tenantID,
			AgentID:  agentID,
			Action:   audit.ActionToolCall,
			Resource: toolName,
			Success:  err == nil,
			Error:    errString(err),
		}
		if werr := p.store.Write(context.Background(), rec); werr != nil {
			log.Printf("mcp audit store write failed: tenant=%s tool=%s err=%v", p.tenantID, toolName, werr)
		}
	}

	// 3) 可观测性指标（M5）
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
