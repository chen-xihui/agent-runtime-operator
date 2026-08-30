package a2a

import (
	"context"
	"errors"
	"testing"
)

// fakeFederator 测试替身
type fakeFederator struct {
	allowed bool
	targets []string
	routed  []string
}

func (f *fakeFederator) Allowed(from, to string) bool { return f.allowed }
func (f *fakeFederator) Route(ctx context.Context, from, to, agentID string, payload map[string]interface{}) (map[string]interface{}, error) {
	f.routed = append(f.routed, to)
	return map[string]interface{}{"ok": true}, nil
}
func (f *fakeFederator) Lookup(from, skill string) ([]string, error) { return f.targets, nil }

func TestMemoryGateway_SendTask_CrossCluster(t *testing.T) {
	ctx := context.Background()
	g := NewMemoryGateway()
	// 本地注册 alice，不注册远程 agent
	g.Register(ctx, "alice", "tenant-a", &AgentCard{Skills: []AgentSkill{{ID: "a"}}})

	f := &fakeFederator{allowed: true, targets: []string{"cluster-b"}}
	g.WithFederator("cluster-a", f)

	// 本地 Agent → 本地委派
	if _, err := g.SendTask(ctx, "alice", "alice", &TaskMessage{ID: "t1"}); err != nil {
		t.Fatalf("local task: %v", err)
	}
	if len(f.routed) != 0 {
		t.Fatalf("local task should not route cross-cluster, routed=%v", f.routed)
	}

	// 远程 Agent → 跨集群委派
	res, err := g.SendTask(ctx, "alice", "remote-agent", &TaskMessage{ID: "t2"})
	if err != nil {
		t.Fatalf("cross-cluster task: %v", err)
	}
	if len(f.routed) != 1 || f.routed[0] != "cluster-b" {
		t.Fatalf("routed = %v, want [cluster-b]", f.routed)
	}
	if res.Message != "delegated to cluster cluster-b" {
		t.Fatalf("message = %q", res.Message)
	}
}

func TestMemoryGateway_SendTask_CrossClusterNotAllowed(t *testing.T) {
	ctx := context.Background()
	g := NewMemoryGateway()
	g.Register(ctx, "alice", "tenant-a", &AgentCard{})

	f := &fakeFederator{allowed: false, targets: []string{"cluster-b"}}
	g.WithFederator("cluster-a", f)

	// 目标集群不信任本集群（D-4）→ 找不到 Agent
	if _, err := g.SendTask(ctx, "alice", "remote-agent", &TaskMessage{ID: "t"}); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestMemoryGateway_SendTask_NoFederator(t *testing.T) {
	ctx := context.Background()
	g := NewMemoryGateway()
	g.Register(ctx, "alice", "tenant-a", &AgentCard{})

	// 未配置联邦，远程 Agent → ErrAgentNotFound
	if _, err := g.SendTask(ctx, "alice", "remote-agent", &TaskMessage{ID: "t"}); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("expected ErrAgentNotFound without federator, got %v", err)
	}
}
