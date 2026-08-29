package mcp

import (
	"context"
	"testing"

	"github.com/example/agent-runtime-operator/internal/audit"
)

func TestMemoryProxy_InvokeFlow(t *testing.T) {
	r := NewMemoryRegistry()
	_ = r.Register(context.Background(), &Tool{
		Name:     "db.query",
		Endpoint: "mcp-db:50051",
	})
	r.BindToolGrant("tenant-a", "agent-x", map[string]ToolGrant{
		"db.query": {
			DataScope: map[string]any{"tenant": "tenant-a"},
			Redact:    []string{"secret"},
		},
	})

	var audited bool
	proxy := NewMemoryProxy("tenant-a", r)
	proxy.WithInvoker(func(ctx context.Context, tool *Tool, args map[string]any) (map[string]any, error) {
		// 校验数据级过滤条件已注入（跨租户数据不可达）
		if args["tenant"] != "tenant-a" {
			t.Fatalf("data scope not injected: %v", args)
		}
		return map[string]any{"rows": 10, "secret": "hidden"}, nil
	})
	proxy.WithAudit(func(tID, aID, toolName string, args, result map[string]any, err error) {
		audited = true
		if tID != "tenant-a" || aID != "agent-x" || toolName != "db.query" {
			t.Fatalf("audit args wrong: %s %s %s", tID, aID, toolName)
		}
	})

	result, err := proxy.Invoke(context.Background(), "agent-x", "db.query", map[string]any{"id": "1"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if result["secret"] != "[REDACTED]" {
		t.Fatalf("secret not redacted: %v", result)
	}
	if result["rows"] != 10 {
		t.Fatalf("rows = %v", result["rows"])
	}
	if !audited {
		t.Fatal("audit not called")
	}
}

func TestMemoryProxy_Unauthorized(t *testing.T) {
	r := NewMemoryRegistry()
	_ = r.Register(context.Background(), &Tool{Name: "db.query"})
	// 无授权绑定
	proxy := NewMemoryProxy("tenant-a", r)

	if _, err := proxy.Invoke(context.Background(), "agent-x", "db.query", nil); err != ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestMemoryProxy_AuditStore(t *testing.T) {
	r := NewMemoryRegistry()
	_ = r.Register(context.Background(), &Tool{Name: "db.query"})
	r.BindToolGrant("tenant-a", "agent-x", map[string]ToolGrant{
		"db.query": {},
	})

	store := audit.NewMemoryStore()
	proxy := NewMemoryProxy("tenant-a", r)
	proxy.WithAuditStore(store)

	// 成功调用
	if _, err := proxy.Invoke(context.Background(), "agent-x", "db.query", nil); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	records, err := store.Query(context.Background(), audit.Filter{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(records) != 1 || records[0].Resource != "db.query" || !records[0].Success {
		t.Fatalf("audit record wrong: %+v", records)
	}
	if records[0].Action != audit.ActionToolCall {
		t.Fatalf("action = %q, want tool_call", records[0].Action)
	}

	// 未授权调用也记录（失败审计）
	if _, err := proxy.Invoke(context.Background(), "agent-x", "nonexistent", nil); err == nil {
		t.Fatal("expected error for nonexistent tool")
	}
	records, _ = store.Query(context.Background(), audit.Filter{TenantID: "tenant-a"})
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	// 应有一条失败记录（nonexistent 工具）
	foundFailed := false
	for _, r := range records {
		if r.Resource == "nonexistent" && !r.Success {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Fatalf("expected a failed audit record for nonexistent tool: %+v", records)
	}
}
