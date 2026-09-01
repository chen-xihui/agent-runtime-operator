// 方案 A：协议级端到端验证（无需 KVM 硬件）。
// 用"协议忠实"的 mock Firecracker API（监听 unix socket），驱动【生产代码 VMManager 的真实 unix-socket 客户端】
// 打通 StartVM → State → StopVM 完整链路，并校验线上请求 payload 与真实 Firecracker API 契约一致。
// 与 firecracker_test.go 的区别：不注入 HTTP apiClient，走 VMManager.Client() 的 unix socket 传输。
package runtime

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
)

// firecrackerState 记录 mock 内部状态机（模拟真实 Firecracker：NotStarted → Running → Paused）
type firecrackerState string

const (
	stateNotStarted firecrackerState = "NotStarted"
	stateRunning    firecrackerState = "Running"
	statePaused     firecrackerState = "Paused"
)

// protoFirecrackerAPI 协议级 mock：校验请求 payload，按真实 Firecracker 语义返回
type protoFirecrackerAPI struct {
	mu    sync.Mutex
	state firecrackerState
	// 记录收到的 payload（校验用）
	lastMachineConfig VMConfig
	lastBootSource    BootSource
	lastDrive         Drive
	// 收到的 action_type 序列（校验 InstanceStart 顺序）
	actions []string
}

func newProtoFirecrackerAPI() *protoFirecrackerAPI {
	return &protoFirecrackerAPI{state: stateNotStarted}
}

func (p *protoFirecrackerAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch r.Method {
	case http.MethodPut:
		switch r.URL.Path {
		case "/machine-config":
			var c VMConfig
			if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
				http.Error(w, "bad machine-config", http.StatusBadRequest)
				return
			}
			// 契约校验：vcpu_count >= 1、mem_size_mib >= 1（真实 Firecracker 强制）
			if c.VCPUCount < 1 || c.MemSizeMib < 1 {
				http.Error(w, "invalid resource sizes", http.StatusBadRequest)
				return
			}
			p.lastMachineConfig = c
			w.WriteHeader(http.StatusNoContent)
		case "/boot-source":
			var b BootSource
			if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
				http.Error(w, "bad boot-source", http.StatusBadRequest)
				return
			}
			if b.KernelImagePath == "" || b.BootArgs == "" {
				http.Error(w, "kernel image path & boot args required", http.StatusBadRequest)
				return
			}
			p.lastBootSource = b
			w.WriteHeader(http.StatusNoContent)
		case "/drives/rootfs":
			var d Drive
			if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
				http.Error(w, "bad drive", http.StatusBadRequest)
				return
			}
			if d.PathOnHost == "" || !d.IsRootDevice {
				http.Error(w, "rootfs drive must have path_on_host & is_root_device=true", http.StatusBadRequest)
				return
			}
			p.lastDrive = d
			w.WriteHeader(http.StatusNoContent)
		case "/actions":
			var act map[string]string
			if err := json.NewDecoder(r.Body).Decode(&act); err != nil {
				http.Error(w, "bad action", http.StatusBadRequest)
				return
			}
			switch act["action_type"] {
			case "InstanceStart":
				if p.state == stateNotStarted {
					p.state = stateRunning
				}
				p.actions = append(p.actions, act["action_type"])
				w.WriteHeader(http.StatusNoContent)
			case "SendCtrlAltDel":
				p.actions = append(p.actions, act["action_type"])
				p.state = stateNotStarted
				w.WriteHeader(http.StatusNoContent)
			default:
				http.Error(w, "unknown action", http.StatusBadRequest)
			}
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	case http.MethodGet:
		if r.URL.Path == "/vm" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"state": string(p.state)})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// serveOnUnix 在指定 unix socket 上启动 mock HTTP server，返回 socket 路径。
// 注意：Windows 上 unix socket 路径有长度限制，用短临时目录避免超长路径导致 bind: invalid argument。
func serveOnUnix(t *testing.T, api *protoFirecrackerAPI) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fc")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "fc.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	srv := &http.Server{Handler: api}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

// unixClient 返回打 unix socket 的 HTTP client（复现 VMManager.Client 的 DialContext）
func unixClient(sock string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
}

