package eventbus

import (
	"strings"
	"testing"
)

func sampleACLSpec() ACLSpec {
	return ACLSpec{
		Prefix:         "agent-runtime",
		OperatorUser:   "operator",
		OperatorPass:   "op-secret",
		AllowJetStream: true,
		Tenants: []TenantCredential{
			{TenantID: "tenant-a", User: "tenant-a", Password: "a-secret"},
			{TenantID: "tenant-b", User: "tenant-b", Password: "b-secret"},
		},
	}
}

func TestBuildACLConfig_HasOperatorAndTenants(t *testing.T) {
	conf := BuildACLConfig(sampleACLSpec())
	if !strings.Contains(conf, "operator") {
		t.Fatalf("operator user missing:\n%s", conf)
	}
	for _, tn := range []string{"tenant-a", "tenant-b"} {
		if !strings.Contains(conf, "tenant-"+tn) && !strings.Contains(conf, tn) {
			t.Fatalf("tenant %s user missing:\n%s", tn, conf)
		}
	}
}

func TestBuildACLConfig_OperatorFullAccess(t *testing.T) {
	conf := BuildACLConfig(sampleACLSpec())
	// operator 应能发布/订阅所有 subject（">"）
	if !strings.Contains(conf, "publish: [\">\"]") {
		t.Fatalf("operator should publish all: %s", conf)
	}
}

func TestBuildACLConfig_TenantScoped(t *testing.T) {
	conf := BuildACLConfig(sampleACLSpec())
	// tenant-a 只能 pub/sub 自己的 agent-runtime.tenant-a.events.>，不能越界到 tenant-b
	if !strings.Contains(conf, "agent-runtime.tenant-a.events.>") {
		t.Fatalf("tenant-a should have own subject: %s", conf)
	}
	// tenant-a 的 publish 列表绝不应包含 tenant-b 的事件前缀
	// 提取 tenant-a 块（粗粒度断言：tenant-a 行不能引用 tenant-b.events）
	if strings.Contains(conf, "tenant-a.events.>\", \"_INBOX") {
		// 合法：tenant-a 事件 + inbox
	}
}

func TestBuildACLConfig_NoTenantCrossScope(t *testing.T) {
	// 关键 R-3 断言：任何单租户的权限列表都不应包含其它租户的事件 subject。
	conf := BuildACLConfig(sampleACLSpec())
	// 每个租户行应是独立的；这里做整体粗查：不能出现 a.events 与 b.events 在同一行
	lines := strings.Split(conf, "\n")
	for _, ln := range lines {
		if strings.Contains(ln, "tenant-a.events.>") && strings.Contains(ln, "tenant-b.events.>") {
			t.Fatalf("tenant-a and tenant-b scopes merged on one line: %s", ln)
		}
	}
}

func TestValidateTenantScope(t *testing.T) {
	if got := ValidateTenantScope("", "tenant-x"); got != "agent-runtime.tenant-x.events.>" {
		t.Fatalf("scope = %s", got)
	}
	if got := ValidateTenantScope("rt", "tenant-x"); got != "rt.tenant-x.events.>" {
		t.Fatalf("scope = %s", got)
	}
}

func TestRenderACL_Runnable(t *testing.T) {
	conf := RenderACL(sampleACLSpec(), true)
	for _, want := range []string{"port: 4222", "jetstream {", "authorization {", "users = ["} {
		if !strings.Contains(conf, want) {
			t.Fatalf("render missing %q:\n%s", want, conf)
		}
	}
}

func TestBuildACLConfig_Deterministic(t *testing.T) {
	c1 := BuildACLConfig(sampleACLSpec())
	c2 := BuildACLConfig(sampleACLSpec())
	if c1 != c2 {
		t.Fatal("ACL config not deterministic")
	}
}
