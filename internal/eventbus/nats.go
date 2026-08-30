package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/example/agent-runtime-operator/internal/telemetry"
)

// NatsConfig NATS 连接配置
type NatsConfig struct {
	URL      string // e.g. nats://127.0.0.1:4222
	User     string
	Password string
	// SubjectPrefix 租户隔离 subject 前缀（R-3），如 "agent-runtime"
	SubjectPrefix string
	// EnableJetStream 是否启用 JetStream 持久化（关键控制事件）
	EnableJetStream bool
	// JetStreamStream JetStream stream 名
	JetStreamStream string
}

// NatsBus 基于 NATS JetStream 的事件总线实现（ADR-03）
// 租户隔离（R-3）：各租户 Relay 使用独立 user/token + subject 级 ACL，
// 只能发布/订阅本租户前缀 subject（如 <prefix>.<tenant>.>）。
type NatsBus struct {
	conn      *nats.Conn
	js        nats.JetStreamContext
	cfg       NatsConfig
	handlers  map[string]func(ctx context.Context, evt *CloudEvent) error
	subs      []*nats.Subscription
	closeCh   chan struct{}
}

// NewNatsBus 创建并连接 NATS
func NewNatsBus(cfg NatsConfig) (*NatsBus, error) {
	opts := []nats.Option{
		nats.Name("agent-runtime-eventbus"),
		nats.ReconnectWait(2 * time.Second),
		nats.MaxReconnects(-1),
	}
	if cfg.User != "" {
		opts = append(opts, nats.UserInfo(cfg.User, cfg.Password))
	}
	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}

	b := &NatsBus{
		conn:     nc,
		cfg:      cfg,
		handlers: make(map[string]func(ctx context.Context, evt *CloudEvent) error),
		closeCh:  make(chan struct{}),
	}

	if cfg.EnableJetStream {
		js, err := nc.JetStream()
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("jetstream: %w", err)
		}
		b.js = js
		stream := cfg.JetStreamStream
		if stream == "" {
			stream = "agent-events"
		}
		// 创建持久化 stream（幂等）
		_, _ = js.AddStream(&nats.StreamConfig{
			Name:     stream,
			Subjects: []string{b.prefix() + ".>"},
			Storage:  nats.FileStorage,
		})
	}
	return b, nil
}

func (b *NatsBus) prefix() string {
	if b.cfg.SubjectPrefix == "" {
		return "agent-runtime"
	}
	return b.cfg.SubjectPrefix
}

// tenantTopic 生成租户隔离的 subject
func (b *NatsBus) tenantTopic(tenantID, topic string) string {
	return fmt.Sprintf("%s.%s.events.%s", b.prefix(), tenantID, topic)
}

// Publish 发布事件到指定 topic
func (b *NatsBus) Publish(ctx context.Context, topic string, evt *CloudEvent) error {
	if evt.TenantID == "" {
		return fmt.Errorf("eventbus: tenantId required")
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	subject := b.tenantTopic(evt.TenantID, topic)
	if b.js != nil {
		_, err = b.js.Publish(subject, data)
		return err
	}
	return b.conn.Publish(subject, data)
}

// PublishToAgent 向特定 Agent 沙箱投递事件
func (b *NatsBus) PublishToAgent(ctx context.Context, agentID string, evt *CloudEvent) error {
	topic := "agent." + agentID
	return b.Publish(ctx, topic, evt)
}

// Subscribe 订阅事件（租户隔离 topic）
func (b *NatsBus) Subscribe(ctx context.Context, tenantID, topic string, h Handler) (Subscription, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("eventbus: tenantId required for scoped subscribe")
	}
	return b.subscribe(ctx, b.tenantTopic(tenantID, topic), h)
}

// SubscribeAll 订阅所有租户的事件（subject: <prefix>.>，供 operator 全局监听节点事件）
func (b *NatsBus) SubscribeAll(ctx context.Context, h Handler) (Subscription, error) {
	return b.subscribe(ctx, b.prefix()+".>", h)
}

// subscribe 底层订阅实现（复用 handler 逻辑）
func (b *NatsBus) subscribe(ctx context.Context, subject string, h Handler) (Subscription, error) {
	handler := func(msg *nats.Msg) {
		evt := &CloudEvent{}
		if err := json.Unmarshal(msg.Data, evt); err != nil {
			return
		}
		// 全链路追踪（M5）：无 traceparent 则生成，保证链路连续
		if evt.TraceParent == "" {
			evt.TraceParent = telemetry.NewTraceContext().Traceparent()
		}
		if err := h(ctx, evt); err != nil {
			// 处理失败，NATS 自动重投
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	}

	var sub *nats.Subscription
	var err error
	if b.js != nil {
		sub, err = b.js.Subscribe(subject, handler)
	} else {
		sub, err = b.conn.Subscribe(subject, handler)
	}
	if err != nil {
		return nil, err
	}
	return &natsSubscription{sub: sub, natsBus: b}, nil
}

type natsSubscription struct {
	sub     *nats.Subscription
	natsBus *NatsBus
}

func (s *natsSubscription) Unsubscribe() error {
	if err := s.sub.Unsubscribe(); err != nil {
		return err
	}
	return s.sub.Drain()
}

// Close 关闭连接
func (b *NatsBus) Close() {
	close(b.closeCh)
	b.conn.Close()
}

var _ Bus = (*NatsBus)(nil)
