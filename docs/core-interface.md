# agent-runtime-operator — 核心接口与实现补充

> 本文档为 `docs/design-doc.md` 的工程落地补充，提供关键 CRD 的 Go 类型定义、编排引擎与协议层的核心接口签名。

## 1. 核心 CRD Go 类型（api/v1）

### 1.1 AgentSpec

```go
package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SandboxRuntime string

const (
	RuntimeGVisor      SandboxRuntime = "gvisor"      // 用户态内核
	RuntimeFirecracker SandboxRuntime = "firecracker" // 微VM
	RuntimeKata        SandboxRuntime = "kata"        // 备选
)

// AgentSpec 定义 Agent 的期望状态
type AgentSpec struct {
	Image        string            `json:"image"`            // Agent 镜像
	Runtime      RuntimeSpec       `json:"runtime"`          // 沙箱运行时
	Entrypoint   []string          `json:"entrypoint,omitempty"`
	MCP          MCPSpec           `json:"mcp"`              // 工具接入配置
	A2A          A2ASpec           `json:"a2a,omitempty"`    // Agent 互操作配置
	Capabilities CapabilitiesSpec  `json:"capabilities,omitempty"`
	Security     SecuritySpec      `json:"security"`
	Labels       map[string]string `json:"labels,omitempty"`
}

type RuntimeSpec struct {
	Class     SandboxRuntime        `json:"class"`
	Resources corev1.ResourceRequirements `json:"resources"`
	// SnapshotEnabled 开启后支持 Firecracker 快照 Suspend/Resume
	SnapshotEnabled bool `json:"snapshotEnabled,omitempty"`
}

type MCPSpec struct {
	// AllowedTools 仅做引用(R-4)：指向 ToolBinding 中已授权的工具名，
	// 权限唯一来源是 ToolBinding，不在本处重复定义权限
	AllowedTools []string `json:"allowedTools"`
	// Endpoints 引用的 MCPEndpoint 名称列表（只管"工具在哪"，不管权限，R-4）
	Endpoints []string `json:"endpoints"`
	// WhitelistDomains 远程工具域名白名单
	WhitelistDomains []string `json:"whitelistDomains,omitempty"`
}

type A2ASpec struct {
	// Enabled 是否参与 A2A 互操作（A2A 能力声明的唯一来源，C2）
	// 修订(C2): 与 CapabilitiesSpec.Protocols 收敛——参与 A2A 以本字段为准，
	//           CapabilitiesSpec 仅作展示性汇总，不再重复承载授权语义。
	Enabled  bool     `json:"enabled"`
	// Tasks 可被外部委派的任务 ID 集合
	Tasks    []string `json:"tasks,omitempty"`
	// AgentCard A2A 能力描述
	AgentCard map[string]interface{} `json:"agentCard,omitempty"`
}

// CapabilitiesSpec 能力汇总（仅展示性，不承载授权语义；实际能力以各子 Spec 为准，C2）
type CapabilitiesSpec struct {
	Protocols []string `json:"protocols"` // 展示性汇总，如 ["mcp","a2a"]
}

type SecuritySpec struct {
	RunAsNonRoot      bool     `json:"runAsNonRoot"`
	ReadOnlyRootFS    bool     `json:"readOnlyRootFS,omitempty"`
	NetworkPolicy     string   `json:"networkPolicy"`     // 引用 NetPol 模板
	EnvSecrets        []string `json:"envSecrets,omitempty"` // vault 路径
	SeccompProfile    string   `json:"seccompProfile,omitempty"`
	MaxLifetimeMin    int      `json:"maxLifetimeMin,omitempty"` // 沙箱最大存活时长(自动回收)
}

// AgentStatus 实际状态
// 修订(P2-3): 统一使用 Phase，不再使用 State，避免语义重复
type AgentStatus struct {
	Phase            string        `json:"phase"` // Pending/Provisioning/Running/Suspended/Terminating
	SandboxRef       string        `json:"sandboxRef,omitempty"`
	MCPConnectedTools []string     `json:"mcpConnectedTools,omitempty"`
	LastHeartbeat    metav1.Time   `json:"lastHeartbeat,omitempty"`
}

// === R-4 职责收敛：ToolBinding 与 MCPEndpoint 类型（补充 C4） ===

// ToolBinding Agent ↔ 工具权限绑定（哪些 Agent 可用哪些工具 + 数据范围）
// 修订(R-4): 是工具权限的唯一来源；Agent.spec.mcp.allowedTools 仅引用此处已授权工具名，
//            不在 Agent CRD 内重复定义权限，避免三处配置不一致。
// 注：CRD 名称为 ToolBinding；本 Spec 为 kubebuilder Spec 拆分。对象名（metadata.name）即引用键。
type ToolBindingSpec struct {
	// AgentRefs 引用本绑定适用的 Agent 对象名集合（为空表示租户内全部）
	AgentRefs []string `json:"agentRefs,omitempty"`
	// Tools 授权的工具名集合（引用 MCPEndpoint 对象名上暴露的工具，即 ToolGrant.Name）
	Tools []ToolGrant `json:"tools"`
}

// ToolGrant 单个工具授权及数据范围
type ToolGrant struct {
	// Name 工具名，对应 MCPEndpoint 暴露的工具
	Name string `json:"name"`
	// DataScope 数据范围过滤条件（数据级 ABAC，P1-4），如 {tenant: "tenant-a"}
	// 调用时注入到工具请求，防止跨租户数据可达
	DataScope map[string]interface{} `json:"dataScope,omitempty"`
	// RateLimit 调用配额
	RateLimit RateLimit `json:"rateLimit,omitempty"`
	// Redact 需脱敏的返回字段
	Redact []string `json:"redact,omitempty"`
}

// MCPEndpoint 工具 Server 连接信息（只管"工具在哪"，不管权限，R-4）
// 注：CRD 名称为 MCPEndpoint；本 Spec 为 kubebuilder Spec 拆分。对象名即引用键。
type MCPEndpointSpec struct {
	// Name 即 metadata.name（MCPEndpoint 对象名），
	// 供 Agent.spec.mcp.endpoints 与 ToolBinding.tools[].name 引用，二者指向同一对象名
	Name string `json:"name"`
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
```

