package eventbus

import (
	"context"
	"testing"
	"time"
)

// 集成测试依赖本机 NATS 服务（docker run -d --name nats-test -p 4222:4222 -js nats:2.10）
// 若 NATS 未启动则跳过。
func testBus(t *testing.T, enableJS bool) *NatsBus {
	t.Helper()
	bus, err := NewNatsBus(NatsConfig{
		URL:             "nats://127.0.0.1:4222",
		SubjectPrefix:   "test",
		EnableJetStream: enableJS,
		JetStreamStream: "test-stream",
	})
	if err != nil {
		t.Skipf("NATS not available, skipping: %v", err)
	}
	t.Cleanup(bus.Close)
	return bus
}

func TestNatsBus_PublishSubscribe(t *testing.T) {
	bus := testBus(t, false)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	received := make(chan *CloudEvent, 1)
	sub, err := bus.Subscribe(ctx, "tenant-a", "workflow/run-1", func(ctx context.Context, evt *CloudEvent) error {
		received <- evt
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	// 等待订阅生效
	time.Sleep(500 * time.Millisecond)

	evt := &CloudEvent{
		ID:       "evt-1",
		Type:     EventNodeStarted,
		TenantID: "tenant-a",
		Source:   "workflow/run-1",
		Data:     map[string]interface{}{"node": "review"},
	}
	if err := bus.Publish(ctx, "workflow/run-1", evt); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case got := <-received:
		if got.ID != evt.ID || got.Type != EventNodeStarted || got.TenantID != "tenant-a" {
			t.Fatalf("event mismatch: %+v", got)
		}
		if got.Data["node"] != "review" {
			t.Fatalf("data mismatch: %+v", got.Data)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for event")
	}
}

func TestNatsBus_TenantIsolation(t *testing.T) {
	bus := testBus(t, false)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	received := make(chan *CloudEvent, 1)
	// 订阅 tenant-a，但发布到 tenant-b（不同租户 subject 前缀，不应收到）
	sub, err := bus.Subscribe(ctx, "tenant-a", "events", func(ctx context.Context, evt *CloudEvent) error {
		received <- evt
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	time.Sleep(500 * time.Millisecond)

	// 发布到 tenant-b
	evt := &CloudEvent{ID: "evt-x", Type: EventNodeFailed, TenantID: "tenant-b"}
	if err := bus.Publish(ctx, "events", evt); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-received:
		t.Fatal("should not receive cross-tenant event")
	case <-time.After(1 * time.Second):
		// 正确：tenant-a 订阅未收到 tenant-b 事件
	}
}

func TestNatsBus_JetStream(t *testing.T) {
	bus := testBus(t, true)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	received := make(chan *CloudEvent, 1)
	sub, err := bus.Subscribe(ctx, "tenant-a", "critical", func(ctx context.Context, evt *CloudEvent) error {
		received <- evt
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	time.Sleep(1 * time.Second)

	evt := &CloudEvent{ID: "evt-js-1", Type: EventNodeSucceeded, TenantID: "tenant-a", Data: map[string]interface{}{"n": 1}}
	if err := bus.Publish(ctx, "critical", evt); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case got := <-received:
		if got.ID != "evt-js-1" {
			t.Fatalf("jetstream event mismatch: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for jetstream event")
	}
}
