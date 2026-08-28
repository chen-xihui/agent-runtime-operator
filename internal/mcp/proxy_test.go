package mcp

import (
	"context"
	"testing"
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
