# Firecracker 真实 KVM 节点部署清单（方案 B）

> 背景：Firecracker 是微虚拟机（MicroVM），**硬性依赖 `/dev/kvm`**（design-doc 9.1）。
> 当前测试虚拟机（192.168.0.31）无 KVM，故本清单用于**具备 KVM 的真实节点**（物理机，
> 或宿主机开启嵌套虚拟化 `CPU 模式=host/passthrough` 后的 VM）。
>
> 对照方案 A（协议级 mock 验证，无需硬件），本清单完成"内核真正引导"这最后一段，
> 与方案 A 共同构成 Firecracker 运行时完整验证。

---

## 0. 前置条件检查（Hard Gate）

在 KVM 节点上先做以下检查，**任一不满足则停止**：

```bash
# 1) KVM 设备存在（Firecracker 硬依赖）
ls -la /dev/kvm                          # 必须存在字符设备
test -c /dev/kvm && echo "KVM OK" || echo "KVM MISSING"

# 2) CPU 虚拟化标志
grep -oE 'vmx|svm' /proc/cpuinfo | sort -u    # 需有 vmx(Intel) 或 svm(AMD)

# 3) KVM 内核模块已加载
lsmod | grep -E 'kvm(_intel|_amd)?'

# 4) 内核版本建议 >= 5.x（Firecracker 对老内核支持有限）
uname -r

# 5) 安装 firecracker 二进制（Amazon 提供静态二进制）
# 官方下载：https://github.com/firecracker-microvm/firecracker/releases
# 以 1.x 为例：
curl -fsSL -o /usr/local/bin/firecracker \
  https://github.com/firecracker-microvm/firecracker/releases/download/v1.8.0/firecracker-v1.8.0-x86_64
chmod +x /usr/local/bin/firecracker
firecracker --version
```

---

## 1. 准备微内核与根文件系统

`VMManager.Start` 默认使用（`internal/runtime/firecracker.go`）：
- 内核：`/opt/vmlinux`
- 根盘：`/opt/rootfs.ext4`（`Drive{DriveID:"rootfs", IsRootDevice:true}`）
- boot args：`console=ttyS0 reboot=k panic=1 pci=off`（`BuildBootSource`）

```bash
# 微内核（Firecracker 官方内核或任意支持微VM的 Linux 内核）
# 官方预编译内核：
curl -fsSL -o /opt/vmlinux https://s3.amazonaws.com/spec.ccfc.min/img/quickstart-guide/kernel/hello-vmlinux.bin

# 根文件系统（可直接用官方 hello 镜像，或自建 ext4）
curl -fsSL -o /opt/rootfs.ext4 https://s3.amazonaws.com/spec.ccfc.min/img/quickstart-guide/rootfs/hello-rootfs.ext4

# 校验文件就绪
ls -la /opt/vmlinux /opt/rootfs.ext4
file /opt/vmlinux /opt/rootfs.ext4
```

> 若自建 rootfs：用 `dd + mkfs.ext4` 制作空盘，装入 agent 二进制与静态依赖。

---

## 2. 准备运行时（containerd + Kata/Firecracker shim）

设计文档 4.1.2 用 `RuntimeClass handler: kata-fc` 承载 Firecracker 微 VM Pod。两种落地方式：

### 方式 A（推荐）：Kata Containers（`kata-fc` handler，Firecracker 后端）
```bash
# 安装 kata-containers（含 kata-fc runtime class）
cat > /opt/kata/share/default/kata-containers/configuration-fc.toml <<'EOF'
[hypervisor.firecracker]
path = "/usr/local/bin/firecracker"
kernel = "/opt/vmlinux"
EOF

# 在 containerd 注册 kata-fc（/etc/containerd/config.toml）
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata-fc]
  runtime_type = "io.containerd.kata.v2"
  privileged_without_host_devices = true

# 重启 containerd
systemctl restart containerd
```

### 方式 B：直接由平台 `VMManager` 管理微 VM（快照 Suspend/Resume 走 Firecracker API）
此路径即项目已实现的 `internal/runtime/firecracker.go`：平台侧 `VMManager` 通过
Firecracker API unix socket（`/tmp/firecracker/<sandbox>.sock`）管理微 VM 生命周期。
- 需在节点上为每个沙箱创建 socket 目录：`mkdir -p /tmp/firecracker && chmod 777 /tmp/firecracker`
- Operator 需以 root 或有 `/dev/kvm` 访问权限的身份运行

---

## 3. 部署 RuntimeClass + 节点标记

```bash
# 打节点标签（RuntimeClass scheduling.nodeSelector 匹配）
kubectl label node <KVM-NODE> agent.runtime.io/runtime=firecracker
kubectl label node <KVM-NODE> agent.runtime.io/kvm=true

# 若配置了 toleration（config/runtimes/firecracker.yaml 有 NoSchedule 容忍）
kubectl taint nodes <KVM-NODE> agent.runtime.io/firecracker=:NoSchedule

# 应用 RuntimeClass
kubectl apply -f config/runtimes/firecracker.yaml
kubectl apply -f config/runtimes/gvisor.yaml

# 验证
kubectl get runtimeclass firecracker -o yaml | grep -E 'handler|nodeSelector|tolerations'
```

---

## 4. 部署平台（CRD + Operator）

```bash
# 安装全部 CRD
make install   # 等价 kubectl apply -f config/crd

# 部署 Operator（进程式，本仓库验证环境做法）
# 确保 operator 以 root 运行（访问 /dev/kvm、/tmp/firecracker、Firecracker API）
nohup /root/operator --temporal-address=127.0.0.1:7233 --nats-url=nats://127.0.0.1:4222 \
  > /root/operator.log 2>&1 < /dev/null &

# 验证控制器就绪（含 sandbox controller）
grep -cE "Starting Controller" /root/operator.log   # 期望 5（sandbox/agent/tenant/workflowrun/plugin）
```

