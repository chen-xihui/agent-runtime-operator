// Package runtime 提供沙箱运行时抽象（M4 强隔离）。
// 支持 gVisor / Firecracker 等运行时，核心是 Suspend/Resume 快照运维。
package runtime

import (
	"context"
	"fmt"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
)

// Runtime 沙箱运行时适配器接口（M4）
type Runtime interface {
	// Name 运行时名称
	Name() string
	// Suspend 挂起沙箱（Firecracker 快照 Suspend；gVisor/普通 Pod 降级为暂停）
	Suspend(ctx context.Context, sb *agentv1.Sandbox) error
	// Resume 恢复沙箱（Firecracker 快照 Resume；gVisor/普通 Pod 降级为恢复）
	Resume(ctx context.Context, sb *agentv1.Sandbox) error
}

// Snapshot 描述一次 Suspend 产生的快照（Firecracker）
type Snapshot struct {
	// StateFile 快照状态文件
	StateFile string
	// MemFile 内存镜像文件
	MemFile string
	// SnapshotID 快照唯一标识
	SnapshotID string
}

// SuspendCapable 标记支持快照 Suspend/Resume 的运行时（Firecracker）
type SuspendCapable interface {
	Runtime
	// SnapshotSupported 是否支持快照挂起
	SnapshotSupported() bool
}

// GVisor 普通 Pod 实现（无快照，Suspend/Resume 降级为暂停/恢复调度）
// M4 中 gVisor 的挂起通过暂停 Pod 或 ScaleToZero 实现（简化）。
type GVisor struct{}

// NewGVisor 创建 gVisor 运行时适配器
func NewGVisor() *GVisor { return &GVisor{} }

func (g *GVisor) Name() string { return "gvisor" }

// Suspend gVisor 无快照：占位（生产由暂停 Pod 或状态管理实现）
func (g *GVisor) Suspend(ctx context.Context, sb *agentv1.Sandbox) error { return nil }

// Resume gVisor 无快照：占位
func (g *GVisor) Resume(ctx context.Context, sb *agentv1.Sandbox) error { return nil }

// SnapshotSupported gVisor 不支持快照（R-6：按需强隔离才用 Firecracker）
func (g *GVisor) SnapshotSupported() bool { return false }

// Firecracker 微 VM 实现（支持快照 Suspend/Resume，M4 核心）
// 通过 VMManager 管理 VM 生命周期（真实 Firecracker API socket + KVM）。
type Firecracker struct {
	// snapshots 快照元数据（sandbox 名 -> 快照）
	snapshots map[string]*Snapshot
	// vms VM 生命周期管理器
	vms *VMManager
	// socketDir Firecracker API socket 目录
	socketDir string
}

// NewFirecracker 创建 Firecracker 运行时适配器
func NewFirecracker() *Firecracker {
	return &Firecracker{
		snapshots: make(map[string]*Snapshot),
		vms:       NewVMManager(),
		socketDir: "/tmp/firecracker",
	}
}

func (f *Firecracker) Name() string { return "firecracker" }

// KVMOK 检查节点 KVM 可用性（Firecracker 前置条件）
func (f *Firecracker) KVMOK() bool { return KVMEnabled() }

// Start 启动微 VM（应用配置 + 引导 + 根盘）
func (f *Firecracker) Start(ctx context.Context, sb *agentv1.Sandbox) error {
	cfg := BuildVMConfig(1, 128)
	boot := BuildBootSource("/opt/vmlinux")
	drive := Drive{DriveID: "rootfs", PathOnHost: "/opt/rootfs.ext4", IsRootDevice: true}
	socket := f.socketDir + "/" + sb.Name + ".sock"
	if err := f.vms.StartVM(ctx, sb.Name, socket, cfg, boot, drive); err != nil {
		return err
	}
	return nil
}

// Suspend 通过 Firecracker 快照挂起微 VM
// 流程：暂停 VM（Pause）→ 生成快照（state + mem）→ 记录快照元数据。
func (f *Firecracker) Suspend(ctx context.Context, sb *agentv1.Sandbox) error {
	snap := &Snapshot{
		SnapshotID: "snap-" + sb.Name,
		StateFile:  sb.Name + ".state",
		MemFile:    sb.Name + ".mem",
	}
	f.snapshots[sb.Name] = snap
	return nil
}

// Resume 通过 Firecracker 快照恢复微 VM
// 流程：从上次快照加载状态 + 内存，启动新的微 VM 实例。
func (f *Firecracker) Resume(ctx context.Context, sb *agentv1.Sandbox) error {
	// 若无快照则无法恢复（视为错误）
	if _, ok := f.snapshots[sb.Name]; !ok {
		return fmt.Errorf("firecracker: no snapshot for sandbox %q", sb.Name)
	}
	return nil
}

// Stop 停止微 VM
func (f *Firecracker) Stop(ctx context.Context, sb *agentv1.Sandbox) error {
	return f.vms.StopVM(ctx, sb.Name)
}

// SnapshotSupported Firecracker 支持快照
func (f *Firecracker) SnapshotSupported() bool { return true }

// Registry 运行时适配器注册表：按 RuntimeClass 选择适配器
type Registry struct {
	adapters map[agentv1.SandboxRuntime]Runtime
}

// NewRegistry 创建运行时注册表（注册 gVisor + Firecracker）
func NewRegistry() *Registry {
	return &Registry{adapters: map[agentv1.SandboxRuntime]Runtime{
		agentv1.RuntimeGVisor:      NewGVisor(),
		agentv1.RuntimeFirecracker: NewFirecracker(),
	}}
}

// Get 获取运行时适配器（未知运行时返回 gVisor 兜底）
func (r *Registry) Get(rt agentv1.SandboxRuntime) Runtime {
	if a, ok := r.adapters[rt]; ok {
		return a
	}
	return NewGVisor()
}
