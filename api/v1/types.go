package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SandboxRuntime 沙箱运行时类型
type SandboxRuntime string

const (
	RuntimeGVisor      SandboxRuntime = "gvisor"      // 用户态内核
	RuntimeFirecracker SandboxRuntime = "firecracker" // 微VM
	RuntimeKata        SandboxRuntime = "kata"        // 备选
)

// 状态阶段常量（统一使用 phase，P2-3）
const (
	PhasePending      = "Pending"
	PhaseProvisioning = "Provisioning"
	PhaseRunning      = "Running"
	PhaseSuspended    = "Suspended"
	PhaseTerminating  = "Terminating"
	PhaseTerminated   = "Terminated"
	PhaseFailed       = "Failed"

	PhaseRunRunning    = "RUNNING"
	PhaseRunSucceeded  = "SUCCEEDED"
	PhaseRunFailed     = "FAILED"
	PhaseRunCancelled  = "CANCELLED"
)

// ===================== Agent =====================

// AgentSpec 定义 Agent 的期望状态
type AgentSpec struct {
	Image        string            `json:"image"` // Agent 镜像
	Runtime      RuntimeSpec       `json:"runtime"`
	Entrypoint   []string          `json:"entrypoint,omitempty"`
	MCP          MCPSpec           `json:"mcp"`
	A2A          A2ASpec           `json:"a2a,omitempty"`
	Capabilities CapabilitiesSpec  `json:"capabilities,omitempty"`
	Security     SecuritySpec      `json:"security"`
	Labels       map[string]string `json:"labels,omitempty"`
}

type RuntimeSpec struct {
	Class     SandboxRuntime             `json:"class"`
	Resources corev1.ResourceRequirements `json:"resources"`
	// SnapshotEnabled 开启后支持 Firecracker 快照 Suspend/Resume
	SnapshotEnabled bool `json:"snapshotEnabled,omitempty"`
}

type MCPSpec struct {
	// AllowedTools 仅做引用(R-4)：指向 ToolBinding 中已授权的工具名
	AllowedTools []string `json:"allowedTools"`
	// Endpoints 引用的 MCPEndpoint 名称列表（只管"工具在哪"，不管权限，R-4）
	Endpoints []string `json:"endpoints"`
	// WhitelistDomains 远程工具域名白名单
	WhitelistDomains []string `json:"whitelistDomains,omitempty"`
}

type A2ASpec struct {
	// Enabled 是否参与 A2A 互操作（A2A 能力声明的唯一来源，C2）
	Enabled bool `json:"enabled"`
	// Tasks 可被外部委派的任务 ID 集合
	Tasks []string `json:"tasks,omitempty"`
	// AgentCard A2A 能力描述
	AgentCard map[string]interface{} `json:"agentCard,omitempty"`
}

// CapabilitiesSpec 能力汇总（仅展示性，不承载授权语义，C2）
type CapabilitiesSpec struct {
	Protocols []string `json:"protocols"`
}

type SecuritySpec struct {
	RunAsNonRoot   bool     `json:"runAsNonRoot"`
	ReadOnlyRootFS bool     `json:"readOnlyRootFS,omitempty"`
	NetworkPolicy  string   `json:"networkPolicy"`
	EnvSecrets     []string `json:"envSecrets,omitempty"`
	SeccompProfile string   `json:"seccompProfile,omitempty"`
	// MaxLifetimeMin 沙箱最大存活时长(自动回收)
	MaxLifetimeMin int `json:"maxLifetimeMin,omitempty"`
}

// AgentStatus 实际状态
type AgentStatus struct {
	Phase              string      `json:"phase"` // Pending/Provisioning/Running/Suspended/Terminating
	SandboxRef         string      `json:"sandboxRef,omitempty"`
	MCPConnectedTools  []string    `json:"mcpConnectedTools,omitempty"`
	LastHeartbeat      metav1.Time `json:"lastHeartbeat,omitempty"`
}

// Agent 是 Agent 编排资源
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=agt,scope=Namespaced
type Agent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentSpec   `json:"spec,omitempty"`
	Status AgentStatus `json:"status,omitempty"`
}

// AgentList 包含 Agent 列表
// +kubebuilder:object:root=true
type AgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Agent `json:"items"`
}

// ===================== Sandbox =====================

// SandboxSpec 沙箱期望状态
type SandboxSpec struct {
	AgentRef     string        `json:"agentRef"`     // 关联 Agent 对象名
	RuntimeClass SandboxRuntime `json:"runtimeClass"`
	Resources    corev1.ResourceRequirements `json:"resources"`
	Image        string        `json:"image"`
	Entrypoint   []string      `json:"entrypoint,omitempty"`
	// EnableRelay Event Relay Sidecar 注入
	EnableRelay  bool          `json:"enableRelay,omitempty"`
	// RunAsNonRoot 是否以非 root 运行（来自 Agent.spec.security.runAsNonRoot，默认 true）
	RunAsNonRoot *bool         `json:"runAsNonRoot,omitempty"`
}

// SandboxStatus 沙箱实际状态
type SandboxStatus struct {
	Phase              string            `json:"phase"` // Pending/Provisioning/Running/Suspended/Terminating
	RuntimeClass       SandboxRuntime    `json:"runtimeClass,omitempty"`
	PodName            string            `json:"podName,omitempty"`
	NetworkPolicy      string            `json:"networkPolicy,omitempty"`
	MountInfo          *MountInfo        `json:"mountInfo,omitempty"`
	RelayReady         bool              `json:"relayReady,omitempty"` // Event Relay Sidecar 就绪(见 4.1.4)
	LastTransitionTime metav1.Time       `json:"lastTransitionTime,omitempty"`
	Message            string            `json:"message,omitempty"`
}

type MountInfo struct {
	PVC       string `json:"pvc,omitempty"`
	Encrypted bool   `json:"encrypted,omitempty"`
}

// Sandbox 是沙箱实例资源
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
type Sandbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxSpec   `json:"spec,omitempty"`
	Status SandboxStatus `json:"status,omitempty"`
}

// SandboxList 包含 Sandbox 列表
// +kubebuilder:object:root=true
type SandboxList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sandbox `json:"items"`
}

// ===================== Tenant =====================

// TenantSpec 租户定义
type TenantSpec struct {
	// Quota 资源配额
	Quota QuotaSpec `json:"quota,omitempty"`
	// NetworkPolicy 默认网络策略模板名
	NetworkPolicy string `json:"networkPolicy,omitempty"`
	// Labels 注入到租户命名空间的标签
	Labels map[string]string `json:"labels,omitempty"`
}

type QuotaSpec struct {
	MaxSandboxes int `json:"maxSandboxes,omitempty"`
	MaxAgents    int `json:"maxAgents,omitempty"`
	MaxCPU       string `json:"maxCpu,omitempty"`
	MaxMemory    string `json:"maxMemory,omitempty"`
}

// TenantStatus 租户实际状态
type TenantStatus struct {
	Phase             string `json:"phase,omitempty"`
	Namespace         string `json:"namespace,omitempty"`
	SandboxCount      int    `json:"sandboxCount,omitempty"`
	AgentCount        int    `json:"agentCount,omitempty"`
}

// Tenant 是租户资源
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
type Tenant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TenantSpec   `json:"spec,omitempty"`
	Status TenantStatus `json:"status,omitempty"`
}

// TenantList 包含 Tenant 列表
// +kubebuilder:object:root=true
type TenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Tenant `json:"items"`
}
