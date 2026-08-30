package audit

import (
	"context"
	"testing"
	"time"
)

// 集成测试：需要本机 NATS（docker run -d --name nats -p 4222:4222 nats:2.10 -js）
// 若无 NATS 则跳过。
func TestNatsStore_WriteAndQuery(t *testing.T) {
	store, err := NewNatsStore(NatsConfig{URL: "nats://127.0.0.1:4222"})
	if err != nil {
		t.Skipf("NATS not available, skipping: %v", err)
	}
	t.Cleanup(store.Close)
	ctx := context.Background()

	// 写入两条审计记录
	if err := store.Write(ctx, &Record{
		TenantID: "tenant-a", AgentID: "reviewer", Action: ActionToolCall, Resource: "db.query",
		Success: true, Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := store.Write(ctx, &Record{
		TenantID: "tenant-a", AgentID: "other", Action: ActionToolCall, Resource: "code.search",
		Success: true, Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("write2: %v", err)
	}

	// 按租户查询（应至少 2 条）
	records, err := store.Query(ctx, Filter{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(records) < 2 {
		t.Fatalf("records = %d, want >= 2", len(records))
	}

	// 按 agent 过滤
	records, _ = store.Query(ctx, Filter{TenantID: "tenant-a", AgentID: "reviewer"})
	found := false
	for _, r := range records {
		if r.AgentID == "reviewer" && r.Resource == "db.query" {
			found = true
		}
	}
	if !found {
		t.Fatalf("filtered records = %+v", records)
	}
}
