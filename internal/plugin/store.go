package plugin

import (
	"context"
	"fmt"
	"sync"
)

// AgentPlugins 管理单个 Agent 已安装/启用的插件集合。
// 插件市场按 Agent 维度绑定：Agent 挂载插件后获得对应能力扩展。
type AgentPlugins struct {
	mu      sync.RWMutex
	agentID string
	// registry 全局插件注册中心（提供插件定义）
	registry *Registry
	// installed 该 Agent 已安装的插件名集合
	installed map[string]struct{}
}

// NewAgentPlugins 为指定 Agent 创建插件管理器
func NewAgentPlugins(agentID string, registry *Registry) *AgentPlugins {
	return &AgentPlugins{
		agentID:   agentID,
		registry:  registry,
		installed: make(map[string]struct{}),
	}
}

// Mount 挂载插件到 Agent（从注册中心安装）
func (a *AgentPlugins) Mount(ctx context.Context, pluginName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.installed[pluginName]; ok {
		return nil // 已挂载，幂等
	}
	if _, err := a.registry.Get(pluginName); err != nil {
		return err // 插件不存在
	}
	a.installed[pluginName] = struct{}{}
	return nil
}

// Unmount 从 Agent 卸载插件
func (a *AgentPlugins) Unmount(ctx context.Context, pluginName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.installed[pluginName]; !ok {
		return nil
	}
	delete(a.installed, pluginName)
	return nil
}

// Installed 返回 Agent 已安装的插件名（确定性排序）
func (a *AgentPlugins) Installed() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, 0, len(a.installed))
	for name := range a.installed {
		out = append(out, name)
	}
	// 简单排序保证确定性
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ListEnabled 列出该 Agent 已安装且全局启用的插件
func (a *AgentPlugins) ListEnabled(ctx context.Context) ([]*Plugin, error) {
	a.mu.RLock()
	names := make([]string, 0, len(a.installed))
	for name := range a.installed {
		names = append(names, name)
	}
	a.mu.RUnlock()

	var out []*Plugin
	for _, name := range names {
		p, err := a.registry.Get(name)
		if err != nil {
			continue
		}
		if p.State == StateEnabled {
			out = append(out, p)
		}
	}
	return out, nil
}

// HasPlugin 判断 Agent 是否安装了指定插件
func (a *AgentPlugins) HasPlugin(pluginName string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.installed[pluginName]
	return ok
}

// Call 调用 Agent 已安装且启用的插件
func (a *AgentPlugins) Call(ctx context.Context, pluginName string, args map[string]interface{}) (map[string]interface{}, error) {
	if !a.HasPlugin(pluginName) {
		return nil, fmt.Errorf("plugin %q not mounted on agent %q", pluginName, a.agentID)
	}
	return a.registry.Call(ctx, pluginName, args)
}
