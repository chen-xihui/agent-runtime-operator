package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PluginSpec 插件定义（M5 插件市场，注册为 K8s 资源）
type PluginSpec struct {
	// Version 语义化版本（如 1.2.0）
	Version string `json:"version"`
	// Type 插件类型（tool/skill/hook/visual）
	Type string `json:"type"`
	// Description 描述
	Description string `json:"description,omitempty"`
	// Author 作者
	Author string `json:"author,omitempty"`
	// Tags 标签（发现用）
	Tags []string `json:"tags,omitempty"`
	// RequiresAgents 适用的 Agent 类型（可选）
	RequiresAgents []string `json:"requiresAgents,omitempty"`
	// Enabled 是否启用（默认 true）
	Enabled *bool `json:"enabled,omitempty"`
}

// PluginStatus 插件实际状态
type PluginStatus struct {
	// State 状态（installed/enabled/disabled）
	State string `json:"state,omitempty"`
	// InstalledVersion 当前安装版本
	InstalledVersion string `json:"installedVersion,omitempty"`
	// Message 状态信息
	Message string `json:"message,omitempty"`
}

// Plugin 是插件市场资源（M5）
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=plug,scope=Cluster
type Plugin struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PluginSpec   `json:"spec,omitempty"`
	Status PluginStatus `json:"status,omitempty"`
}

// PluginList 包含 Plugin 列表
// +kubebuilder:object:root=true
type PluginList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Plugin `json:"items"`
}