### 1.2 Workflow / WorkflowRun

```go
// WorkflowSpec 编排 DSL 定义
type WorkflowSpec struct {
	Entrypoint string         `json:"entrypoint"`
	Events     []EventTrigger `json:"events,omitempty"` // 触发工作流的外部事件订阅
	Nodes      []WorkflowNode `json:"nodes"`
}

type EventTrigger struct {
	On   string `json:"on"`   // 事件类型，如 "repo.commit.pushed"
	From string `json:"from"` // 来源，可选
	// 修订(C3): From 仅限本租户内部来源（同 tenant 下的事件源）；
	//           跨租户事件触发必须显式存在 FederationPolicy 双向信任，否则提交时拒绝（对齐 D-4）。
	//           实现上在 Parser/Validate 阶段校验 From 的租户归属。
}


// WorkflowNode 编排节点
type WorkflowNode struct {
	ID        string     `json:"id"`
	Agent     string     `json:"agent"`     // 执行 Agent
	Action    string     `json:"action"`    // Agent 动作
	DependsOn []string   `json:"dependsOn,omitempty"`
	Retry     *RetrySpec `json:"retry,omitempty"`
	Condition string     `json:"condition,omitempty"` // CEL 表达式(R-2)，如 'nodes.review.result.approved == false'
	Always    bool       `json:"always,omitempty"`    // 无论成败都执行(补偿)
	Timeout   string     `json:"timeout,omitempty"`   // 节点超时，如 "5m"
	Kind      string     `json:"kind,omitempty"`      // 默认 task，支持 approval(人工审批)
}

type RetrySpec struct {
	Max      int    `json:"max"`
	// Backoff 重试策略: none | fixed | exponential
	// 修订(C1): 默认 none；exponential 时忽略 Interval（Temporal 内部计算退避），
	//           fixed 时 Interval 必填。非法组合在 Parser/Validate 阶段拒绝。
	Backoff  string `json:"backoff"`
	Interval string `json:"interval,omitempty"` // Go duration 语义，如 "5s"（仅 fixed 生效）
}

// WorkflowRun 一次执行实例
type WorkflowRunSpec struct {
	WorkflowRef string `json:"workflowRef"`
	Input       map[string]interface{} `json:"input,omitempty"`
}

// WorkflowRunStatus 执行实例状态
// 修订(P2-3): 统一使用 Phase，不再使用 State，避免语义重复。
// 注意：CRD status 为只读低频快照视图(R-5)，实际以 Temporal 为事实状态源；
//       Phase 允许短暂落后于 Temporal（准实时）。
type WorkflowRunStatus struct {
	// RunID 唯一执行标识，作为幂等键组成部分(N-3)
	RunID       string `json:"runId"`
	// Phase 统一执行阶段: RUNNING/SUCCEEDED/FAILED/CANCELLED（与 Agent/Sandbox 的 phase 对齐，P2-3）
	Phase       string `json:"phase"`
	CurrentNode string `json:"currentNode,omitempty"`
	NodeResults map[string]interface{} `json:"nodeResults,omitempty"`
	Error       string `json:"error,omitempty"`
	EventsCount int64  `json:"eventsCount,omitempty"`
}
```

