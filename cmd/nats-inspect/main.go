// NATS 事件订阅检查工具（诊断用）：订阅指定 subject，打印收到的消息。
package main

import (
	"flag"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

func main() {
	var url, subject string
	var dur time.Duration
	flag.StringVar(&url, "url", "nats://127.0.0.1:4222", "NATS URL")
	flag.StringVar(&subject, "subject", "agent-runtime.>", "Subject to subscribe")
	flag.DurationVar(&dur, "duration", 15*time.Second, "Listen duration")
	flag.Parse()

	nc, err := nats.Connect(url)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer nc.Close()

	count := 0
	sub, err := nc.Subscribe(subject, func(m *nats.Msg) {
		count++
		log.Printf("RECEIVED subject=%s len=%d data=%s", m.Subject, len(m.Data), string(m.Data))
	})
	if err != nil {
		log.Fatalf("subscribe: %v", err)
	}
	log.Printf("listening on %q for %v...", subject, dur)
	time.Sleep(dur)
	_ = sub
	log.Printf("done, received %d messages", count)
}
