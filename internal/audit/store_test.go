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
