package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 常见错误
var (
	ErrToolNotFound      = errors.New("mcp: tool not found")
	ErrUnauthorized      = errors.New("mcp: unauthorized tool access")
	ErrToolExists        = errors.New("mcp: tool already registered")
)

// ToolBinding 描述一个 Agent↔工具授权条目（来自 ToolBinding CRD 或显式注入）
// 对应 api/v1.ToolGrant + ToolBindingSpec 的权限语义（R-4）
type ToolBinding struct {
	// Agent 该绑定适用的 Agent 名（空表示租户内全部）
	Agent string
	// Tools 授权的工具及数据范围
	Tools map[string]ToolGrant
}

// ToolGrant 单个工具的授权及数据范围（数据级 ABAC，P1-4）
type ToolGrant struct {
	// DataScope 注入到工具请求的过滤条件，如 {tenant: "tenant-a"}
	DataScope map[string]any
	// RateLimit 调用配额
	RateLimit RateLimit
	// Redact 需脱敏的返回字段
	Redact []string
}

// MemoryRegistry 基于内存存储的 MCP 工具注册与鉴权中心
type MemoryRegistry struct {
	mu     sync.RWMutex
	tools  map[string]*Tool                       // 工具名 -> 工具描述
	grants map[string]map[string]map[string]ToolGrant // tenantID -> (agentID -> (toolName -> 授权))
	// 租户级默认授权：tenantID -> (toolName -> 授权)（Agent 为空时）
	tenantGrants map[string]map[string]ToolGrant
	// 调用计数（用于限流）
	callCounts map[string]int
	limits     map[string]RateLimit
}

// NewMemoryRegistry 创建内存工具注册中心
func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		tools:        make(map[string]*Tool),
		grants:       make(map[string]map[string]map[string]ToolGrant),
		tenantGrants: make(map[string]map[string]ToolGrant),
		callCounts:   make(map[string]int),
		limits:       make(map[string]RateLimit),
	}
}

// Register 注册一个全局工具（平台管理员）
func (r *MemoryRegistry) Register(ctx context.Context, tool *Tool) error {
	if tool == nil || tool.Name == "" {
		return fmt.Errorf("mcp: tool name required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[tool.Name]; exists {
		return ErrToolExists
	}
	cp := *tool
	r.tools[tool.Name] = &cp
	if tool.RateLimit.RPS > 0 {
		r.limits[tool.Name] = tool.RateLimit
	}
	return nil
}

// Unregister 注销一个工具
func (r *MemoryRegistry) Unregister(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
	delete(r.limits, name)
	return nil
}

// BindToolGrant 为租户下某 Agent（或租户全部）绑定工具授权（来自 ToolBinding CRD）
func (r *MemoryRegistry) BindToolGrant(tenantID, agentID string, grants map[string]ToolGrant) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if agentID == "" {
		r.tenantGrants[tenantID] = grants
		return
	}
	if r.grants[tenantID] == nil {
		r.grants[tenantID] = make(map[string]map[string]ToolGrant)
	}
	r.grants[tenantID][agentID] = grants
}

// Authorize 校验租户+Agent 是否有权调用该工具，返回工具与数据范围（数据级 ABAC，P1-4）
func (r *MemoryRegistry) Authorize(ctx context.Context, tenantID, agentID, toolName string, params map[string]any) (*Tool, *DataScope, error) {
	r.mu.RLock()
	tool, toolOK := r.tools[toolName]
	r.mu.RUnlock()

	if !toolOK {
		return nil, nil, ErrToolNotFound
	}

	// 查找授权：先精确到 Agent，再回退到租户级默认
	grant, ok := r.lookupGrant(tenantID, agentID, toolName)
	if !ok {
		return nil, nil, ErrUnauthorized
	}

	// 数据级过滤（P1-4）：从 DataScope 构建注入过滤条件
	scope := &DataScope{
		Filter: grant.DataScope,
		Redact: grant.Redact,
	}

	// 限流检查（简化计数器实现）
	if err := r.checkRateLimit(toolName); err != nil {
		return nil, nil, err
	}

	cp := *tool
	return &cp, scope, nil
}

func (r *MemoryRegistry) lookupGrant(tenantID, agentID, toolName string) (ToolGrant, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if m, ok := r.grants[tenantID]; ok {
		if g, ok2 := m[agentID]; ok2 {
			gr, ok3 := g[toolName]
			if ok3 {
				return gr, true
			}
		}
	}
	if m, ok := r.tenantGrants[tenantID]; ok {
		gr, ok2 := m[toolName]
		if ok2 {
			return gr, true
		}
	}
	return ToolGrant{}, false
}

// checkRateLimit 简化滑动窗口限流（按累计调用数）
func (r *MemoryRegistry) checkRateLimit(toolName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	lim, ok := r.limits[toolName]
	if !ok || lim.RPS <= 0 {
		return nil
	}
	r.callCounts[toolName]++
	if r.callCounts[toolName] > lim.RPS*60 { // 每 60s 窗口内的简化上限
		return fmt.Errorf("mcp: rate limit exceeded for tool %q", toolName)
	}
	return nil
}

// List 返回租户可见的工具列表（按名排序，确定性）
func (r *MemoryRegistry) List(ctx context.Context, tenantID string) ([]*Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*Tool, 0, len(names))
	for _, n := range names {
		cp := *r.tools[n]
		out = append(out, &cp)
	}
	return out, nil
}

// RedactValues 按字段名列表对返回值脱敏（DLP，P1-1）
func RedactValues(result map[string]any, redactFields []string) map[string]any {
	if len(redactFields) == 0 {
		return result
	}
	redactSet := make(map[string]struct{}, len(redactFields))
	for _, f := range redactFields {
		redactSet[f] = struct{}{}
	}
	for k := range redactSet {
		if _, ok := result[k]; ok {
			result[k] = "[REDACTED]"
		}
	}
	return result
}

// InjectScope 将数据范围过滤条件注入工具请求参数（跨租户数据不可达）
func InjectScope(args map[string]any, scope *DataScope) map[string]any {
	if scope == nil || len(scope.Filter) == 0 {
		return args
	}
	out := make(map[string]any, len(args)+len(scope.Filter))
	for k, v := range args {
		out[k] = v
	}
	for k, v := range scope.Filter {
		out[k] = v
	}
	return out
}
