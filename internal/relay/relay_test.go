package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/agent-runtime-operator/internal/eventbus"
)

// waitForRelayConnections 等待 relay 识别到指定数量的连接（避免竞态）
func waitForRelayConnections(t *testing.T, r *Relay, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for r.connectionCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d relay connections, got %d", n, r.connectionCount())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRelayDeliverAndReceive(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "agent.sock")

	r := New(&Config{
		LocalSocket: sock,
		TenantID:    "tenant-a",
		AgentID:     "code-reviewer",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyCh, err := r.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-readyCh

	// 确保 socket 文件已创建
	time.Sleep(50 * time.Millisecond)

	// Agent 连接 socket
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial socket: %v", err)
	}
	defer conn.Close()

	// 等待 relay 的 acceptLoop 识别该连接（避免事件投递到空连接集合丢失）
	waitForRelayConnections(t, r, 1)

	// 服务端投递事件，Agent 应收到
	deliverEvt := &eventbus.CloudEvent{
		ID:       "evt-1",
		Type:     eventbus.EventNodeStarted,
		TenantID: "tenant-a",
		Source:   "workflow/run-1",
	}
	if err := r.DeliverToAgent(ctx, deliverEvt); err != nil {
		t.Fatalf("DeliverToAgent: %v", err)
	}

	// 读取并校验投递事件
	reader := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read delivered event: %v", err)
	}
	var got eventbus.CloudEvent
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != deliverEvt.ID || got.Type != eventbus.EventNodeStarted {
		t.Fatalf("delivered event mismatch: got %+v", got)
	}

	// Agent 上报事件，服务端回调应收到
	reported := &eventbus.CloudEvent{
		ID:       "evt-2",
		Type:     eventbus.EventNodeSucceeded,
		TenantID: "tenant-a",
		Source:   "agent/code-reviewer",
	}
	received := make(chan *eventbus.CloudEvent, 1)
	r.OnEvent(func(ctx context.Context, evt *eventbus.CloudEvent) error {
		received <- evt
		return nil
	})

	data, _ := json.Marshal(reported)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write reported event: %v", err)
	}

	select {
	case evt := <-received:
		if evt.ID != reported.ID || evt.Type != eventbus.EventNodeSucceeded {
			t.Fatalf("reported event mismatch: got %+v", evt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for reported event")
	}
}
