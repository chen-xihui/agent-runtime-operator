package a2a

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// 常见错误
var (
	ErrAgentNotFound = errors.New("a2a: agent not found")
	ErrCrossTenant   = errors.New("a2a: cross-tenant delegation forbidden (D-4)")
	ErrTaskFailed    = errors.New("a2a: task execution failed")
)

// registration A2A 注册条目（含租户归属，用于 D-4 跨租户校验）
type registration struct {
	TenantID string
	Card     *AgentCard
}

// MemoryGateway 基于内存的 A2A 注册中心与消息路由
// 通道分工（D-2）：Agent 之间协作走 A2A；跨租户默认禁止（D-4）。
// 跨集群（M5 联邦）：本地无目标 Agent 时，经联邦路由器跨集群委派（D-4 双向信任）。
type MemoryGateway struct {
	mu    sync.RWMutex
	regs  map[string]registration // agentID -> 注册条目
	// 租户联邦信任：tenantA -> tenantB（双向授权后才允许跨租户委派，D-4）
	federation map[string]map[string]struct{}
	// 任务执行回调（由上层提供，实际委派到目标 Agent 沙箱）
	taskHandler func(ctx context.Context, to string, task *TaskMessage) (*TaskResult, error)
	// 消息路由回调
	routeHandler func(ctx context.Context, msg *Message) error
	// 跨集群联邦路由器（可选，M5）
	federator Federator
	// 本集群名（联邦路由起点）
	clusterName string
}

// NewMemoryGateway 创建 A2A Gateway
func NewMemoryGateway() *MemoryGateway {
	return &MemoryGateway{
		regs:       make(map[string]registration),
		federation: make(map[string]map[string]struct{}),
	}
}

// WithFederator 启用跨集群联邦委派（M5）
// clusterName 为本集群名，federator 为跨集群路由器。
func (g *MemoryGateway) WithFederator(clusterName string, f Federator) *MemoryGateway {
	g.clusterName = clusterName
	g.federator = f
	return g
}

// WithTaskHandler 设置任务委派执行回调（M3 编排引擎集成时接入）
func (g *MemoryGateway) WithTaskHandler(h func(ctx context.Context, to string, task *TaskMessage) (*TaskResult, error)) *MemoryGateway {
	g.taskHandler = h
	return g
}

// WithRouteHandler 设置消息路由回调
func (g *MemoryGateway) WithRouteHandler(h func(ctx context.Context, msg *Message) error) *MemoryGateway {
	g.routeHandler = h
	return g
}

