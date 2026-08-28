// Event Relay Sidecar 主入口。
// 作为 Sandbox Pod 内的可信 Sidecar，提供本地 unix socket 服务，作为沙箱唯一安全出口（P0-2）。
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/example/agent-runtime-operator/internal/eventbus"
	"github.com/example/agent-runtime-operator/internal/relay"
)

func main() {
	var socket, tenantID, agentID, natsCreds string
	flag.StringVar(&socket, "socket", "/var/run/agent.sock", "本地 unix socket 路径")
	flag.StringVar(&tenantID, "tenant", "", "租户 ID")
	flag.StringVar(&agentID, "agent", "", "Agent ID")
	flag.StringVar(&natsCreds, "nats-creds", "", "NATS 凭证路径（仅 Relay 持有）")
	flag.Parse()

	if tenantID == "" {
		// 支持从环境变量读取（与 sandbox/pod.go 注入的环境变量一致）
		tenantID = os.Getenv("TENANT_ID")
		agentID = os.Getenv("AGENT_ID")
		if s := os.Getenv("LOCAL_SOCKET"); s != "" {
			socket = s
		}
	}

	cfg := &relay.Config{
		LocalSocket:   socket,
		TenantID:      tenantID,
		AgentID:       agentID,
		NATSCredsPath: natsCreds,
	}

	r := relay.New(cfg)

	// 注册事件处理回调：此处占位，M2 将转发到 NATS 事件总线
	r.OnEvent(func(ctx context.Context, evt *eventbus.CloudEvent) error {
		log.Printf("received event from agent: type=%s id=%s", evt.Type, evt.ID)
		return nil
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	readyCh, err := r.Start(ctx)
	if err != nil {
		log.Fatalf("relay start failed: %v", err)
	}
	<-readyCh
	log.Printf("event relay listening on %s (tenant=%s agent=%s)", socket, tenantID, agentID)

	<-ctx.Done()
	log.Println("event relay shutting down")
	_ = r.Close()
}
