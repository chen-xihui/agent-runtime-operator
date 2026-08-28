package registration

import (
	"context"
	"testing"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/a2a"
	"github.com/example/agent-runtime-operator/internal/mcp"
)

// fakeMCP 测试替身
type fakeMCP struct {
	registered []string
	grants     map[string]map[string]mcp.ToolGrant
}

func (f *fakeMCP) Register(ctx context.Context, tool *mcp.Tool) error {
	f.registered = append(f.registered, tool.Name)
	return nil
}
func (f *fakeMCP) Unregister(ctx context.Context, name string) error { return nil }
func (f *fakeMCP) BindToolGrant(tenantID, agentID string, grants map[string]mcp.ToolGrant) {
	if f.grants == nil {
		f.grants = map[string]map[string]mcp.ToolGrant{}
	}
	f.grants[tenantID+"|"+agentID] = grants
}

// fakeGateway 测试替身
type fakeGateway struct {
	cards map[string]string // agentID -> tenantID
}

func (f *fakeGateway) Register(ctx context.Context, agentID, tenantID string, card *a2a.AgentCard) error {
	if f.cards == nil {
		f.cards = map[string]string{}
	}
	f.cards[agentID] = tenantID
	return nil
}

func TestSyncer_SyncAgentTools(t *testing.T) {
	m := &fakeMCP{}
	s := NewSyncer(m, &fakeGateway{})

	tb := agentv1.ToolBinding{}
	tb.Name = "tb-review"
	tb.Spec.Tools = []agentv1.ToolGrant{
		{
			Name:      "code.search",
			DataScope: map[string]interface{}{"tenant": "tenant-a"},
			Redact:    []string{"token"},
			RateLimit: agentv1.RateLimit{RPS: 5},
		},
	}

	ep := agentv1.MCPEndpoint{}
	ep.Name = "code.search"
	ep.Spec.Address = "mcp-code:50051"
	ep.Spec.Auth = agentv1.AuthRef{Type: "bearer"}

	// AgentRefs 为空 → 租户内全部生效
	if err := s.SyncAgentTools(context.Background(), "tenant-a", "reviewer", []agentv1.ToolBinding{tb}, map[string]agentv1.MCPEndpoint{ep.Name: ep}); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// 工具应注册
	if len(m.registered) != 1 || m.registered[0] != "code.search" {
		t.Fatalf("registered = %v, want [code.search]", m.registered)
	}
	// 授权应绑定
	grants := m.grants["tenant-a|reviewer"]
	if grants == nil {
		t.Fatal("grant not bound")
	}
	g := grants["code.search"]
	if g.DataScope["tenant"] != "tenant-a" {
		t.Fatalf("datascope = %v", g.DataScope)
	}
	if len(g.Redact) != 1 || g.Redact[0] != "token" {
		t.Fatalf("redact = %v", g.Redact)
	}
}

func TestSyncer_SyncAgentTools_AgentRefsFilter(t *testing.T) {
	m := &fakeMCP{}
	s := NewSyncer(m, &fakeGateway{})

	// 仅绑定特定 Agent 的 ToolBinding
	tb := agentv1.ToolBinding{}
	tb.Name = "tb-specific"
	tb.Spec.AgentRefs = []string{"reviewer"}
	tb.Spec.Tools = []agentv1.ToolGrant{{Name: "db.query"}}

	// 其他 Agent 不应获得授权
	if err := s.SyncAgentTools(context.Background(), "tenant-a", "other", []agentv1.ToolBinding{tb}, nil); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(m.grants["tenant-a|other"]) != 0 {
		t.Fatalf("other agent should not have grants, got %v", m.grants["tenant-a|other"])
	}

	// 目标 Agent 应获得授权
	if err := s.SyncAgentTools(context.Background(), "tenant-a", "reviewer", []agentv1.ToolBinding{tb}, nil); err != nil {
		t.Fatalf("sync reviewer: %v", err)
	}
	if _, ok := m.grants["tenant-a|reviewer"]["db.query"]; !ok {
		t.Fatalf("reviewer should have db.query grant")
	}
}

func TestSyncer_RegisterAgentCard(t *testing.T) {
	g := &fakeGateway{}
	m := &fakeMCP{}
	s := NewSyncer(m, g)

	// 未启用 A2A 不注册
	agent := &agentv1.Agent{}
	agent.Name = "no-a2a"
	agent.Spec.A2A.Enabled = false
	if err := s.RegisterAgentCard(context.Background(), "tenant-a", "no-a2a", agent); err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(g.cards) != 0 {
		t.Fatalf("should not register when a2a disabled, got %v", g.cards)
	}

	// 启用 A2A 注册
	agent = &agentv1.Agent{}
	agent.Name = "reviewer"
	agent.Spec.A2A.Enabled = true
	agent.Spec.A2A.Tasks = []string{"review_code", "post_comment"}
	if err := s.RegisterAgentCard(context.Background(), "tenant-a", "reviewer", agent); err != nil {
		t.Fatalf("register: %v", err)
	}
	if g.cards["reviewer"] != "tenant-a" {
		t.Fatalf("agent card not registered correctly: %v", g.cards)
	}
}

func TestBuildAgentCard(t *testing.T) {
	agent := &agentv1.Agent{}
	agent.Name = "code-reviewer"
	agent.Spec.A2A.Enabled = true
	agent.Spec.A2A.Tasks = []string{"review_code"}

	card := BuildAgentCard(agent)
	if len(card.Skills) != 1 || card.Skills[0].ID != "review_code" {
		t.Fatalf("skills = %+v", card.Skills)
	}
	if card.Name != "code-reviewer" {
		t.Fatalf("card name = %q", card.Name)
	}
}
