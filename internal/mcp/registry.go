package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"golang.org/x/time/rate"
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
	// limits 工具级限流配置：toolName -> RateLimit
	limits map[string]RateLimit
	// limiters 按 <tenantID>/<agentID>/<toolName> 维度的令牌桶（真滑动窗口语义）
	limiters map[string]*rate.Limiter
}

// NewMemoryRegistry 创建内存工具注册中心
func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		tools:        make(map[string]*Tool),
		grants:       make(map[string]map[string]map[string]ToolGrant),
		tenantGrants: make(map[string]map[string]ToolGrant),
		limits:       make(map[string]RateLimit),
		limiters:     make(map[string]*rate.Limiter),
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
	// 清理该工具对应的所有令牌桶，避免内存泄漏
	suffix := "/" + name
	for k := range r.limiters {
		if strings.HasSuffix(k, suffix) {
			delete(r.limiters, k)
		}
	}
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

	// 限流检查（按租户/Agent/工具维度的令牌桶，真滑动窗口语义）
	if err := r.checkRateLimit(tenantID, agentID, toolName); err != nil {
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

// checkRateLimit 基于令牌桶的限流（按 <tenantID>/<agentID>/<toolName> 维度隔离）。
// 语义：RPS 为每秒补充速率，Burst 为突发容量（未配置时取 RPS，最小 1）。
// 令牌桶会随时间自动补充，不存在"累计计数导致永久拒绝"的问题；
// 不同租户/Agent 之间互不影响（避免一个租户耗尽全局配额）。
func (r *MemoryRegistry) checkRateLimit(tenantID, agentID, toolName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	lim, ok := r.limits[toolName]
	if !ok || lim.RPS <= 0 {
		return nil
	}
	key := tenantID + "/" + agentID + "/" + toolName
	l, ok := r.limiters[key]
	if !ok {
		burst := lim.Burst
		if burst <= 0 {
			burst = lim.RPS
		}
		if burst < 1 {
			burst = 1
		}
		l = rate.NewLimiter(rate.Limit(lim.RPS), burst)
		r.limiters[key] = l
	}
	if !l.Allow() {
		return fmt.Errorf("mcp: rate limit exceeded for tool %q (tenant=%s agent=%s)", toolName, tenantID, agentID)
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

// RedactedPlaceholder 脱敏占位符
const RedactedPlaceholder = "[REDACTED]"

// RedactValues 按字段名列表对返回值脱敏（DLP，P1-1）。
// 递归遍历嵌套 map / slice，命中字段名（大小写不敏感）即替换为占位符，
// 避免嵌套结构中的敏感字段（如 {"data": {"token": "..."}}）绕过脱敏。
// 返回值：脱敏后的对象与是否发生脱敏（避免污染调用方原始数据时无法判断）。
func RedactValues(result map[string]any, redactFields []string) map[string]any {
	if len(redactFields) == 0 || result == nil {
		return result
	}
	redactSet := make(map[string]struct{}, len(redactFields))
	for _, f := range redactFields {
		redactSet[strings.ToLower(f)] = struct{}{}
	}
	out, _ := redactValue(result, redactSet).(map[string]any)
	return out
}

// redactValue 递归脱敏任意值（map / slice / 标量），返回深拷贝后的结果。
func redactValue(v any, redactSet map[string]struct{}) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if _, hit := redactSet[strings.ToLower(k)]; hit {
				out[k] = RedactedPlaceholder
				continue
			}
			out[k] = redactValue(val, redactSet)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = redactValue(item, redactSet)
		}
		return out
	default:
		return v
	}
}

// InjectScope 将数据范围过滤条件注入工具请求参数（跨租户数据不可达）。
//
// 安全语义（P1-4，必须在测试中固化）：
//   - scope.Filter 中的键**始终覆盖** args 中同名键——调用方无法通过传入
//     同名参数（如 tenant、userId）绕过数据范围限制；
//   - 返回新的 map，不修改调用方原始 args；
//   - scope 为空时原样返回 args（不额外拷贝，调用方不应再修改）。
func InjectScope(args map[string]any, scope *DataScope) map[string]any {
	if scope == nil || len(scope.Filter) == 0 {
		return args
	}
	out := make(map[string]any, len(args)+len(scope.Filter))
	// 先拷贝调用方参数，再注入 scope（scope 后写 → 覆盖同名键）
	for k, v := range args {
		out[k] = v
	}
	for k, v := range scope.Filter {
		out[k] = v
	}
	return out
}