---

## 2. 编排引擎核心接口

### 2.1 DSL 解析与 DAG

```go
package orchestrator

// Graph 由 DSL 解析得到的可执行 DAG
type Graph struct {
	Nodes map[string]*Node `json:"nodes"`
	Edges map[string][]string `json:"edges"` // 依赖边: nodeID -> 依赖它的节点
}

type Node struct {
	ID        string
	Agent     string
	Action    string
	Retry     RetrySpec
	Condition string
	Always    bool
	Timeout   time.Duration
	Kind      string
}

// Parser 将 Workflow CR 解析为 Graph
type Parser interface {
	Parse(spec *v1.WorkflowSpec) (*Graph, error)
	Validate(g *Graph) error // 检测环、缺失依赖、无入口
}

// 修订(P0-1/D-1): 编排职责收敛。平台只做 DSL→执行数据，不自研调度器。
// 因 Temporal Workflow 为预注册 Go 代码、无法运行时编译任意 YAML，
// 采用"通用编排 Workflow + 数据驱动"：DSL 只产出 DAG 数据，由预注册的
// GenericOrchestratorWorkflow 按数据解释执行。
// Compiler 将 DSL/Graph 转换为执行数据
type Compiler interface {
	// Compile 将 Graph 转换为可供 GenericOrchestratorWorkflow 执行的 DAG 数据
	Compile(g *Graph) (*ExecutionData, error)
	// Validate 编译期校验(Temporal 侧也可二次校验)
	Validate(g *Graph) error
}

// ExecutionData 通用编排 Workflow 的输入数据（数据驱动）
type ExecutionData struct {
	Nodes map[string]*Node `json:"nodes"`
	Edges map[string][]string `json:"edges"`
	Retry RetrySpec        `json:"retry,omitempty"`
}

// DAGEngine 执行 DAG（底层委托 Temporal GenericOrchestratorWorkflow）
type DAGEngine interface {
	// Execute 以执行数据启动通用编排 Workflow，返回 runID
	Execute(data *ExecutionData, input map[string]interface{}) (runID string, err error)
	// Cancel 取消运行
	Cancel(runID string) error
	// OnEvent 处理事件总线投递的节点结果事件(幂等去重后推进)
	// 幂等键: evt.ID + runID + nodeID，避免 at-least-once 重复推进
	OnEvent(ctx context.Context, evt *CloudEvent) error
}

// ConditionEvaluator condition 表达式求值器(R-2)，采用 CEL，运行于确定性上下文
type ConditionEvaluator interface {
	// Compile 静态编译校验 CEL 表达式，非法时在提交阶段即报错
	Compile(expr string) error
	// Eval 求值: 绑定 nodes.<id>.result / input / env
	Eval(ctx context.Context, expr string, bindings map[string]any) (bool, error)
}

// 确定性约束(R-1): GenericOrchestratorWorkflow 内
//   - 事件等待只用 workflow.Await / Signal / Sleep；
//   - 禁止在 Workflow 内直接调 NATS I/O，I/O 全部收敛到 Activity。
```

