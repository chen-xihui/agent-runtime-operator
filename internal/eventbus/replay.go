// replay.go 事件回放（design-doc 8）：基于 JetStream 历史，按租户/topic 回放已发布事件，
// 用于问题排查与流程复现。与 nats-inspect（实时抓包）不同，此工具读 JetStream 持久化历史
// （DeliverAllPolicy），非实时订阅。
package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// JetStreamReplay 回放输入
type ReplayRequest struct {
	// Stream JetStream stream 名，默认 agent-events
	Stream string
	// Subject 过滤（如 agent-runtime.tenant-a.events.> 或 agent-runtime.>）
	Subject string
	// Since 仅回放该时间之后的事件（nil 表示全部）
	Since *time.Time
	// Limit 返回上限（0 表示不限）
	Limit int
}

// ReplayedEvent 单条回放事件
type ReplayedEvent struct {
	Sequence  uint64          `json:"sequence"`
	Subject   string          `json:"subject"`
	Published time.Time       `json:"published"`
	Event     *CloudEvent     `json:"event"`
}

// replayPuller 抽象 JetStream 拉取（便于测试注入 mock）；签名与 nats.JetStreamContext.PullSubscribe 一致
type replayPuller interface {
	PullSubscribe(subj, durable string, opts ...nats.SubOpt) (*nats.Subscription, error)
}

// ReplayJetStream 读取 JetStream 持久化的历史事件并按序回放。
// js 为已连接的 nats.JetStreamContext。
func ReplayJetStream(ctx context.Context, js replayPuller, req ReplayRequest) ([]ReplayedEvent, error) {
	stream := req.Stream
	if stream == "" {
		stream = "agent-events"
	}
	subject := req.Subject
	if subject == "" {
		subject = "agent-runtime.>"
	}

	// 构建拉取订阅选项：从最早（DeliverAll）拉历史
	sub, err := js.PullSubscribe(subject, "", nats.ManualAck(), nats.DeliverAll(), nats.BindStream(stream))
	if err != nil {
		return nil, fmt.Errorf("pull subscribe %q on %s: %w", subject, stream, err)
	}
	defer func() { _ = sub.Drain() }()

	var out []ReplayedEvent
	pending := int(req.Limit)
	for {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}
		msgs, err := sub.Fetch(10, nats.MaxWait(2*time.Second))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				break // 无更多历史
			}
			return out, fmt.Errorf("fetch: %w", err)
		}
		if len(msgs) == 0 {
			break
		}
		for _, m := range msgs {
			_ = m.Ack()
			ev := &CloudEvent{}
			if err := json.Unmarshal(m.Data, ev); err != nil {
				return out, fmt.Errorf("decode event on %s: %w", m.Subject, err)
			}
			// Since 过滤
			if req.Since != nil && ev.Time.Before(*req.Since) {
				continue
			}
			re := ReplayedEvent{Subject: m.Subject, Event: ev}
			if meta, merr := m.Metadata(); merr == nil {
				re.Sequence = meta.Sequence.Stream
				re.Published = meta.Timestamp
			}
			out = append(out, re)
			if pending > 0 && len(out) >= pending {
				return out, nil
			}
		}
	}
	if out == nil {
		out = []ReplayedEvent{}
	}
	return out, nil
}
