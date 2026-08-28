package mcp

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryRegistry_RegisterAndAuthorize(t *testing.T) {
	r := NewMemoryRegistry()

	// 注册工具
	tool := &Tool{Name: "db.query", Endpoint: "mcp-db:50051", Auth: "bearer"}
	if err := r.Register(context.Background(), tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	// 重复注册报错
	if err := r.Register(context.Background(), tool); !errors.Is(err, ErrToolExists) {
		t.Fatalf("expected ErrToolExists, got %v", err)
	}

	// 未授权调用
	if _, _, err := r.Authorize(context.Background(), "tenant-a", "agent-x", "db.query", nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}

	// 绑定授权（租户级默认）
	r.BindToolGrant("tenant-a", "", map[string]ToolGrant{
		"db.query": {DataScope: map[string]any{"tenant": "tenant-a"}},
	})

	// 授权成功，返回数据范围（ABAC 注入）
	got, scope, err := r.Authorize(context.Background(), "tenant-a", "agent-x", "db.query", nil)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if got.Name != "db.query" {
		t.Fatalf("tool name = %q", got.Name)
	}
	if scope.Filter["tenant"] != "tenant-a" {
		t.Fatalf("scope filter = %v, want tenant=tenant-a", scope.Filter)
	}

	// 未注册工具
	if _, _, err := r.Authorize(context.Background(), "tenant-a", "agent-x", "nope", nil); !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("expected ErrToolNotFound, got %v", err)
	}
}

func TestMemoryRegistry_AgentSpecificGrant(t *testing.T) {
	r := NewMemoryRegistry()
	_ = r.Register(context.Background(), &Tool{Name: "code.search"})

	// 只有特定 Agent 有权限
	r.BindToolGrant("tenant-a", "reviewer", map[string]ToolGrant{
		"code.search": {Redact: []string{"token"}},
	})

	if _, _, err := r.Authorize(context.Background(), "tenant-a", "other", "code.search", nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected unauthorized for other agent, got %v", err)
	}
	_, scope, err := r.Authorize(context.Background(), "tenant-a", "reviewer", "code.search", nil)
	if err != nil {
		t.Fatalf("authorize reviewer: %v", err)
	}
	if len(scope.Redact) != 1 || scope.Redact[0] != "token" {
		t.Fatalf("redact = %v, want [token]", scope.Redact)
	}
}

func TestMemoryRegistry_List(t *testing.T) {
	r := NewMemoryRegistry()
	_ = r.Register(context.Background(), &Tool{Name: "b.tool"})
	_ = r.Register(context.Background(), &Tool{Name: "a.tool"})

	list, err := r.List(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].Name != "a.tool" || list[1].Name != "b.tool" {
		t.Fatalf("list order wrong: %+v", list)
	}
}

func TestInjectScope_And_Redact(t *testing.T) {
	args := InjectScope(map[string]any{"id": "1"}, &DataScope{Filter: map[string]any{"tenant": "tenant-a"}})
	if args["tenant"] != "tenant-a" || args["id"] != "1" {
		t.Fatalf("inject scope = %v", args)
	}

	res := RedactValues(map[string]any{"token": "secret", "ok": true}, []string{"token"})
	if res["token"] != "[REDACTED]" {
		t.Fatalf("redact failed: %v", res)
	}
	if res["ok"] != true {
		t.Fatalf("non-redact field changed: %v", res)
	}
}
