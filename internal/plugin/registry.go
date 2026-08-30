// Package plugin 提供 Agent Runtime 的插件市场与扩展机制（M5）。
// 插件为 Agent 提供可扩展能力（工具、技能、事件处理器等），
// 支持注册、发现、版本管理、安装/卸载、启停。
package plugin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
)

// 插件状态
const (
	StateInstalled = "installed" // 已安装
	StateEnabled   = "enabled"   // 已启用（生效）
	StateDisabled  = "disabled"  // 已禁用
)

// 插件类型
const (
	TypeTool      = "tool"      // 工具扩展
	TypeSkill     = "skill"     // 技能扩展
	TypeHook      = "hook"      // 事件钩子扩展
	TypeVisual    = "visual"    // 可视化/交互扩展
)

// 常见错误
var (
	ErrPluginNotFound = errors.New("plugin: not found")
	ErrPluginExists   = errors.New("plugin: already exists")
	ErrPluginConflict = errors.New("plugin: version conflict")
	ErrPluginDisabled = errors.New("plugin: disabled")
)

// Manifest 插件元数据
type Manifest struct {
	// Name 插件唯一名
	Name string `json:"name"`
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
}

// Plugin 运行时插件实例
type Plugin struct {
	Manifest
	// State 当前状态
	State string `json:"state"`
	// Handler 插件执行回调（依类型不同：工具调用/技能执行/事件钩子）
	Handler PluginHandler `json:"-"`
}

// PluginHandler 插件执行函数（具体行为由上层注入）
type PluginHandler func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error)

// Registry 插件注册中心（内存实现，M5）
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]*Plugin // 插件名 -> 插件（仅保留当前版本）
	// 版本历史：插件名 -> 版本列表
	versions map[string][]string
}

// NewRegistry 创建插件注册中心
func NewRegistry() *Registry {
	return &Registry{
		plugins:  make(map[string]*Plugin),
		versions: make(map[string][]string),
	}
}

// Install 安装插件（注册到市场并启用）
func (r *Registry) Install(ctx context.Context, m Manifest, handler PluginHandler) error {
	if m.Name == "" || m.Version == "" {
		return fmt.Errorf("plugin: name and version required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.plugins[m.Name]; ok {
		// 已存在同版本 → 冲突
		if existing.Version == m.Version {
			return ErrPluginExists
		}
		// 版本降级 → 拒绝
		if compareVersions(m.Version, existing.Version) < 0 {
			return ErrPluginConflict
		}
	}
	r.plugins[m.Name] = &Plugin{
		Manifest: m,
		State:    StateEnabled,
		Handler:  handler,
	}
	r.versions[m.Name] = append(r.versions[m.Name], m.Version)
	return nil
}

// Uninstall 卸载插件
func (r *Registry) Uninstall(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.plugins[name]; !ok {
		return ErrPluginNotFound
	}
	delete(r.plugins, name)
	return nil
}

// Enable 启用插件
func (r *Registry) Enable(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.plugins[name]
	if !ok {
		return ErrPluginNotFound
	}
	p.State = StateEnabled
	return nil
}

// Disable 禁用插件
func (r *Registry) Disable(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.plugins[name]
	if !ok {
		return ErrPluginNotFound
	}
	p.State = StateDisabled
	return nil
}

// Get 获取插件
func (r *Registry) Get(name string) (*Plugin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[name]
	if !ok {
		return nil, ErrPluginNotFound
	}
	cp := *p
	return &cp, nil
}

// List 列出插件（按名排序，可过滤类型）
func (r *Registry) List(pluginType string) []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*Plugin
	for _, p := range r.plugins {
		if pluginType != "" && p.Type != pluginType {
			continue
		}
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Discover 按标签/类型发现插件
func (r *Registry) Discover(tags []string, pluginType string) []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*Plugin
	for _, p := range r.plugins {
		if pluginType != "" && p.Type != pluginType {
			continue
		}
		if !hasAnyTag(p.Tags, tags) {
			continue
		}
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Call 调用插件（仅允许 enabled 状态）
func (r *Registry) Call(ctx context.Context, name string, args map[string]interface{}) (map[string]interface{}, error) {
	r.mu.RLock()
	p, ok := r.plugins[name]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrPluginNotFound
	}
	if p.State != StateEnabled {
		return nil, ErrPluginDisabled
	}
	if p.Handler == nil {
		return nil, fmt.Errorf("plugin %q has no handler", name)
	}
	return p.Handler(ctx, args)
}

// Versions 获取插件的版本历史
func (r *Registry) Versions(name string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.versions[name]))
	copy(out, r.versions[name])
	return out
}

func hasAnyTag(have, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		if slices.Contains(have, w) {
			return true
		}
	}
	return false
}

// compareVersions 简单语义化版本比较（a < b 返回负数，a > b 返回正数）
func compareVersions(a, b string) int {
	as := parseVersion(a)
	bs := parseVersion(b)
	for i := range 3 {
		if as[i] != bs[i] {
			if as[i] < bs[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// parseVersion 解析 x.y.z（缺失补 0）
func parseVersion(v string) [3]int {
	var out [3]int
	idx := 0
	num := 0
	for i := 0; i < len(v) && idx < 3; i++ {
		c := v[i]
		if c >= '0' && c <= '9' {
			num = num*10 + int(c-'0')
		} else {
			out[idx] = num
			num = 0
			idx++
		}
	}
	if idx < 3 {
		out[idx] = num
	}
	return out
}
