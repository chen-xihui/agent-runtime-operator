package eventbus

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// 集成测试：依赖本机 NATS（nats://127.0.0.1:4222），未启动则跳过（与 nats_test.go 一致）。
// 验证基于 JetStream 的历史回放（DeliverAll）：先发布历史事件，再回放读取。

func TestReplayJetStream_ReplaysPublishedHistory(t *testing.T) {
	nc, err := nats.Connect("nats://127.0.0.1:4222")
	if err != nil {
		t.Skipf("NATS not available, skipping: %v", err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Skipf("JetStream not available, skipping: %v", err)
	}

	const stream = "test-replay-stream"
	_, _ = js.AddStream(&nats.StreamConfig{Name: stream, Subjects: []string{"test.>"}, Storage: nats.FileStorage})

	// 发布 3 个历史事件
	pub := []*CloudEvent{
		{ID: "ev-1", Type: EventNodeStarted, TenantID: "tenant-a", Source: "wf/1", Time: time.Now().Add(-3 * time.Second), Data: map[string]interface{}{"node": "analyze"}},
		{ID: "ev-2", Type: EventNodeSucceeded, TenantID: "tenant-a", Source: "wf/1", Time: time.Now().Add(-2 * time.Second), Data: map[string]interface{}{"node": "analyze"}},
		{ID: "ev-3", Type: EventNodeSucceeded, TenantID: "tenant-b", Source: "wf/2", Time: time.Now().Add(-1 * time.Second), Data: map[string]interface{}{"node": "review"}},
	}
	for _, e := range pub {
		b, _ := json.Marshal(e)
		if _, err := js.Publish("test."+e.TenantID+".events."+e.Source, b); err != nil {
			t.Fatalf("publish %s: %v", e.ID, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 回放全部
	req := ReplayRequest{Stream: stream, Subject: "test.>"}
	evts, err := ReplayJetStream(ctx, js, req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(evts) != 3 {
		t.Fatalf("replayed %d events, want 3 (all history)", len(evts))
	}

	// 按 tenant 过滤
	reqT := ReplayRequest{Stream: stream, Subject: "test.tenant-a.events.>"}
	evtsA, err := ReplayJetStream(ctx, js, reqT)
	if err != nil {
		t.Fatalf("replay tenant-a: %v", err)
	}
	if len(evtsA) != 2 {
		t.Fatalf("tenant-a replayed %d, want 2", len(evtsA))
	}

	// Limit
	reqL := ReplayRequest{Stream: stream, Subject: "test.>", Limit: 1}
	evtsL, err := ReplayJetStream(ctx, js, reqL)
	if err != nil {
		t.Fatalf("replay limit: %v", err)
	}
	if len(evtsL) != 1 {
		t.Fatalf("limited replay got %d, want 1", len(evtsL))
	}

	// 清理测试 stream
	_ = js.DeleteStream(stream)
}
