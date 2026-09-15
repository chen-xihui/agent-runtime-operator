package audit

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStore_WriteAndQuery(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	s.Write(ctx, &Record{ID: "1", TenantID: "tenant-a", AgentID: "agent-x", Action: ActionToolCall, Resource: "db.query", Success: true, Timestamp: time.Now()})
	s.Write(ctx, &Record{ID: "2", TenantID: "tenant-a", AgentID: "agent-y", Action: ActionToolCall, Resource: "code.search", Success: true, Timestamp: time.Now().Add(-time.Minute)})
	s.Write(ctx, &Record{ID: "3", TenantID: "tenant-b", AgentID: "agent-z", Action: ActionNetworkEgress, Resource: "api.example.com", Success: false, Timestamp: time.Now()})

	// 按租户过滤
	records, err := s.Query(ctx, Filter{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("tenant-a records = %d, want 2", len(records))
	}

	// 按 Agent + 动作过滤
	records, _ = s.Query(ctx, Filter{AgentID: "agent-x", Action: ActionToolCall})
	if len(records) != 1 || records[0].Resource != "db.query" {
		t.Fatalf("filtered records = %+v", records)
	}

	// 时间倒序 + Limit
	records, _ = s.Query(ctx, Filter{TenantID: "tenant-a", Limit: 1})
	if len(records) != 1 || records[0].ID != "1" {
		t.Fatalf("limit/time desc wrong: %+v", records)
	}
}

func TestMemoryStore_NilRecord(t *testing.T) {
	s := NewMemoryStore()
	if err := s.Write(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil record")
	}
}

func TestNoopStore(t *testing.T) {
	var s Store = NoopStore{}
	if err := s.Write(context.Background(), &Record{}); err != nil {
		t.Fatalf("noop write: %v", err)
	}
	if _, err := s.Query(context.Background(), Filter{}); err != nil {
		t.Fatalf("noop query: %v", err)
	}
}

// TestMemoryStore_CapacityEviction 容量上限：超过后淘汰最旧记录（防止无界增长）
func TestMemoryStore_CapacityEviction(t *testing.T) {
	s := NewMemoryStoreWithCapacity(3)
	ctx := context.Background()

	base := time.Now()
	for i := 0; i < 5; i++ {
		_ = s.Write(ctx, &Record{
			ID:        string(rune('a' + i)),
			TenantID:  "tenant-a",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}

	records, err := s.Query(ctx, Filter{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// 只保留最新 3 条：c/d/e（a、b 被淘汰）
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3 (capacity)", len(records))
	}
	got := map[string]bool{}
	for _, r := range records {
		got[r.ID] = true
	}
	if got["a"] || got["b"] {
		t.Fatalf("oldest records not evicted: %v", got)
	}
	for _, want := range []string{"c", "d", "e"} {
		if !got[want] {
			t.Fatalf("expected %s retained, got %v", want, got)
		}
	}
}
