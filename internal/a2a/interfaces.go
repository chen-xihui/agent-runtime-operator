// Package a2a 提供 A2A（Agent-to-Agent）互操作接口。
// 见 design-doc 4.3.2 / core-interface 3.2。
//
// 通道分工（D-2）：编排引擎 → Agent 走事件总线；Agent 之间协作走 A2A。
// 跨租户边界（D-4）：默认禁止跨租户 A2A，仅在有 FederationPolicy 双向信任后允许。
package a2a

import "context"

// AgentCard A2A 协议的能力描述
type AgentCard struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	URL         string         `json:"url"`
	Version     string         `json:"version"`
	Skills      []AgentSkill   `json:"skills"`
	Auth        map[string]any `json:"auth,omitempty"`
}

type AgentSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
}

// Gateway A2A 注册中心与消息路由
type Gateway interface {
	// Register 注册 Agent 的能力卡
	Register(ctx context.Context, agentID string, card *AgentCard) error
	// Discover 按技能/关键词发现可用 Agent
	Discover(ctx context.Context, query string, skills []string) ([]*AgentCard, error)
	// SendTask 委派任务给目标 Agent
	SendTask(ctx context.Context, from, to string, task *TaskMessage) (*TaskResult, error)
	// Route 消息路由（含跨集群代理）
	Route(ctx context.Context, msg *Message) error
}

type TaskMessage struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"` // "task/send" / "message/send"
	Input       map[string]any `json:"input"`
	RequireReply bool          `json:"requireReply"`
	TraceParent string         `json:"traceparent,omitempty"`
}

type TaskResult struct {
	TaskID    string     `json:"taskId"`
	State     string     `json:"state"` // completed / failed / in-progress
	Artifacts []Artifact `json:"artifacts,omitempty"`
	Message   string     `json:"message,omitempty"`
}

type Artifact struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // file / code / text / data
	URI     string `json:"uri,omitempty"`
	Content string `json:"content,omitempty"`
}

// Message A2A 消息
type Message struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Type      string `json:"type"`
	Payload   map[string]any `json:"payload"`
	TraceParent string `json:"traceparent,omitempty"`
}
