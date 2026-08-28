// Package registration 提供 Agent ↔ MCP Registry / A2A Gateway 的控制器联动。
//
// 职责（M2）：
// - 从 ToolBinding/MCPEndpoint CRD 读取工具授权，注入 MCP Registry（R-4 权限唯一来源）
// - Agent 启动（Running）时注册 AgentCard 到 A2A Gateway
// - Agent 删除时注销
package registration

import (
	"context"
	"slices"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/a2a"
	"github.com/example/agent-runtime-operator/internal/mcp"
)

// MCPRegistry 抽象：注册中心（最小接口，便于测试替身）
type MCPRegistry interface {
	// Register 注册工具
	Register(ctx context.Context, tool *mcp.Tool) error
	// Unregister 注销工具
	Unregister(ctx context.Context, name string) error
	// BindToolGrant 为租户下某 Agent（或租户全部）绑定工具授权
	BindToolGrant(tenantID, agentID string, grants map[string]mcp.ToolGrant)
}

// A2AGateway 抽象：A2A 注册中心（最小接口）
type A2AGateway interface {
	Register(ctx context.Context, agentID, tenantID string, card *a2a.AgentCard) error
}

// Syncer 封装 Agent↔Registry/Gateway 联动
type Syncer struct {
	mcp     MCPRegistry
	gateway A2AGateway
}

// NewSyncer 创建联动同步器
func NewSyncer(m MCPRegistry, g A2AGateway) *Syncer {
	return &Syncer{mcp: m, gateway: g}
}

// SyncAgentTools 从 ToolBinding 列表加载 Agent 的工具授权并注入 MCP Registry。
// 规则（R-4）：
//   - ToolBinding.AgentRefs 包含该 Agent 或为空（租户内全部）时生效
//   - 权限唯一来源是 ToolBinding，Agent.spec.mcp.allowedTools 仅作引用
func (s *Syncer) SyncAgentTools(ctx context.Context, tenantID, agentID string, bindings []agentv1.ToolBinding, endpoints map[string]agentv1.MCPEndpoint) error {
	grants := make(map[string]mcp.ToolGrant)
	for _, tb := range bindings {
		// 该 ToolBinding 是否适用于本 Agent
		if !bindingApplies(tb, agentID) {
			continue
		}
		for _, grant := range tb.Spec.Tools {
			grants[grant.Name] = mcp.ToolGrant{
				DataScope: grant.DataScope,
				RateLimit: mcp.RateLimit{
					RPS:     grant.RateLimit.RPS,
					Burst:   grant.RateLimit.Burst,
					Monthly: grant.RateLimit.Monthly,
				},
				Redact: grant.Redact,
			}
			// 若 MCPEndpoint 存在，注册/更新工具描述
			if ep, ok := endpoints[grant.Name]; ok {
				_ = s.mcp.Register(ctx, &mcp.Tool{
					Name:        grant.Name,
					Endpoint:    ep.Spec.Address,
					Auth:        ep.Spec.Auth.Type,
					RateLimit:   mcp.RateLimit{RPS: grant.RateLimit.RPS, Burst: grant.RateLimit.Burst, Monthly: grant.RateLimit.Monthly},
					Redact:      grant.Redact,
				})
			}
		}
	}
	// 注入授权（Agent 级）
	s.mcp.BindToolGrant(tenantID, agentID, grants)
	return nil
}

// RegisterAgentCard 注册 Agent 能力卡到 A2A Gateway（Agent Running 时）
func (s *Syncer) RegisterAgentCard(ctx context.Context, tenantID, agentID string, agent *agentv1.Agent) error {
	if !agent.Spec.A2A.Enabled {
		return nil // 未启用 A2A 不注册（C2）
	}
	card := BuildAgentCard(agent)
	return s.gateway.Register(ctx, agentID, tenantID, card)
}

// BuildAgentCard 从 Agent spec 构建 A2A AgentCard
func BuildAgentCard(agent *agentv1.Agent) *a2a.AgentCard {
	card := &a2a.AgentCard{
		Name:        agent.Name,
		Description: "Agent managed by agent-runtime-operator",
		Version:     "1.0",
	}
	// 能力卡展示性汇总
	if card.Auth == nil {
		card.Auth = map[string]any{}
	}
	for _, t := range agent.Spec.A2A.Tasks {
		card.Skills = append(card.Skills, a2a.AgentSkill{ID: t, Name: t})
	}
	// 若 AgentCard 直接提供了自定义卡（agentCard 字段），合并
	if raw, ok := agent.Spec.A2A.AgentCard["description"].(string); ok && raw != "" {
		card.Description = raw
	}
	return card
}

// bindingApplies 判断 ToolBinding 是否适用于指定 Agent
// AgentRefs 为空表示租户内全部；否则需包含该 Agent 名
func bindingApplies(tb agentv1.ToolBinding, agentID string) bool {
	if len(tb.Spec.AgentRefs) == 0 {
		return true
	}
	return slices.Contains(tb.Spec.AgentRefs, agentID)
}
