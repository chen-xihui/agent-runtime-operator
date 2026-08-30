package a2a

import "context"

// Federator 跨集群联邦路由器接口（M5 多集群联邦）
// 抽象自 federation.Router，供 A2A Gateway 做跨集群 Agent 委派，避免包循环依赖。
type Federator interface {
	// Allowed 判断本集群是否被目标集群信任（D-4 双向信任）
	Allowed(from, to string) bool
	// Route 跨集群路由委派任务到远程 Agent
	Route(ctx context.Context, fromCluster, toCluster, agentID string, payload map[string]interface{}) (map[string]interface{}, error)
	// Lookup 联邦发现可承载技能的信任集群
	Lookup(fromCluster, skill string) ([]string, error)
}