### 2.2 事件驱动核心

```go
package eventbus

// CloudEvent 遵循 CNCF CloudEvents 规范
type CloudEvent struct {
	ID              string                 `json:"id"`
	Source          string                 `json:"source"`
	Type            string                 `json:"type"` // WORKFLOW_STARTED / NODE_SUCCEEDED / ...（见 EventNode* 常量，N-1）
	Subject         string                 `json:"subject,omitempty"`
	Time            time.Time              `json:"time"`
	Data            map[string]interface{} `json:"data"`
	TraceParent     string                 `json:"traceparent,omitempty"` // 全链路追踪
	TenantID        string                 `json:"tenantId"`              // 租户隔离
	SchemaURL       string                 `json:"dataschema,omitempty"`
}

// Bus 事件总线抽象 (NATS JetStream 实现)
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
// 修订(N-1): 事件名与 CloudEvent.Type 统一为 NODE_STARTED/SUCCEEDED/FAILED/SKIPPED
const (
	EventNodeStarted   = "NODE_STARTED"
	EventNodeSucceeded = "NODE_SUCCEEDED"
	EventNodeFailed    = "NODE_FAILED"
	EventNodeSkipped   = "NODE_SKIPPED"
	EventAskHuman      = "AGENT_ASK_HUMAN"
)

// topic 前缀约定，供订阅使用
const (
	TopicPrefix = "<tenant>/events"
)
```

---

## 3. 协议层接口

### 3.1 MCP Registry / Proxy

```go
package mcp

// Tool 一个 MCP 工具的描述
type Tool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	InputSchema map[string]any    `json:"inputSchema"`
	Endpoint    string            `json:"endpoint"` // MCP Server 地址
	Auth        string            `json:"auth"`     // 鉴权方式
	RateLimit   RateLimit         `json:"rateLimit,omitempty"`
	Redact      []string          `json:"redact,omitempty"` // 敏感字段脱敏
	Audit       bool              `json:"audit"`
}

// Registry MCP 工具注册与鉴权中心
type Registry interface {
	Register(ctx context.Context, tool *Tool) error
	Unregister(ctx context.Context, name string) error
	// Authorize 校验租户+Agent 是否有权调用该工具
	// 修订(P1-4): 增加 params 做数据级 ABAC，返回 DataScope 用于注入数据过滤条件
	Authorize(ctx context.Context, tenantID, agentID, toolName string, params map[string]any) (*Tool, *DataScope, error)
	List(ctx context.Context, tenantID string) ([]*Tool, error)
}

// DataScope 数据范围过滤条件，防止跨租户数据可达(ABAC)
type DataScope struct {
	Filter map[string]any `json:"filter"` // 注入到工具请求的过滤条件，如 {tenant: "tenant-a"}
	Redact []string       `json:"redact,omitempty"` // 需脱敏字段
}

// Proxy Sidecar 内的 MCP 代理
type Proxy interface {
	// Invoke 代理调用，执行鉴权/数据级过滤/脱敏/审计
	Invoke(ctx context.Context, agentID, toolName string, args map[string]any) (map[string]any, error)
}
```

### 3.2 A2A Gateway

```go
package a2a

// AgentCard A2A 协议的能力描述
type AgentCard struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	URL         string          `json:"url"`
	Version     string          `json:"version"`
	Skills      []AgentSkill    `json:"skills"`
	Auth        map[string]any  `json:"auth,omitempty"`
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
	ID          string `json:"id"`
	Type        string `json:"type"` // "task/send" / "message/send"
	Input       map[string]any `json:"input"`
	RequireReply bool  `json:"requireReply"`
	TraceParent string `json:"traceparent,omitempty"`
}

type TaskResult struct {
	TaskID   string `json:"taskId"`
	State    string `json:"state"` // completed / failed / in-progress
	Artifacts []Artifact `json:"artifacts,omitempty"`
	Message  string `json:"message,omitempty"`
}

type Artifact struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // file / code / text / data
	URI     string `json:"uri,omitempty"`
	Content string `json:"content,omitempty"`
}
```

