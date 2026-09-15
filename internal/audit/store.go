// Package audit 提供 DLP 全量出网审计存储（P1-1）。
// 记录 Agent 的工具调用（MCP Proxy）、网络出网、敏感数据访问等审计事件，
// 满足合规与安全审计要求。
package audit

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Record 审计记录
type Record struct {
	// ID 记录唯一标识
	ID string `json:"id"`
	// TenantID 租户
	TenantID string `json:"tenantId"`
	// AgentID Agent
	AgentID string `json:"agentId"`
	// Action 动作类型（tool_call / network_egress / data_access / ...）
	Action string `json:"action"`
	// Resource 被访问资源（工具名 / 域名 / 敏感字段）
	Resource string `json:"resource"`
	// Success 是否成功
	Success bool `json:"success"`
	// Error 失败原因
	Error string `json:"error,omitempty"`
	// ArgsHash 参数指纹（避免落明文敏感参数）
	ArgsHash string `json:"argsHash,omitempty"`
	// Timestamp 审计时间
	Timestamp time.Time `json:"timestamp"`
}

// 动作类型常量
const (
	ActionToolCall     = "tool_call"
	ActionNetworkEgress = "network_egress"
	ActionDataAccess    = "data_access"
)

// Filter 审计查询过滤条件
type Filter struct {
	TenantID string
	AgentID  string
	Action   string
	Resource string
	// Limit 返回条数上限
	Limit int
}

// Store 审计存储接口
type Store interface {
	// Write 写入一条审计记录
	Write(ctx context.Context, r *Record) error
	// Query 按条件查询（按时间倒序）
	Query(ctx context.Context, f Filter) ([]*Record, error)
}

// 内存审计存储默认容量上限（超出淘汰最旧记录，避免无界增长）
const defaultMemoryStoreCap = 10000

// MemoryStore 基于内存的审计存储。
//
// ⚠️ 仅适用于开发/测试/单实例场景：记录仅存于进程内，重启即丢失，
// 且多副本之间不共享。生产环境请使用 NatsStore（JetStream 持久化，
// internal/audit/nats_store.go）或外置数据库。
//
// 存储为有界环形语义：超过 capacity 时淘汰最旧记录。
type MemoryStore struct {
	mu       sync.RWMutex
	records  []*Record
	capacity int
}

// NewMemoryStore 创建内存审计存储（默认容量 defaultMemoryStoreCap）
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{capacity: defaultMemoryStoreCap}
}

// NewMemoryStoreWithCapacity 创建指定容量的内存审计存储（便于测试与容量评估）
func NewMemoryStoreWithCapacity(capacity int) *MemoryStore {
	if capacity <= 0 {
		capacity = defaultMemoryStoreCap
	}
	return &MemoryStore{capacity: capacity}
}

// Write 写入审计记录
func (s *MemoryStore) Write(ctx context.Context, r *Record) error {
	if r == nil {
		return fmt.Errorf("audit: nil record")
	}
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
	// 容量淘汰：超过上限时丢弃最旧记录（保持有界内存）
	if cap := s.capacity; cap > 0 && len(s.records) > cap {
		// 复制到新切片，避免底层数组无限增长（否则 append 会持续扩容）
		trimmed := make([]*Record, cap)
		copy(trimmed, s.records[len(s.records)-cap:])
		s.records = trimmed
	}
	return nil
}

// Query 按条件查询（按时间倒序）
func (s *MemoryStore) Query(ctx context.Context, f Filter) ([]*Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []*Record
	for _, r := range s.records {
		if f.TenantID != "" && r.TenantID != f.TenantID {
			continue
		}
		if f.AgentID != "" && r.AgentID != f.AgentID {
			continue
		}
		if f.Action != "" && r.Action != f.Action {
			continue
		}
		if f.Resource != "" && r.Resource != f.Resource {
			continue
		}
		out = append(out, r)
	}
	// 时间倒序
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.After(out[j].Timestamp)
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// NoopStore 空实现（默认，避免 nil 崩溃）
type NoopStore struct{}

// Write 空实现
func (NoopStore) Write(ctx context.Context, r *Record) error { return nil }

// Query 空实现
func (NoopStore) Query(ctx context.Context, f Filter) ([]*Record, error) { return nil, nil }