// TestVMManager_Protocol_UnixSocket 走生产 VMManager 的真实 unix-socket 客户端（不注入 apiClient）
func TestVMManager_Protocol_UnixSocket(t *testing.T) {
	api := newProtoFirecrackerAPI()
	sock := serveOnUnix(t, api)
	ctx := context.Background()

	// 生产代码：NewVMManager() 不注入 apiClient → 走 Client() 的 unix socket transport
	m := NewVMManager()
	m.WithBaseURL("http://localhost") // 与生产一致（unix socket 经 DialContext 转发）

	cfg := BuildVMConfig(1, 128)
	boot := BuildBootSource("/opt/vmlinux")
	drive := Drive{DriveID: "rootfs", PathOnHost: "/opt/rootfs.ext4", IsRootDevice: true}

	// 1) StartVM —— 经 unix socket 打 mock，触发 /machine-config + /boot-source + /drives/rootfs + /actions
	if err := m.StartVM(ctx, "sb-fc", sock, cfg, boot, drive); err != nil {
		t.Fatalf("start vm (unix socket): %v", err)
	}

	// 2) 校验 mock 收到的 payload 符合 Firecracker 契约
	api.mu.Lock()
	mc, bs, dr := api.lastMachineConfig, api.lastBootSource, api.lastDrive
	actions := append([]string{}, api.actions...)
	api.mu.Unlock()

	if mc.VCPUCount != 1 || mc.MemSizeMib != 128 {
		t.Fatalf("machine config payload mismatch: %+v", mc)
	}
	if bs.KernelImagePath != "/opt/vmlinux" || bs.BootArgs == "" {
		t.Fatalf("boot source payload mismatch: %+v", bs)
	}
	if dr.PathOnHost != "/opt/rootfs.ext4" || !dr.IsRootDevice {
		t.Fatalf("drive payload mismatch: %+v", dr)
	}
	if len(actions) != 1 || actions[0] != "InstanceStart" {
		t.Fatalf("expected exactly InstanceStart, got %v", actions)
	}

	// 3) State —— 返回 Running（mock 状态机推进）
	st, err := m.State(ctx, "sb-fc")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if st.State != string(stateRunning) {
		t.Fatalf("state = %q, want Running", st.State)
	}

	// 4) StopVM —— 发 SendCtrlAltDel
	if err := m.StopVM(ctx, "sb-fc"); err != nil {
		t.Fatalf("stop vm: %v", err)
	}
	api.mu.Lock()
	actions = append([]string{}, api.actions...)
	api.mu.Unlock()
	if len(actions) != 2 || actions[1] != "SendCtrlAltDel" {
		t.Fatalf("expected SendCtrlAltDel after stop, got %v", actions)
	}
}

// TestVMManager_Protocol_RejectsBadPayload 校验 mock 拒绝不符合契约的 payload（验证协议双向一致）
func TestVMManager_Protocol_RejectsBadPayload(t *testing.T) {
	api := newProtoFirecrackerAPI()
	sock := serveOnUnix(t, api)

	client := unixClient(sock)
	// 直接打一个非法 machine-config（vcpu_count=0），应被 mock 拒绝
	req, _ := http.NewRequest(http.MethodPut, "http://localhost/machine-config",
		strings.NewReader(`{"vcpu_count":0,"mem_size_mib":0}`))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid machine-config, got %d", resp.StatusCode)
	}
}

// TestVMManager_Protocol_AdapterStart 用 Firecracker 适配器 Start 经 VMManager（unix socket）驱动 mock。
// 适配器 Start 计算 socket = socketDir + <name>.sock，此处把 mock socket 就建在该路径上，验证全链路。
func TestVMManager_Protocol_AdapterStart(t *testing.T) {
	api := newProtoFirecrackerAPI()
	sbName := "sb-adapter-fc"

	// 构造目录，并把 mock socket 建在 适配器Start 期望的路径 socketDir/<name>.sock
	dir, err := os.MkdirTemp("", "fc")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, sbName+".sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	srv := &http.Server{Handler: api}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	f := NewFirecracker()
	f.socketDir = dir // 适配器 Start 用 dir/<name>.sock，正好命中上面的 mock
	ctx := context.Background()

	sb := &agentv1.Sandbox{}
	sb.Name = sbName
	if err := f.Start(ctx, sb); err != nil {
		t.Fatalf("adapter Start (unix socket to mock): %v", err)
	}

	api.mu.Lock()
	actions := append([]string{}, api.actions...)
	api.mu.Unlock()
	if len(actions) != 1 || actions[0] != "InstanceStart" {
		t.Fatalf("adapter Start should produce InstanceStart, got %v", actions)
	}
}
