package a2a

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryGateway_RegisterAndDiscover(t *testing.T) {
	g := NewMemoryGateway()
	ctx := context.Background()

	g.Register(ctx, "code-reviewer", "tenant-a", &AgentCard{
		Description: "reviews pull requests",
		Skills:      []AgentSkill{{ID: "review_code", Name: "review"}},
	})
	g.Register(ctx, "comment-bot", "tenant-a", &AgentCard{
		Description: "posts comments",
		Skills:      []AgentSkill{{ID: "post_comment", Name: "comment"}},
	})
	// 其他租户的 Agent 不应被发现（D-4）
	g.Register(ctx, "tenant-b-agent", "tenant-b", &AgentCard{
		Description: "other tenant agent",
	})

	// 按技能发现
	agents, err := g.Discover(ctx, "tenant-a", "", []string{"review_code"})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(agents) != 1 || agents[0].Name != "code-reviewer" {
		t.Fatalf("discover by skill = %+v", agents)
	}

	// 关键词发现（不应包含其他租户）
	agents, _ = g.Discover(ctx, "tenant-a", "reviews", nil)
	if len(agents) != 1 {
		t.Fatalf("discover by keyword should find 1, got %d", len(agents))
	}
}

func TestMemoryGateway_SendTaskCrossTenant(t *testing.T) {
	g := NewMemoryGateway()
	ctx := context.Background()

	g.Register(ctx, "alice", "tenant-a", &AgentCard{Skills: []AgentSkill{{ID: "a"}}})
	g.Register(ctx, "bob", "tenant-b", &AgentCard{Skills: []AgentSkill{{ID: "b"}}})

	var called bool
	g.WithTaskHandler(func(ctx context.Context, to string, task *TaskMessage) (*TaskResult, error) {
		called = true
		return &TaskResult{TaskID: task.ID, State: "completed"}, nil
	})

	// 跨租户默认禁止（D-4）
	if _, err := g.SendTask(ctx, "alice", "bob", &TaskMessage{ID: "t1"}); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("expected ErrCrossTenant, got %v", err)
	}
	if called {
		t.Fatal("task should not be called cross-tenant")
	}

	// 建立联邦信任后允许
	g.AddFederation("tenant-a", "tenant-b")
	_, err := g.SendTask(ctx, "alice", "bob", &TaskMessage{ID: "t2"})
	if err != nil {
		t.Fatalf("federated task should succeed: %v", err)
	}
	if !called {
		t.Fatal("task handler not called after federation")
	}
}

func TestMemoryGateway_UnregisteredAgent(t *testing.T) {
	g := NewMemoryGateway()
	g.Register(context.Background(), "alice", "tenant-a", &AgentCard{})

	// 目标未注册
	if _, err := g.SendTask(context.Background(), "alice", "ghost", &TaskMessage{}); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}