// Register 注册 Agent 能力卡
func (g *MemoryGateway) Register(ctx context.Context, agentID, tenantID string, card *AgentCard) error {
	if card == nil {
		return fmt.Errorf("a2a: card required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.regs[agentID] = registration{TenantID: tenantID, Card: card}
	return nil
}

// Discover 按技能/关键词发现可用 Agent（同租户）
func (g *MemoryGateway) Discover(ctx context.Context, tenantID, query string, skills []string) ([]*AgentCard, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []*AgentCard
	for id, reg := range g.regs {
		if reg.TenantID != tenantID {
			continue // 只发现本租户 Agent（D-4）
		}
		if matchCard(reg.Card, query, skills) {
			cp := *reg.Card
			cp.Name = id
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SendTask 委派任务给目标 Agent（默认禁止跨租户，D-4）
// 本地无目标 Agent 且配置联邦时，尝试跨集群委派（M5）。
func (g *MemoryGateway) SendTask(ctx context.Context, from, to string, task *TaskMessage) (*TaskResult, error) {
	// 本地 Agent 存在 → 本地委派
	if g.isLocalAgent(to) {
		if err := g.checkSameOrFederatedTenant(ctx, from, to); err != nil {
			return nil, err
		}
		if g.taskHandler == nil {
			return &TaskResult{TaskID: task.ID, State: "in-progress"}, nil
		}
		return g.taskHandler(ctx, to, task)
	}

	// 本地无目标 Agent：尝试跨集群联邦委派（M5）
	return g.sendCrossCluster(ctx, to, task)
}

// isLocalAgent 判断目标 Agent 是否在本地注册
func (g *MemoryGateway) isLocalAgent(agentID string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.regs[agentID]
	return ok
}

// sendCrossCluster 跨集群联邦委派：从目标集群列表中选择可信任集群转发
func (g *MemoryGateway) sendCrossCluster(ctx context.Context, to string, task *TaskMessage) (*TaskResult, error) {
	if g.federator == nil || g.clusterName == "" {
		return nil, ErrAgentNotFound
	}
	// 联邦发现：找出信任本集群且能承载目标技能的目标集群
	skill := ""
	if task != nil {
		skill = task.Type
	}
	targets, err := g.federator.Lookup(g.clusterName, skill)
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		// 校验双向信任（D-4）
		if !g.federator.Allowed(g.clusterName, target) {
			continue
		}
		// 跨集群路由委派
		payload := map[string]interface{}{
			"task": task,
		}
		if _, err := g.federator.Route(ctx, g.clusterName, target, to, payload); err != nil {
			continue // 尝试下一个集群
		}
		return &TaskResult{
			TaskID:  task.ID,
			State:   "in-progress",
			Message: "delegated to cluster " + target,
		}, nil
	}
	return nil, ErrAgentNotFound
}

// Route 消息路由（含跨集群代理；跨租户需联邦信任）
func (g *MemoryGateway) Route(ctx context.Context, msg *Message) error {
	if err := g.checkSameOrFederatedTenant(ctx, msg.From, msg.To); err != nil {
		return err
	}
	if g.routeHandler == nil {
		return nil
	}
	return g.routeHandler(ctx, msg)
}

// checkSameOrFederatedTenant 校验来源与目标 Agent 是否同租户或存在联邦信任（D-4）
func (g *MemoryGateway) checkSameOrFederatedTenant(_ context.Context, from, to string) error {
	g.mu.RLock()
	fromReg, fromOK := g.regs[from]
	toReg, toOK := g.regs[to]
	g.mu.RUnlock()

	// 未注册的 Agent：保守拒绝
	if !fromOK || !toOK {
		return ErrAgentNotFound
	}
	if fromReg.TenantID == toReg.TenantID {
		return nil
	}
	// 跨租户：必须有联邦双向信任
	if g.hasFederation(fromReg.TenantID, toReg.TenantID) && g.hasFederation(toReg.TenantID, fromReg.TenantID) {
		return nil
	}
	return ErrCrossTenant
}

// AddFederation 建立两个租户间的双向联邦信任（D-4）
func (g *MemoryGateway) AddFederation(t1, t2 string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.federation[t1] == nil {
		g.federation[t1] = make(map[string]struct{})
	}
	if g.federation[t2] == nil {
		g.federation[t2] = make(map[string]struct{})
	}
	g.federation[t1][t2] = struct{}{}
	g.federation[t2][t1] = struct{}{}
}

func (g *MemoryGateway) hasFederation(a, b string) bool {
	m, ok := g.federation[a]
	if !ok {
		return false
	}
	_, ok = m[b]
	return ok
}

// matchCard 判断 AgentCard 是否匹配查询词或技能
func matchCard(card *AgentCard, query string, skills []string) bool {
	// 技能匹配（任一命中即可）
	for _, want := range skills {
		for _, have := range card.Skills {
			if strings.EqualFold(have.ID, want) || strings.EqualFold(have.Name, want) {
				return true
			}
		}
	}
	// 关键词匹配描述
	if query != "" {
		lower := strings.ToLower(query)
		if strings.Contains(strings.ToLower(card.Description), lower) ||
			strings.Contains(strings.ToLower(card.Name), lower) {
			return true
		}
	}
	return false
}