---

## 5. 端到端验证（Firecracker Sandbox 全链路）

```bash
# 1) 创建租户
kubectl apply -f config/samples/tenant_a.yaml
kubectl get tenant tenant-a   # 期望 Active

# 2) 创建 runtime.class=firecracker 的 Agent（runtime.class 触发 RuntimeClass 调度到 KVM 节点）
kubectl apply -f - <<'EOF'
apiVersion: agent.runtime.io/v1
kind: Agent
metadata:
  name: fc-reviewer
  namespace: tenant-a
spec:
  image: busybox:1.36
  runtime:
    class: firecracker          # ★ 关键：Firecracker 微 VM
    resources: { cpu: "1", memory: "128Mi" }
  entrypoint: ["/bin/sh", "-c", "sleep infinity"]
  mcp: { allowedTools: [], endpoints: [] }
  security: { runAsNonRoot: false, readOnlyRootFS: false }
EOF

# 3) 观察 Sandbox 状态机（重点：Provisioning → Running）
kubectl get sandbox -n tenant-a -w
kubectl get sandbox sb-fc-reviewer -n tenant-a -o jsonpath='{.status.phase}{" "}{.status.runtimeClass}{"\n"}'
# 期望：Running firecracker

# 4) 确认 Pod 调度到 KVM 节点、以微 VM 运行
kubectl get pod -n tenant-a -o wide   # NODE 应为 KVM 节点
kubectl describe pod -n tenant-a | grep -E 'RuntimeClass|QoS|Node:'

# 5) 确认 Firecracker 微 VM 进程与 API socket
ps aux | grep firecracker | grep -v grep        # 应有 firecracker 进程（PID）
ls -la /tmp/firecracker/*.sock                   # 应有沙箱 socket

# 6) KVM 能力检测（operator 日志应通过）
# 代码路径：Firecracker.KVMOK() → KVMEnabled()（/dev/kvm 检查）
grep -i kvm /root/operator.log | tail -5

# 7) Suspend/Resume（快照运维，M4 核心）
kubectl patch sandbox sb-fc-reviewer -n tenant-a --type merge -p '{"spec":{"suspend":true}}'
kubectl get sandbox sb-fc-reviewer -n tenant-a -o jsonpath='{.status.phase}'   # Suspended
kubectl patch sandbox sb-fc-reviewer -n tenant-a --type merge -p '{"spec":{"suspend":false}}'
kubectl get sandbox sb-fc-reviewer -n tenant-a -o jsonpath='{.status.phase}'   # Running
```

---

## 6. 验证要点汇总

| 项 | 期望 | 说明 |
|----|------|------|
| `/dev/kvm` | 存在字符设备 | Firecracker 硬前置 |
| firecracker 二进制 | `firecracker --version` 可用 | 1.x 静态二进制 |
| `/opt/vmlinux` + `/opt/rootfs.ext4` | 文件存在 | 微内核与根盘 |
| RuntimeClass `firecracker` | handler/kata-fc + nodeSelector | 调度到 KVM 节点 |
| Sandbox 状态机 | Provisioning → **Running** | runtimeClass=firecracker |
| Pod 调度 | NODE=KVM 节点 | nodeSelector 生效 |
| firecracker 进程 + socket | 存在 | 微 VM 真正启动 |
| Suspend/Resume | Suspended ↔ Running | Firecracker 快照运维 |
| `KVMEnabled()` | true | operator 侧能力门控 |

---

## 7. 故障排查

| 现象 | 原因 | 解决 |
|------|------|------|
| Pod Pending | RuntimeClass 无匹配节点 | 检查节点标签 `agent.runtime.io/runtime=firecracker` + taint/toleration |
| Pod 报 `RuntimeHandler not supported` | containerd 未注册 kata-fc | 补 containerd runtime 配置并重启 |
| Pod 报 `/dev/kvm` 权限 | kubelet 未挂载 /dev/kvm | kata 配置 `privileged_without_host_devices=true` 或 DevicePlugin 暴露 |
| Sandbox 卡 Provisioning | VMManager 无法访问 Firecracker API socket | 确认 `/tmp/firecracker` 存在且 operator 有写权限 |
| `firecracker` 启动失败 | 内核/rootfs 不匹配 | 换官方 hello 镜像，检查 boot args |
| Suspend 后 Resume 报错 | 无快照 | 先用 Suspend 生成快照再 Resume（代码已强制） |
| 老内核不支持 | Firecracker 需要较新内核特性 | 升内核 >= 5.x 或换 Firecracker 兼容镜像 |

---

## 8. 与方案 A 的关系

| | 方案 A（已完成） | 方案 B（本清单） |
|--|------------------|------------------|
| 目标 | 验证"代码 → Firecracker API 协议"链路 | 验证"内核真正引导 + 节点集成" |
| 硬件 | 无需 KVM | 需真实 `/dev/kvm` |
| 验证内容 | unix socket 传输、payload 契约、状态机 | 微 VM 启动、调度、Suspend/Resume 真实快照 |
| 复用 | 生产 `VMManager` 代码 | 同一代码 + 真实 Firecracker 二进制 |

> 两者共用同一套生产代码（`internal/runtime/firecracker.go`），方案 A 先行验证协议正确性，
> 方案 B 在此之上完成真实 KVM 引导。**方案 A 全绿后，方案 B 的失败点只可能来自节点环境/镜像，而非协议代码。**
