package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// NatsStore 基于 NATS JetStream 的审计存储（持久化，DLP 审计收集到外部存储）
// 审计记录写入 JetStream stream，供合规审计长期保存与查询。
type NatsStore struct {
	nc *nats.Conn
	js nats.JetStreamContext
	// streamName JetStream stream 名
	streamName string
}

// NatsConfig NATS 审计存储配置
type NatsConfig struct {
	URL      string
	User     string
	Password string
	// StreamName JetStream stream 名（默认 audit-events）
	StreamName string
}

// NewNatsStore 创建 NATS JetStream 审计存储
func NewNatsStore(cfg NatsConfig) (*NatsStore, error) {
	opts := []nats.Option{nats.Name("agent-runtime-audit")}
	if cfg.User != "" {
		opts = append(opts, nats.UserInfo(cfg.User, cfg.Password))
	}
	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("audit: connect nats: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("audit: jetstream: %w", err)
	}

	stream := cfg.StreamName
	if stream == "" {
		stream = "audit-events"
	}
	// 创建持久化 stream（幂等）
	_, _ = js.AddStream(&nats.StreamConfig{
		Name:     stream,
		Subjects: []string{"audit.>"},
		Storage:  nats.FileStorage,
	})

	return &NatsStore{nc: nc, js: js, streamName: stream}, nil
}

// Write 写入审计记录到 JetStream（subject: audit.<tenant>.<action>）
func (s *NatsStore) Write(ctx context.Context, r *Record) error {
	if r == nil {
		return fmt.Errorf("audit: nil record")
	}
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now()
	}
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("audit: marshal: %w", err)
	}
	subject := "audit." + r.TenantID + "." + r.Action
	if _, err := s.js.Publish(subject, data); err != nil {
		return fmt.Errorf("audit: publish: %w", err)
	}
	return nil
}

// Query 查询审计记录（从 JetStream 拉取并过滤）
// 简化实现：扫描 stream 中所有消息进行过滤（生产可用 KV/索引）。
func (s *NatsStore) Query(ctx context.Context, f Filter) ([]*Record, error) {
	sub, err := s.js.SubscribeSync("audit.>", nats.BindStream(s.streamName))
	if err != nil {
		return nil, fmt.Errorf("audit: subscribe: %w", err)
	}
	defer sub.Unsubscribe()

	var out []*Record
	for {
		msg, err := sub.NextMsg(200 * time.Millisecond)
		if err != nil {
			break // 无更多消息
		}
		var r Record
		if err := json.Unmarshal(msg.Data, &r); err != nil {
			continue
		}
		if f.TenantID != "" && r.TenantID != f.TenantID {
			continue
		}
		if f.AgentID != "" && r.AgentID != f.AgentID {
			continue
		}
		if f.Action != "" && r.Action != f.Action {
			continue
		}
		if f.Resource != "" && r.Resource != f.Resource {
			continue
		}
		out = append(out, &r)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

// Close 关闭连接
func (s *NatsStore) Close() {
	s.nc.Close()
}

var _ Store = (*NatsStore)(nil)
