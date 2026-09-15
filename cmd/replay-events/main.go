// 事件回放工具（design-doc 8）：基于 JetStream 持久化历史，按租户/topic 回放已发布事件，
// 用于问题排查与流程复现。与 nats-inspect（实时抓包）不同，此工具读历史（DeliverAll），非实时。
//
// 用法：
//   replay-events --url=nats://127.0.0.1:4222 --tenant=tenant-a
//   replay-events --subject='agent-runtime.>' --limit=20 --json
//   replay-events --since='2026-09-01T00:00:00Z'
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/chen-xihui/agent-runtime-operator/internal/eventbus"
	"github.com/nats-io/nats.go"
)

func main() {
	var url, tenant, subject, stream, since string
	var limit int
	var jsonOut bool
	flag.StringVar(&url, "url", "nats://127.0.0.1:4222", "NATS URL")
	flag.StringVar(&tenant, "tenant", "", "tenant id to filter (subject=agent-runtime.<tenant>.events.>)")
	flag.StringVar(&subject, "subject", "", "explicit subject filter (overrides --tenant)")
	flag.StringVar(&stream, "stream", "agent-events", "JetStream stream name")
	flag.StringVar(&since, "since", "", "only events after RFC3339 time")
	flag.IntVar(&limit, "limit", 0, "max events to replay (0=all)")
	flag.BoolVar(&jsonOut, "json", false, "output as JSON lines")
	flag.Parse()

	nc, err := nats.Connect(url)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		log.Fatalf("jetstream: %v", err)
	}

	req := eventbus.ReplayRequest{Stream: stream, Subject: subject, Limit: limit}
	if req.Subject == "" && tenant != "" {
		// 单租户回放：仅该租户事件
		req.Subject = eventbus.ValidateTenantScope("agent-runtime", tenant)
	}
	if req.Subject == "" {
		req.Subject = "agent-runtime.>"
	}
	if since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			log.Fatalf("bad --since: %v", err)
		}
		req.Since = &t
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	evts, err := eventbus.ReplayJetStream(ctx, js, req)
	if err != nil {
		log.Fatalf("replay: %v", err)
	}

	log.Printf("replayed %d event(s) from stream %q subject %q", len(evts), stream, req.Subject)
	for _, e := range evts {
		if jsonOut {
			b, _ := json.Marshal(e)
			fmt.Println(string(b))
			continue
		}
		desc := e.Event.Type
		if e.Event.Subject != "" {
			desc += " subj=" + e.Event.Subject
		}
		fmt.Printf("[%s] seq=%d %s\n", e.Published.Format(time.RFC3339), e.Sequence, desc)
		if data, ok := e.Event.Data["node"].(string); ok {
			fmt.Printf("         node=%s\n", data)
		}
	}
	fmt.Printf("\n[summary] %d events; scopes: %s\n", len(evts), strings.Join(scopeSummary(evts), ", "))
}

// scopeSummary 汇总回放事件来源（租户/节点），便于快速定位
func scopeSummary(evts []eventbus.ReplayedEvent) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range evts {
		key := e.Subject
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	return out
}
