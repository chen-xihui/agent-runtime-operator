// Temporal 编排 Worker 主入口。
// 运行 GenericOrchestratorWorkflow 及编排 Activity（DispatchNodeActivity / RequestApprovalActivity）。
// 独立进程，便于与 operator 分离部署（ADR-02：编排执行与调谐解耦）。
package main

import (
	"context"
	"flag"
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/example/agent-runtime-operator/internal/eventbus"
	"github.com/example/agent-runtime-operator/internal/orchestrator"
)

func main() {
	var temporalAddr, taskQueue, natsURL string
	flag.StringVar(&temporalAddr, "temporal-address", "127.0.0.1:7233", "Temporal server address.")
	flag.StringVar(&taskQueue, "task-queue", "agent-orchestration", "Temporal task queue.")
	flag.StringVar(&natsURL, "nats-url", "", "NATS server URL (emit node events).")
	flag.Parse()

	tClient, err := client.Dial(client.Options{HostPort: temporalAddr})
	if err != nil {
		log.Fatalf("dial temporal %s: %v", temporalAddr, err)
	}

	// 配置默认节点派发器（DispatchNodeActivity 依赖）
	dispatcher := &orchestrator.NodeDispatcher{}

	// EventSink：节点启动事件发到 NATS（operator 更新 WorkflowRun status）
	if natsURL != "" {
		bus, err := eventbus.NewNatsBus(eventbus.NatsConfig{
			URL:           natsURL,
			SubjectPrefix: "agent-runtime",
		})
		if err != nil {
			log.Fatalf("connect nats %s: %v", natsURL, err)
		}
		defer bus.Close()
		dispatcher.EventSink = func(ctx context.Context, in orchestrator.DispatchInput, evtType string) error {
			// NATS subject 不允许 '/'，用点分隔；runID 经 Data 传递
			return bus.Publish(ctx, "workflow."+in.NodeID, &eventbus.CloudEvent{
				ID:       "evt-" + in.RunID + "-" + in.NodeID + "-" + evtType,
				Type:     evtType,
				TenantID: in.TenantID,
				Source:   "workflow." + in.RunID,
				Data: map[string]interface{}{
					"node":  in.NodeID,
					"runID": in.RunID,
				},
			})
		}
	}

	// Dispatch：模拟 Agent 执行成功，向 Workflow 发节点结果 Signal 推进 DAG
	dispatcher.Dispatch = func(ctx context.Context, in orchestrator.DispatchInput) error {
		log.Printf("dispatching node %s (agent=%s action=%s)", in.NodeID, in.Agent, in.Action)
		// 通过 Temporal Signal 向 Workflow 发 NODE_SUCCEEDED（推进到下一节点）
		return tClient.SignalWorkflow(context.Background(), in.RunID, "",
			orchestrator.NodeResultSignalName, orchestrator.NodeResult{
				NodeID: in.NodeID,
				State:  "SUCCEEDED",
			})
	}
	orchestrator.SetDefaultNodeDispatcher(dispatcher)

	w := worker.New(tClient, taskQueue, worker.Options{})
	orchWorker := orchestrator.NewWorker(w)

	log.Printf("starting orchestration worker (temporal=%s queue=%s nats=%s)", temporalAddr, taskQueue, natsURL)
	if err := orchWorker.StartAsync(); err != nil {
		log.Fatalf("worker failed to start: %v", err)
	}
	log.Printf("worker started, polling for tasks")

	// 阻塞主 goroutine，保持 worker 持续运行（避免进程退出）
	select {}
}