---

## 4. 沙箱控制器接口

```go
package sandbox

// RuntimeAdapter 沙箱运行时适配器（gVisor / Firecracker / Kata）
type RuntimeAdapter interface {
	Name() string
	// Provision 创建沙箱(Pod/VM)
	Provision(ctx context.Context, spec *v1.AgentSpec, tenant string) (*SandboxHandle, error)
	// Destroy 销毁沙箱并清理资源
	Destroy(ctx context.Context, handle *SandboxHandle) error
	// Suspend 挂起(Firecracker 快照)
	Suspend(ctx context.Context, handle *SandboxHandle) error
	// Resume 恢复
	Resume(ctx context.Context, handle *SandboxHandle) error
	// Exec 在沙箱内执行命令(供调试)
	Exec(ctx context.Context, handle *SandboxHandle, cmd []string) ([]byte, error)
}

type SandboxHandle struct {
	PodName      string
	Namespace    string
	RuntimeClass string
	AgentPod     *corev1.Pod
	SnapshotPath string // Firecracker 快照路径
}
```

### 4.2 Event Relay Sidecar（修订：评审 P0-2）

> 沙箱内 Agent 与外部事件总线/协议网关的**唯一安全出口**。Agent 仅通过本地 `unix://` socket 通信，**不持有任何外部凭证**。

```go
package relay

// Relay Sandbox Sidecar 内的 Event Relay + MCP Proxy 统一代理
type Relay interface {
	// Start 启动本地 socket 服务，等待 Agent 连接
	Start(ctx context.Context, cfg *Config) error

	// DeliverToAgent 将控制面事件投递给沙箱内 Agent(经本地 socket)
	DeliverToAgent(ctx context.Context, evt *eventbus.CloudEvent) error

	// ReceiveFromAgent 接收 Agent 上报的结果事件，做租户校验/签名后发往总线
	ReceiveFromAgent(ctx context.Context, evt *eventbus.CloudEvent) error
}

type Config struct {
	LocalSocket   string // e.g. /var/run/agent.sock
	TenantID      string
	AgentID       string
	NATSCredsPath string // 仅 Relay 持有
	MCPRegistryAddr string
	A2AGatewayAddr  string
	AuditSink       string
}
```

---

## 5. 关键依赖版本建议

| 依赖 | 版本 | 用途 |
|------|------|------|
| sigs.k8s.io/controller-runtime | v0.18+ | Operator 控制循环 |
| k8s.io/apimachinery | v0.28+ | CRD 类型 |
| github.com/nats-io/nats.go | v1.36+ | NATS JetStream |
| github.com/cloudevents/sdk-go | v2 | CloudEvents 事件封装 |
| go.temporal.io/sdk | v1.28+ | 编排执行底座（正式依赖，ADR-02） |
| github.com/openfga/openfga | v1.5+ | MCP 数据级 ABAC 授权（P1-4） |
| github.com/spf13/cobra | v1.8 | CLI |
| go.opentelemetry.io/otel | v1.28 | 可观测 |

---

## 6. 快速上手指引（M1 验证目标）

```bash
# 1. 生成工程脚手架
kubebuilder init --domain agent.runtime.io --repo github.com/example/agent-runtime-operator
kubebuilder create api --group agent --version v1 --kind Tenant --resource --controller
kubebuilder create api --group agent --version v1 --kind Sandbox --resource --controller
kubebuilder create api --group agent --version v1 --kind Agent --resource --controller

# 2. 本地启动 Operator(连集群)
make run

# 3. 创建一个最小 gVisor 沙箱 Agent 并观察调谐日志
kubectl apply -f config/samples/agent_gvisor.yaml
```

**M1 验收标准（分两步，修订 P2-4）**：
- **M1-a**：`Sandbox` 被 Operator 自动创建为**普通 Pod**，Agent 启动并完成健康检查。
- **M1-b**：切换为 **gVisor RuntimeClass + Event Relay Sidecar**，Agent 通过本地 socket 收发事件，租户隔离（NetworkPolicy Deny-All）与事件通路（4.1.4 / relay.4.2）生效。
