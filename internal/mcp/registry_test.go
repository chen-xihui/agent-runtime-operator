package mcp

import (
	"context"
	"errors"
	"testing"
	"time"
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

// TestInjectScope_OverridesCallerArgs 固化安全语义：scope 必须覆盖调用方同名参数（防越权）
func TestInjectScope_OverridesCallerArgs(t *testing.T) {
	args := map[string]any{"tenant": "tenant-b", "id": "1"} // 调用方试图伪造 tenant
	out := InjectScope(args, &DataScope{Filter: map[string]any{"tenant": "tenant-a"}})

	if out["tenant"] != "tenant-a" {
		t.Fatalf("scope must override caller args, got tenant=%v", out["tenant"])
	}
	if out["id"] != "1" {
		t.Fatalf("id lost: %v", out)
	}
	// 原始 args 不应被修改
	if args["tenant"] != "tenant-b" {
		t.Fatalf("caller args mutated: %v", args)
	}
}

// TestInjectScope_NoScopeReturnsOriginal 无 scope 时原样返回
func TestInjectScope_NoScopeReturnsOriginal(t *testing.T) {
	args := map[string]any{"id": "1"}
	if got := InjectScope(args, nil); got["id"] != "1" {
		t.Fatalf("nil scope: %v", got)
	}
	if got := InjectScope(args, &DataScope{}); got["id"] != "1" {
		t.Fatalf("empty scope: %v", got)
	}
}

// TestRedactValues_Nested 递归脱敏嵌套 map / slice（P0-3：防止嵌套绕过）
func TestRedactValues_Nested(t *testing.T) {
	in := map[string]any{
		"token": "top-secret",
		"user": map[string]any{
			"name":     "alice",
			"password": "p@ss",
			"meta": map[string]any{
				"apiKey": "k-123",
			},
		},
		"items": []any{
			map[string]any{"secret": "s1", "ok": "v1"},
			map[string]any{"secret": "s2"},
		},
		"list": []string{"plain"},
	}
	out := RedactValues(in, []string{"token", "password", "apiKey", "secret"})

	if out["token"] != RedactedPlaceholder {
		t.Fatalf("top-level not redacted: %v", out["token"])
	}
	user := out["user"].(map[string]any)
	if user["password"] != RedactedPlaceholder {
		t.Fatalf("nested password not redacted: %v", user["password"])
	}
	if user["name"] != "alice" {
		t.Fatalf("nested non-sensitive changed: %v", user["name"])
	}
	meta := user["meta"].(map[string]any)
	if meta["apiKey"] != RedactedPlaceholder {
		t.Fatalf("deep nested apiKey not redacted: %v", meta["apiKey"])
	}
	items := out["items"].([]any)
	for i, it := range items {
		m := it.(map[string]any)
		if m["secret"] != RedactedPlaceholder {
			t.Fatalf("slice[%d] secret not redacted: %v", i, m["secret"])
		}
	}
	if got := items[0].(map[string]any)["ok"]; got != "v1" {
		t.Fatalf("slice non-sensitive changed: %v", got)
	}
	// 原始数据不被修改
	if in["token"] != "top-secret" {
		t.Fatalf("input mutated: %v", in["token"])
	}
}

// TestRedactValues_CaseInsensitive 字段名大小写不敏感
func TestRedactValues_CaseInsensitive(t *testing.T) {
	out := RedactValues(map[string]any{"Token": "x", "PASSWORD": "y"}, []string{"token", "password"})
	if out["Token"] != RedactedPlaceholder || out["PASSWORD"] != RedactedPlaceholder {
		t.Fatalf("case-insensitive redact failed: %v", out)
	}
}

// TestCheckRateLimit_TokenBucket 令牌桶限流：超限拒绝，随时间恢复，且按维度隔离
func TestCheckRateLimit_TokenBucket(t *testing.T) {
	r := NewMemoryRegistry()
	// RPS=100, Burst=2 → 初始桶容量 2
	_ = r.Register(context.Background(), &Tool{Name: "t", RateLimit: RateLimit{RPS: 100, Burst: 2}})
	r.BindToolGrant("tenant-a", "", map[string]ToolGrant{"t": {}})
	r.BindToolGrant("tenant-b", "", map[string]ToolGrant{"t": {}})
	ctx := context.Background()

	// 前 2 次应通过（桶容量）
	for i := range 2 {
		if _, _, err := r.Authorize(ctx, "tenant-a", "agent-x", "t", nil); err != nil {
			t.Fatalf("call %d should pass: %v", i+1, err)
		}
	}
	// 第 3 次立即调用应被限流（尚未补充）
	if _, _, err := r.Authorize(ctx, "tenant-a", "agent-x", "t", nil); err == nil {
		t.Fatalf("3rd call should be rate limited")
	}

	// 不同租户独立配额（不被 tenant-a 的耗尽影响）
	if _, _, err := r.Authorize(ctx, "tenant-b", "agent-x", "t", nil); err != nil {
		t.Fatalf("other tenant should have independent quota: %v", err)
	}
}

// TestCheckRateLimit_NotPermanent 限流不是永久性的：等待后可恢复
func TestCheckRateLimit_NotPermanent(t *testing.T) {
	r := NewMemoryRegistry()
	// RPS=1000（每毫秒 1 个令牌），Burst=1 → 等待 ~5ms 可恢复
	_ = r.Register(context.Background(), &Tool{Name: "t", RateLimit: RateLimit{RPS: 1000, Burst: 1}})
	r.BindToolGrant("tenant-a", "", map[string]ToolGrant{"t": {}})
	ctx := context.Background()

	if _, _, err := r.Authorize(ctx, "tenant-a", "a", "t", nil); err != nil {
		t.Fatalf("1st call: %v", err)
	}
	if _, _, err := r.Authorize(ctx, "tenant-a", "a", "t", nil); err == nil {
		t.Fatalf("2nd call should be limited")
	}
	time.Sleep(20 * time.Millisecond)
	if _, _, err := r.Authorize(ctx, "tenant-a", "a", "t", nil); err != nil {
		t.Fatalf("should recover after wait (not permanent): %v", err)
	}
}

// TestUnregister_CleansLimiters 注销工具时清理令牌桶，避免内存泄漏
func TestUnregister_CleansLimiters(t *testing.T) {
	r := NewMemoryRegistry()
	_ = r.Register(context.Background(), &Tool{Name: "t", RateLimit: RateLimit{RPS: 10, Burst: 1}})
	r.BindToolGrant("tenant-a", "", map[string]ToolGrant{"t": {}})

	_, _, _ = r.Authorize(context.Background(), "tenant-a", "a", "t", nil)
	if len(r.limiters) != 1 {
		t.Fatalf("limiter not created: %d", len(r.limiters))
	}
	_ = r.Unregister(context.Background(), "t")
	if len(r.limiters) != 0 {
		t.Fatalf("limiters not cleaned after unregister: %d", len(r.limiters))
	}
}
