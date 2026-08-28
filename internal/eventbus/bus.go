// Package eventbus 提供事件总线抽象（NATS JetStream 实现），承载编排事件与 Agent 生命周期事件。
// 见 design-doc 3.2 / core-interface 2.2。
package eventbus

import (
	"context"
	"time"
)

// CloudEvent 遵循 CNCF CloudEvents 规范
type CloudEvent struct {
	ID          string                 `json:"id"`
	Source      string                 `json:"source"`
	Type        string                 `json:"type"` // NODE_STARTED / NODE_SUCCEEDED / ...
	Subject     string                 `json:"subject,omitempty"`
	Time        time.Time              `json:"time"`
	Data        map[string]interface{} `json:"data"`
	TraceParent string                 `json:"traceparent,omitempty"`
	TenantID    string                 `json:"tenantId"`
	SchemaURL   string                 `json:"dataschema,omitempty"`
}

// Bus 事件总线抽象（NATS JetStream 实现，ADR-03）
type Bus interface {
	Publish(ctx context.Context, topic string, evt *CloudEvent) error
	Subscribe(ctx context.Context, tenantID, topic string, h Handler) (Subscription, error)
	// PublishToAgent 向特定 Agent 沙箱投递事件
	PublishToAgent(ctx context.Context, agentID string, evt *CloudEvent) error
}

type Handler func(ctx context.Context, evt *CloudEvent) error

type Subscription interface {
	Unsubscribe() error
}

// 关键 topic 约定: <tenant>/events/<agent|workflow>/<type>
const (
	EventNodeStarted   = "NODE_STARTED"
	EventNodeSucceeded = "NODE_SUCCEEDED"
	EventNodeFailed    = "NODE_FAILED"
	EventNodeSkipped   = "NODE_SKIPPED"
	EventAskHuman      = "AGENT_ASK_HUMAN"
)

const TopicPrefix = "<tenant>/events"
