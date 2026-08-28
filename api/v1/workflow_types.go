package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ===================== Workflow =====================

// WorkflowSpec 编排 DSL 定义
type WorkflowSpec struct {
	Entrypoint string         `json:"entrypoint"`
	Events     []EventTrigger `json:"events,omitempty"` // 触发工作流的外部事件订阅
	Nodes      []WorkflowNode `json:"nodes"`
}

type EventTrigger struct {
	On   string `json:"on"`   // 事件类型，如 "repo.commit.pushed"
	From string `json:"from"` // 来源，可选；跨租户来源需 FederationPolicy（C3）
}

// WorkflowNode 编排节点
type WorkflowNode struct {
	ID        string     `json:"id"`
	Agent     string     `json:"agent"`     // 执行 Agent
	Action    string     `json:"action"`    // Agent 动作
	DependsOn []string   `json:"dependsOn,omitempty"`
	Retry     *RetrySpec `json:"retry,omitempty"`
	Condition string     `json:"condition,omitempty"` // CEL 表达式(R-2)
	Always    bool       `json:"always,omitempty"`    // 无论成败都执行(补偿)
	Timeout   string     `json:"timeout,omitempty"`   // 节点超时，如 "5m"
	Kind      string     `json:"kind,omitempty"`      // 默认 task，支持 approval(人工审批)
}

type RetrySpec struct {
	Max      int    `json:"max"`
	Backoff  string `json:"backoff"` // none | fixed | exponential（C1）
	Interval string `json:"interval,omitempty"` // Go duration 语义，如 "5s"（仅 fixed 生效）
}

// Workflow 是编排 DSL 资源
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
type Workflow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec WorkflowSpec `json:"spec,omitempty"`
}

// WorkflowList 包含 Workflow 列表
// +kubebuilder:object:root=true
type WorkflowList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workflow `json:"items"`
}

// ===================== WorkflowRun =====================

// WorkflowRunSpec 一次执行实例
type WorkflowRunSpec struct {
	WorkflowRef string                 `json:"workflowRef"`
	Input       map[string]interface{} `json:"input,omitempty"`
}

// WorkflowRunStatus 执行实例状态
// 注意：CRD status 为只读低频快照视图(R-5)，实际以 Temporal 为事实状态源。
type WorkflowRunStatus struct {
	RunID       string                 `json:"runId"` // 唯一执行标识，幂等键组成部分(N-3)
	Phase       string                 `json:"phase"` // RUNNING/SUCCEEDED/FAILED/CANCELLED（P2-3）
	CurrentNode string                 `json:"currentNode,omitempty"`
	NodeResults map[string]interface{} `json:"nodeResults,omitempty"`
	Error       string                 `json:"error,omitempty"`
	EventsCount int64                  `json:"eventsCount,omitempty"`
}

// WorkflowRun 是一次执行实例资源
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=wfr,scope=Namespaced
type WorkflowRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkflowRunSpec   `json:"spec,omitempty"`
	Status WorkflowRunStatus `json:"status,omitempty"`
}

// WorkflowRunList 包含 WorkflowRun 列表
// +kubebuilder:object:root=true
type WorkflowRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkflowRun `json:"items"`
}

// ===================== ToolBinding（R-4 职责收敛） =====================

// ToolBindingSpec Agent ↔ 工具权限绑定，是工具权限的唯一来源（R-4）
type ToolBindingSpec struct {
	// AgentRefs 引用本绑定适用的 Agent 对象名集合（为空表示租户内全部）
	AgentRefs []string    `json:"agentRefs,omitempty"`
	// Tools 授权的工具名集合（对应 MCPEndpoint 对象名上暴露的工具，即 ToolGrant.Name）
	Tools []ToolGrant `json:"tools"`
}

// ToolGrant 单个工具授权及数据范围
type ToolGrant struct {
	// Name 工具名，对应 MCPEndpoint 暴露的工具
	Name string `json:"name"`
	// DataScope 数据范围过滤条件（数据级 ABAC，P1-4）
	DataScope map[string]interface{} `json:"dataScope,omitempty"`
	// RateLimit 调用配额
	RateLimit RateLimit `json:"rateLimit,omitempty"`
	// Redact 需脱敏的返回字段
	Redact []string `json:"redact,omitempty"`
}

// ToolBinding 是工具权限绑定资源
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
type ToolBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ToolBindingSpec `json:"spec,omitempty"`
}

// ToolBindingList 包含 ToolBinding 列表
// +kubebuilder:object:root=true
type ToolBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ToolBinding `json:"items"`
}

// ===================== MCPEndpoint（R-4 职责收敛） =====================

// MCPEndpointSpec 工具 Server 连接信息（只管"工具在哪"，不管权限，R-4）
type MCPEndpointSpec struct {
	// Address 端点地址，如 "mcp-db:50051"
	Address string `json:"address"`
	// Transport stdio | streamable-http
	Transport string `json:"transport"`
	// Auth 连接鉴权方式（凭证存于 Secret/Vault，不落 CRD 明文）
	Auth AuthRef `json:"auth,omitempty"`
	// WhitelistDomains 该端点允许出网的域名白名单
	WhitelistDomains []string `json:"whitelistDomains,omitempty"`
}

type AuthRef struct {
	// Type 鉴权方式，如 none / bearer / mtls
	Type string `json:"type"`
	// SecretRef 凭证引用（仅存 Secret 名，不存明文）
	SecretRef string `json:"secretRef,omitempty"`
}

type RateLimit struct {
	RPS     int `json:"rps,omitempty"`
	Burst   int `json:"burst,omitempty"`
	Monthly int `json:"monthly,omitempty"`
}

// MCPEndpoint 是工具 Server 连接信息资源
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
type MCPEndpoint struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec MCPEndpointSpec `json:"spec,omitempty"`
}

// MCPEndpointList 包含 MCPEndpoint 列表
// +kubebuilder:object:root=true
type MCPEndpointList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MCPEndpoint `json:"items"`
}
