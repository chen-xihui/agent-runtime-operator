// Package federation 提供多集群联邦（M5 生产化）。
// 管理跨集群信任（FederationPolicy）与跨集群 Agent 路由，
// 使本地无法满足的 Agent 任务能经联邦转发到远程集群。
package federation

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
)

// ClusterRole 集群角色
type ClusterRole string

// 集群角色常量
const (
	RoleHub     ClusterRole = "hub"     // 中心控制
	RoleSpoke   ClusterRole = "spoke"   // 边缘执行
	RoleStandalone ClusterRole = "standalone"
)

// Cluster 联邦集群描述
type Cluster struct {
	// Name 集群唯一名（跨集群路由寻址）
	Name string `json:"name"`
	// Endpoint 远程集群 API 端点（CRD/工具服务地址）
	Endpoint string `json:"endpoint"`
	// Role 集群角色
	Role ClusterRole `json:"role"`
	// TrustedFrom 允许向其委派任务的集群集合
	TrustedFrom []string `json:"trustedFrom,omitempty"`
}

// Policy 联邦策略（FederationPolicy）
// 决定哪些跨集群委派被允许（D-4 双向信任）。
type Policy struct {
	// From 本集群名
	From string `json:"from"`
	// To 目标集群名
	To string `json:"to"`
	// Allowed 是否允许 From 向 To 委派
	Allowed bool `json:"allowed"`
}

// Router 跨集群 Agent 路由（联邦转发）
type Router struct {
	mu       sync.RWMutex
	clusters map[string]*Cluster
	// routeFn 实际跨集群转发函数（注入，可测）
	routeFn func(ctx context.Context, fromCluster, toCluster, agentID string, payload map[string]interface{}) (map[string]interface{}, error)
}

// NewRouter 创建联邦路由器
func NewRouter() *Router {
	return &Router{clusters: make(map[string]*Cluster)}
}

// WithRouteFn 设置跨集群转发实现（注入）
func (r *Router) WithRouteFn(fn func(ctx context.Context, fromCluster, toCluster, agentID string, payload map[string]interface{}) (map[string]interface{}, error)) *Router {
	r.routeFn = fn
	return r
}

// RegisterCluster 注册/更新联邦集群
func (r *Router) RegisterCluster(c *Cluster) error {
	if c == nil || c.Name == "" {
		return fmt.Errorf("federation: cluster name required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clusters[c.Name] = c
	return nil
}

// RemoveCluster 注销集群
func (r *Router) RemoveCluster(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clusters, name)
}

// ListClusters 列出集群（确定性排序）
func (r *Router) ListClusters() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.clusters))
	for n := range r.clusters {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Allowed 判断跨集群委派是否被策略允许（双向信任，D-4）
// 要求：to 集群显式信任 from 集群（from 在 to.TrustedFrom 中）
func (r *Router) Allowed(from, to string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	toCluster, ok := r.clusters[to]
	if !ok {
		return false
	}
	return slices.Contains(toCluster.TrustedFrom, from)
}

// Route 将任务跨集群路由到目标 Agent（联邦转发）
// 校验目标集群存在且本集群被信任（双向信任，D-4）。
func (r *Router) Route(ctx context.Context, fromCluster, toCluster, agentID string, payload map[string]interface{}) (map[string]interface{}, error) {
	if !r.Allowed(fromCluster, toCluster) {
		return nil, fmt.Errorf("federation: cross-cluster delegation %s->%s not allowed (D-4)", fromCluster, toCluster)
	}
	if r.routeFn == nil {
		return nil, fmt.Errorf("federation: route function not configured")
	}
	return r.routeFn(ctx, fromCluster, toCluster, agentID, payload)
}

// Lookup 查找跨集群可承载指定技能的 Agent（联邦发现）
// 简化：返回可委派的目标集群列表（真实技能发现需远程集群 AgentCard）。
func (r *Router) Lookup(fromCluster, skill string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var targets []string
	for name, c := range r.clusters {
		if name == fromCluster {
			continue
		}
		// 本集群必须被目标集群信任
		if slices.Contains(c.TrustedFrom, fromCluster) {
			targets = append(targets, name)
		}
	}
	sort.Strings(targets)
	return targets, nil
}

// IsValidClusterName 校验集群名（简单规则，防注入）
func IsValidClusterName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
