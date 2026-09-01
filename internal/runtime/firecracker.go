package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// ===================== KVM 检测 =====================

// KVMDevPath KVM 设备路径
const KVMDevPath = "/dev/kvm"

// KVMEnabled 检查节点是否支持 KVM（Firecracker 前置条件，design-doc 9.1）
func KVMEnabled() bool {
	fi, err := os.Stat(KVMDevPath)
	if err != nil {
		return false
	}
	// /dev/kvm 应是一个设备节点
	return fi.Mode()&os.ModeDevice != 0
}

// ===================== VM 配置 =====================

// VMConfig Firecracker 微 VM 配置（对应 PUT /machine-config）
type VMConfig struct {
	VCPUCount   int    `json:"vcpu_count"`
	MemSizeMib  int    `json:"mem_size_mib"`
	HtEnabled   bool   `json:"ht_enabled"`
	CPUTemplate string `json:"cpu_template,omitempty"`
}

// BootSource 微内核引导配置（PUT /boot-source）
type BootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArgs        string `json:"boot_args"`
}

// Drive 根文件系统盘（PUT /drives）
type Drive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
}

// BuildVMConfig 生成 Firecracker VM 配置（默认 1 vCPU + 128MB）
func BuildVMConfig(cpu, memMB int) VMConfig {
	if cpu <= 0 {
		cpu = 1
	}
	if memMB <= 0 {
		memMB = 128
	}
	return VMConfig{VCPUCount: cpu, MemSizeMib: memMB, HtEnabled: false}
}

// BuildBootSource 生成微内核引导配置
func BuildBootSource(kernelPath string, bootArgs ...string) BootSource {
	args := "console=ttyS0 reboot=k panic=1 pci=off"
	if len(bootArgs) > 0 && bootArgs[0] != "" {
		args = bootArgs[0]
	}
	return BootSource{KernelImagePath: kernelPath, BootArgs: args}
}

// ===================== VM 生命周期管理 =====================

// VM 表示一个 Firecracker 微 VM 实例
type VM struct {
	// ID 微 VM ID（对应 sandbox 名）
	ID string
	// SocketPath Firecracker API unix socket 路径
	SocketPath string
	// PID Firecracker 进程 PID
	PID int
}

// VMState Firecracker 微 VM 状态
type VMState struct {
	State string `json:"state"` // NotStarted/Running/Paused
}

// VMManager 通过 Firecracker API socket 管理微 VM 生命周期
// 真实环境由 firecracker 二进制 + KVM 支撑；此处实现 HTTP/Unix socket API 客户端。
type VMManager struct {
	// apiClient 可注入的 API 客户端（测试 mock）
	apiClient HTTPDoer
	// baseURL Firecracker API 基础地址（默认 http://localhost，经 unix socket 转发）
	baseURL string
	// vms 内存态 VM 记录（生产以实际 VM 为准）
	vms map[string]*VM
}

// HTTPDoer 抽象 HTTP 客户端（便于测试注入 mock）
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// NewVMManager 创建 VM 管理器
func NewVMManager() *VMManager {
	return &VMManager{
		baseURL: "http://localhost",
		vms:     make(map[string]*VM),
	}
}

// WithAPIClient 注入自定义 API 客户端（测试用）
func (m *VMManager) WithAPIClient(c HTTPDoer) *VMManager {
	m.apiClient = c
	return m
}

// WithBaseURL 设置 Firecracker API 基础地址（测试用）
func (m *VMManager) WithBaseURL(url string) *VMManager {
	m.baseURL = url
	return m
}

// Client 返回该 VM 的 HTTP client（unix socket transport）
func (m *VMManager) Client(vm *VM) *http.Client {
	// 使用 unix socket 连接 Firecracker API
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", vm.SocketPath)
			},
		},
		Timeout: 10 * time.Second,
	}
}

// StartVM 启动微 VM（应用配置 + 引导）
// 流程：PUT /machine-config + /boot-source + /drives → PUT /actions (InstanceStart)
func (m *VMManager) StartVM(ctx context.Context, id, socketPath string, cfg VMConfig, boot BootSource, drive Drive) error {
	vm := &VM{ID: id, SocketPath: socketPath}
	client := m.vmClient(vm)

	// 1. 机器配置
	if err := putJSON(ctx, client, m.baseURL+"/machine-config", cfg); err != nil {
		return fmt.Errorf("set machine config: %w", err)
	}
	// 2. 引导源
	if err := putJSON(ctx, client, m.baseURL+"/boot-source", boot); err != nil {
		return fmt.Errorf("set boot source: %w", err)
	}
	// 3. 根盘
	if err := putJSON(ctx, client, m.baseURL+"/drives/rootfs", drive); err != nil {
		return fmt.Errorf("set drive: %w", err)
	}
	// 4. 启动实例
	if err := putJSON(ctx, client, m.baseURL+"/actions", map[string]string{
		"action_type": "InstanceStart",
	}); err != nil {
		return fmt.Errorf("start instance: %w", err)
	}
	m.vms[id] = vm
	return nil
}

// StopVM 停止微 VM
func (m *VMManager) StopVM(ctx context.Context, id string) error {
	vm, ok := m.vms[id]
	if !ok {
		return fmt.Errorf("firecracker: vm %q not found", id)
	}
	client := m.vmClient(vm)
	if err := putJSON(ctx, client, m.baseURL+"/actions", map[string]string{
		"action_type": "SendCtrlAltDel",
	}); err != nil {
		return fmt.Errorf("stop vm: %w", err)
	}
	delete(m.vms, id)
	return nil
}

// State 获取微 VM 状态
func (m *VMManager) State(ctx context.Context, id string) (VMState, error) {
	vm, ok := m.vms[id]
	if !ok {
		return VMState{}, fmt.Errorf("firecracker: vm %q not found", id)
	}
	client := m.vmClient(vm)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, m.baseURL+"/vm", nil)
	resp, err := client.Do(req)
	if err != nil {
		return VMState{}, fmt.Errorf("get vm state: %w", err)
	}
	defer resp.Body.Close()
	var st VMState
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return VMState{}, fmt.Errorf("decode state: %w", err)
	}
	return st, nil
}

// vmClient 返回 VM 请求的 HTTP client（优先用注入的 apiClient，否则 unix socket client）
func (m *VMManager) vmClient(vm *VM) HTTPDoer {
	if m.apiClient != nil {
		return m.apiClient
	}
	return m.Client(vm)
}

// putJSON 发送 PUT JSON 请求到 Firecracker API
func putJSON(ctx context.Context, client HTTPDoer, url string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
