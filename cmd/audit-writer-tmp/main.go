// 临时验证工具：用项目 audit.NatsStore 写入一条 DLP 审计记录到 JetStream，
// 用于验证 API Server /api/v1/audit 查询链路。
package main

import (
	"context"
	"flag"
	"log"

	"github.com/example/agent-runtime-operator/internal/audit"
)

func main() {
	var url string
	flag.StringVar(&url, "url", "nats://127.0.0.1:4222", "NATS URL")
	flag.Parse()

	store, err := audit.NewNatsStore(audit.NatsConfig{URL: url})
	if err != nil {
		log.Fatalf("nats store: %v", err)
	}
	defer store.Close()

	rec := &audit.Record{
		ID:       "test-1",
		TenantID: "tenant-api",
		AgentID:  "reviewer",
		Action:   audit.ActionToolCall,
		Resource: "db.query",
		Success:  true,
	}
	if err := store.Write(context.Background(), rec); err != nil {
		log.Fatalf("write audit: %v", err)
	}
	log.Printf("audit record written: tenant=%s agent=%s action=%s resource=%s", rec.TenantID, rec.AgentID, rec.Action, rec.Resource)
}
