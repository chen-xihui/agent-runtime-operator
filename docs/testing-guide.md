# 虚拟机环境测试步骤手册

本手册汇总 agent-runtime-operator 在**虚拟机测试环境**的完整测试步骤，基于 192.168.0.31/32 的多次验证实践整理。
适用：在真实 K8S 集群上对 M1~M5 里程碑做端到端验证。

---

## 0. 环境信息速查

| 项 | 值 |
|----|----|
| 虚拟机 1 | 192.168.0.31（K8S master，跑 NATS/Temporal/Operator/Worker） |
| 虚拟机 2 | 192.168.0.32（备用，多集群/worker 节点，可能未开机） |
| 虚拟机 root 密码 | `q1w2e3r4t5` |
| K8S 版本 | v1.23.x（docker 内置 dockershim） |
| 工作区 | `d:/Users/chenxihui/go/agent-runtime-operator` |
| 远程交互 | PowerShell + `Posh-SSH` 模块（`Import-Module Posh-SSH`） |

SSH 连接模板（PowerShell）：
```powershell
$pass = ConvertTo-SecureString 'q1w2e3r4t5' -AsPlainText -Force
$cred = New-Object System.Management.Automation.PSCredential('root', $pass)
$session = New-SSHSession -ComputerName 192.168.0.31 -Credential $cred -AcceptKey
$r = Invoke-SSHCommand -SessionId $session.SessionId -Command "要执行的命令"
$r.Output
Remove-SSHSession -SessionId $session.SessionId | Out-Null
```

传输文件：
```powershell
Set-SCPItem -ComputerName 192.168.0.31 -Credential $cred -Path "本地路径" -Destination "/root/" -Force -AcceptKey
```

---

## 1. 集群准备与修复

> 背景：原集群 v1.23 证书曾全部过期（2024-03-16），需先修复。

### 1.1 检查集群健康
```bash
kubectl get nodes          # master 应 Ready
kubectl get crd | grep agent.runtime   # 应看到 7 个 CRD
```

### 1.2 修复证书过期（如需要）
```bash
# 启用 containerd CRI 插件（若被禁用）
sed -i 's/disabled_plugins = \["cri"\]/disabled_plugins = []/' /etc/containerd/config.toml
systemctl restart containerd

# 续期证书
kubeadm certs renew all
# 重新生成 kubelet 配置 + 重启
kubeadm init phase kubeconfig kubelet
systemctl restart kubelet
```

### 1.3 部署 CRD（若未部署）
```bash
# 从本机传送 config/crd/*.yaml 后：
kubectl apply -f /tmp/crd/
```

---

## 2. 部署依赖服务（NATS + Temporal）

> 虚拟机**无法访问外网**，所有镜像需本机 `docker save` → SCP → 虚拟机 `docker load`。

### 2.1 本机准备镜像
```bash
# 本机拉取
docker pull nats:2.10
docker pull postgres:13
docker pull temporalio/auto-setup:latest
# 导出
docker save nats:2.10 temporalio/auto-setup:latest -o /tmp/agent-runtime/images.tar
docker save postgres:13 -o /tmp/agent-runtime/postgres.tar
# 传送到虚拟机（见环境速查的 SCP 命令）
```

### 2.2 虚拟机加载镜像
```bash
docker load -i /root/images.tar
docker load -i /root/postgres.tar
```

### 2.3 启动 NATS（JetStream）
```bash
docker run -d --name nats -p 4222:4222 -p 8222:8222 nats:2.10 -js -m 8222
docker logs nats | grep "Server is ready"
```

### 2.4 启动 PostgreSQL
```bash
docker run -d --name temporal-postgres \
  -e POSTGRES_USER=temporal -e POSTGRES_PASSWORD=temporal -e POSTGRES_DB=temporal \
  -p 5432:5432 postgres:13
docker exec temporal-postgres pg_isready
```

### 2.5 启动 Temporal（连接 postgres）
```bash
docker run -d --name temporal \
  --link temporal-postgres:postgres \
  -e DB=postgres12_pgx -e DB_PORT=5432 \
  -e POSTGRES_USER=temporal -e POSTGRES_PWD=temporal -e POSTGRES_SEEDS=postgres \
  -p 7233:7233 -p 7234:7234 -p 7235:7235 -p 7239:7239 \
  temporalio/auto-setup:latest
# 验证 7233 端口
timeout 3 bash -c 'echo > /dev/tcp/127.0.0.1/7233' && echo OPEN
```

---

## 3. 构建并部署 Operator + Worker + API Server

### 3.1 本机编译 Linux 版
```powershell
# 项目目录下
$env:CGO_ENABLED=0; $env:GOOS="linux"; $env:GOARCH="amd64"
go build -o /tmp/agent-runtime/operator ./cmd/operator
go build -o /tmp/agent-runtime/worker  ./cmd/worker
go build -o /tmp/agent-runtime/api-server ./cmd/api-server
```

### 3.2 传送到虚拟机 + 启动
```bash
# 先杀旧进程（占用二进制时需先杀）
pkill -9 -f /root/operator; pkill -9 -f /root/worker; pkill -9 -f /root/api-server; sleep 2
# 传送后（chmod +x）

# 启动 Worker（连 Temporal + NATS）——必须用 setsid 保持后台存活！
setsid nohup /root/worker --temporal-address=127.0.0.1:7233 --nats-url=nats://127.0.0.1:4222 > /root/worker.log 2>&1 < /dev/null &
disown

# 启动 Operator（连 Temporal + NATS，启用 WorkflowRun 控制器）
setsid nohup /root/operator --temporal-address=127.0.0.1:7233 --nats-url=nats://127.0.0.1:4222 > /root/operator.log 2>&1 < /dev/null &
disown

# 启动 API Server（连集群 kubeconfig + NATS 审计，暴露 REST API）
# 注意：1) api-server 用 --kubeconfig=/root/.kube/config 连集群（跑在宿主机，非 Pod）
#       2) --addr 用 8090：8080 已被 Operator 的 metrics 服务（--metrics-bind-address=:8080）占用
setsid nohup /root/api-server --addr=:8090 --kubeconfig=/root/.kube/config --nats-url=nats://127.0.0.1:4222 > /root/apiserver.log 2>&1 < /dev/null &
disown

sleep 8
grep -cE "Starting Controller" /root/operator.log   # 期望 4（含 WorkflowRun）
grep -iE "workflowrun controller|event-driven" /root/operator.log  # 期望 enabled
grep -iE "api-server listening|audit store enabled" /root/apiserver.log  # 期望两条
```

> ⚠️ 关键：必须用 `setsid nohup ... < /dev/null &` + `disown`，否则 SSH 断开后进程会被 SIGHUP 杀掉。

---

## 4. 里程碑测试步骤

### 4.1 M1：基础底座（租户/Agent/Sandbox/Pod）

```bash
# 1) 创建租户（自动创建 Namespace + Quota）
cat > /tmp/tenant.yaml <<'EOF'
apiVersion: agent.runtime.io/v1
kind: Tenant
metadata:
  name: tenant-a
spec:
  quota: {maxSandboxes: 10, maxAgents: 20, maxCpu: "8", maxMemory: "16Gi"}
EOF
kubectl apply -f /tmp/tenant.yaml

# 2) 创建 Agent（触发 Sandbox → Pod）
cat > /tmp/agent.yaml <<'EOF'
apiVersion: agent.runtime.io/v1
kind: Agent
metadata: {name: code-reviewer, namespace: tenant-a}
spec:
  image: busybox:1.36
  runtime: {class: ""}    # 空 class → 默认运行时（本集群 dockershim 不支持 RuntimeClass）
  entrypoint: [/bin/sh, -c, sleep infinity]
  mcp: {allowedTools: [], endpoints: []}   # 空数组，不触发 relay
  security: {runAsNonRoot: false, readOnlyRootFS: false}
EOF
kubectl apply -f /tmp/agent.yaml

# 3) 验证状态机
kubectl get tenant tenant-a -o jsonpath='{.status.phase}{"\n"}'    # Active
kubectl get agent -n tenant-a     # Running
kubectl get sandbox -n tenant-a   # Running
kubectl get pods -n tenant-a      # 1/1 Running
```

**验证要点**：
- Agent→Sandbox→Pod 状态机 `Provisioning → Running`
- `mcp.allowedTools` 为空数组时**不应**启用 Event Relay（bug 已修复）

### 4.2 M4：租户安全 + Sandbox Suspend/Resume

```bash
# 1) 验证默认 NetworkPolicy Deny-All + Quota
kubectl get netpol -n tenant-a            # tenant-default-deny
kubectl get resourcequota -n tenant-a    # tenant-quota

# 2) Sandbox Suspend（Running → Suspended）
kubectl patch sandbox sb-code-reviewer -n tenant-a --type=json \
  -p '[{"op":"add","path":"/spec/suspend","value":true}]'
kubectl get sandbox -n tenant-a   # Suspended

# 3) Sandbox Resume（Suspended → Running）
kubectl patch sandbox sb-code-reviewer -n tenant-a --type=json \
  -p '[{"op":"replace","path":"/spec/suspend","value":false}]'
kubectl get sandbox -n tenant-a   # Running
```

**验证要点**：
- NetworkPolicy PodSelector 为空（全选 Deny-All）
- `spec.suspend` 需先确认 CRD 含该字段（bug 已修复）

### 4.3 M2：MCP 工具授权联动

```bash
# 1) 创建 MCPEndpoint（工具连接信息）
kubectl apply -f - <<'EOF'
apiVersion: agent.runtime.io/v1
kind: MCPEndpoint
metadata: {name: mcp-code, namespace: tenant-a}
spec: {address: mcp-code:50051, transport: streamable-http, auth: {type: bearer}}
EOF

# 2) 创建 ToolBinding（授权 Agent 使用工具）
kubectl apply -f - <<'EOF'
apiVersion: agent.runtime.io/v1
kind: ToolBinding
metadata: {name: tb-reviewer, namespace: tenant-a}
spec:
  agentRefs: [code-reviewer]
  tools:
    - name: mcp-code
      dataScope: {tenant: tenant-a}
      rateLimit: {rps: 10}
      redact: [token]
EOF

# 3) 验证无 RBAC 错误（Agent Running 时同步授权到 MCP Registry）
grep -iE "forbidden|failed to sync" /root/operator.log   # 应为空
```

### 4.4 M3：编排引擎（NATS + Temporal 端到端）

> 前置：NATS + Temporal 已部署（见第 2 节），operator 和 worker 已用 `--temporal-address --nats-url` 启动。

```bash
# 1) 创建 Workflow（顺序 DAG）
kubectl apply -f - <<'EOF'
apiVersion: agent.runtime.io/v1
kind: Workflow
metadata: {name: m3-pipeline, namespace: tenant-a}
spec:
  entrypoint: analyze
  nodes:
    - {id: analyze, agent: analyzer, action: analyze_repo}
    - {id: review, agent: reviewer, action: review_code, dependsOn: [analyze]}
EOF

# 2) 创建 WorkflowRun（触发 Temporal 执行）
kubectl apply -f - <<'EOF'
apiVersion: agent.runtime.io/v1
kind: WorkflowRun
metadata: {name: m3-run-1, namespace: tenant-a}
spec:
  workflowRef: m3-pipeline
  input: {tenantId: tenant-a}
EOF

# 3) 等待编排执行（约 10-20s）
sleep 15

# 4) 验证执行启动：runID + workflowID 回写（期望 RUNNING）
kubectl get workflowrun -n tenant-a \
  -o jsonpath='{range .items[*]}phase={.status.phase} runID={.status.runId} wfID={.status.workflowId}{"\n"}{end}'

# 5) Worker 执行日志（拓扑推进 analyze → review）
grep "dispatching node" /root/worker.log          # 期望 analyze、review
grep "orchestration completed" /root/worker.log   # 期望 nodeCount 2

# 6) 事件推进验证（核心：期望最终 SUCCEEDED）
kubectl get workflowrun -n tenant-a \
  -o jsonpath='{range .items[*]}phase={.status.phase} node={.status.currentNode} events={.status.eventsCount} results={.status.nodeResults}{"\n"}{end}'
# 期望：phase=SUCCEEDED, events=4, nodeResults={analyze:{SUCCEEDED}, review:{SUCCEEDED}}
```

**验证要点**：
- WorkflowRun 创建 → `runID`（Temporal RunID）+ `workflowID` 回写（执行启动）
- Worker 按 DAG 依赖拓扑派发节点（analyze 完成 → review）
- **事件推进**：worker 发 `NODE_STARTED` + `NODE_SUCCEEDED` 到 NATS → operator NodeEventProcessor → 更新 status（R-5 快照）
- 全节点终态后判定 `SUCCEEDED`/`FAILED`

**事件推进链路诊断方法**（若 status 不更新）：
```bash
# 编译并传送诊断工具（本机）
# go build -o /tmp/agent-runtime/nats-inspect ./cmd/nats-inspect
nohup /root/nats-inspect --subject="agent-runtime.>" --duration=25s > /root/natsinspect.log 2>&1 &
# 趁监听窗口内触发 WorkflowRun，看能否抓到事件
kubectl apply -f - <<'EOF'
apiVersion: agent.runtime.io/v1
kind: WorkflowRun
metadata: {name: m3-run-dbg, namespace: tenant-a}
spec: {workflowRef: m3-pipeline, input: {tenantId: tenant-a}}
EOF
cat /root/natsinspect.log   # 应显示 4 个事件（analyze/review 各 STARTED+SUCCEEDED）
```

**已知坑点（影响事件推进）**：
1. **NODE_SUCCEEDED 必须发**：worker 的 Dispatch 需同时发 `NODE_SUCCEEDED` 事件到 NATS（仅 NODE_STARTED 不会触发终态判定）
2. **runID 语义**：事件携带的是 **WorkflowID**（`agent-orchestration-*`），而 `status.runId` 是 **Temporal RunID**（`01a0...`），两者不同；需 `status.workflowId` 匹配（已修复）
3. **worker 必须保持运行**：用 `nohup ... &` 直接启动（`setsid nohup` 组合在 SSH 会话可能异常），`StartAsync` + `select{}` 保持 poll

### 4.5 API Server（REST API 端到端）

> 前置：Operator 已启动、已有一个 Active 租户（如 `tenant-api`，`tenant-a` 可能 Terminating）、api-server 已启动（见 3.2，`--addr=:8090`）。api-server 通过 kubeconfig 连集群。

```bash
# 1) 健康检查
curl -s http://127.0.0.1:8090/healthz   # 期望 {"status":"ok"}

# 2) 列出租户（对接 SDK，回读 CRD）
curl -s http://127.0.0.1:8090/api/v1/tenants | head -c 400

# 3) 通过 REST 创建租户（POST /api/v1/tenants）
curl -s -X POST http://127.0.0.1:8090/api/v1/tenants \
  -H 'Content-Type: application/json' \
  -d '{"metadata":{"name":"tenant-api"},"spec":{"quota":{"maxSandboxes":5,"maxAgents":5,"maxCpu":"2","maxMemory":"4Gi"}}}'
kubectl get tenant tenant-api   # 应存在且 Active

# 4) 通过 REST 创建 Agent（触发 Sandbox 调谐）
# 注意：Agent spec.mcp.allowedTools/endpoints 为必填，需给空数组
curl -s -X POST http://127.0.0.1:8090/api/v1/tenants/tenant-api/agents \
  -H 'Content-Type: application/json' \
  -d '{"metadata":{"name":"reviewer"},"spec":{"image":"busybox:1.36","runtime":{"class":""},"entrypoint":["/bin/sh","-c","sleep infinity"],"mcp":{"allowedTools":[],"endpoints":[]},"security":{"runAsNonRoot":false,"readOnlyRootFS":false}}}'
sleep 15
curl -s http://127.0.0.1:8090/api/v1/tenants/tenant-api/sandboxes/sb-reviewer | head -c 500   # phase=Running

# 5) 通过 REST 挂起/恢复 Sandbox
curl -s -X POST http://127.0.0.1:8090/api/v1/tenants/tenant-api/sandboxes/sb-reviewer/suspend
curl -s http://127.0.0.1:8090/api/v1/tenants/tenant-api/sandboxes/sb-reviewer | grep -o '"phase":"[A-Za-z]*"'  # Suspended
curl -s -X POST http://127.0.0.1:8090/api/v1/tenants/tenant-api/sandboxes/sb-reviewer/resume
curl -s http://127.0.0.1:8090/api/v1/tenants/tenant-api/sandboxes/sb-reviewer | grep -o '"phase":"[A-Za-z]*"'  # Running

# 6) 通过 REST 创建 Workflow + 触发 WorkflowRun
curl -s -X POST http://127.0.0.1:8090/api/v1/tenants/tenant-api/workflows \
  -H 'Content-Type: application/json' \
  -d '{"metadata":{"name":"api-wf"},"spec":{"entrypoint":"analyze","nodes":[{"id":"analyze","agent":"analyzer","action":"analyze_repo"},{"id":"review","agent":"reviewer","action":"review_code","dependsOn":["analyze"]}]}}'
# WorkflowRun 创建（API Server 无该端点，用 kubectl 触发，见 4.4）后，经 REST 查询：
curl -s http://127.0.0.1:8090/api/v1/tenants/tenant-api/workflowruns/api-run-1 | head -c 400   # status.phase=SUCCEEDED

# 7) DLP 审计查询（NATS JetStream；需先有审计记录，如 MCP Proxy 工具调用/网络出网）
curl -s "http://127.0.0.1:8090/api/v1/audit?tenant=tenant-api&limit=10"   # 期望 {"records":[...]}（无记录为空数组）
```

**验证要点**：
- `/healthz` 正常；`/api/v1/tenants` 与 `kubectl` 回读一致（SDK 直连 CRD）
- 通过 REST 创建租户/Agent 能触发控制器调谐（与 kubectl apply 等价）
- Sandbox suspend/resume 经 REST 调用等价于 `kubectl patch`
- `--nats-url` 使 `/api/v1/audit` 可查询 JetStream 审计记录（实测：写入 `audit.tenant-api.tool_call` 后按 tenant/action 过滤均命中）
- 不存在的资源经 REST 返回 `404`（错误处理正常）

### 4.6 插件市场（Plugin sample 端到端）

> 前置：operator 已启动且 `plugins.agent.runtime.io` CRD 已 apply（若缺，operator 会崩溃 `failed to wait for plugin caches to sync`）。

```bash
# 0) 确认 CRD 与控制器
kubectl get crd plugins.agent.runtime.io
kubectl get plugin    # 初始为空

# 1) 安装示例插件（code-search, tool, 1.0.0, enabled）
kubectl apply -f /root/plugin_code_search.yaml   # 或 config/samples/plugin_code_search.yaml
sleep 6
kubectl get plugin code-search -o wide   # 期望 TYPE=tool VERSION=1.0.0 STATE=enabled
kubectl get plugin code-search -o jsonpath='{.status.installedVersion}'  # 1.0.0

# 2) 禁用（enabled: false）→ 期望 STATE=disabled
kubectl apply -f /root/plugin_disabled.yaml
sleep 6; kubectl get plugin code-search -o wide   # STATE=disabled

# 3) 重新启用（恢复 sample）→ 期望 STATE=enabled
kubectl apply -f /root/plugin_code_search.yaml
sleep 6; kubectl get plugin code-search -o wide   # STATE=enabled

# 4) 版本升级 1.0.0 → 1.1.0 → 期望 enabled, installedVersion=1.1.0
kubectl apply -f /root/plugin_upgrade.yaml
sleep 6; kubectl get plugin code-search -o wide

# 5) 版本降级 1.1.0 → 0.9.0 → 期望 rejected
kubectl apply -f /root/plugin_downgrade.yaml
sleep 6
kubectl get plugin code-search -o wide          # STATE=failed
kubectl get plugin code-search -o jsonpath='{.status.message}'         # plugin: version conflict
kubectl get plugin code-search -o jsonpath='{.status.installedVersion}' # 保留 1.1.0（实际安装版本）
```

**验证要点**：
- 安装 → `enabled`；禁用 → `disabled`；重新启用 → `enabled`（幂等）
- 版本升级成功；**版本降级被拒**（`plugin: version conflict`），`installedVersion` 反映注册中心**实际安装版本**
- 插件注册中心是**进程内内存**（`plugin.Registry`）：重启 operator 后注册表清空，downgrade 保护仅在单进程生命周期内有效

### 4.7 Firecracker 运行时（协议级验证，无需 KVM 硬件）

> 背景：Firecracker 是微 VM，**硬依赖 `/dev/kvm`**（design-doc 9.1）。无 KVM 的测试机无法跑真实内核，但可验证
> "代码 → Firecracker API 协议（HTTP/unix socket、payload 契约、状态机）"全链路。

```bash
# 方案 A：协议级端到端验证（生产 VMManager 走真实 unix-socket 客户端 + 协议忠实 mock）
# 本机直接运行（无需集群/虚拟机/KVM）：
go test ./internal/runtime/ -run 'TestVMManager_Protocol' -v -count=1
# 期望全部 PASS：
#   TestVMManager_Protocol_UnixSocket    StartVM→payload校验→State=Running→StopVM(SendCtrlAltDel)
#   TestVMManager_Protocol_RejectsBadPayload  非法 machine-config(vcpu=0) 被拒 400
#   TestVMManager_Protocol_AdapterStart  Firecracker 适配器 Start 经 VMManager 命中 mock → InstanceStart

# 稳定性
go test ./internal/runtime/ -run 'TestVMManager_Protocol' -count=5

# KVM 能力检测（真实 KVM 节点上应输出 true）
cat /dev/kvm  # 存在设备即支持
# 代码：KVMEnabled()/Firecracker.KVMOK() 非 KVM 返回 false 且不 panic（优雅降级）
```

**验证要点**：
- 生产 `VMManager`（不注入 apiClient）经 **unix socket 传输** 驱动 `StartVM/State/StopVM`
- 线上 payload 与真实 Firecracker API 契约一致：`/machine-config`（vcpu_count/mem_size_mib）、`/boot-source`（kernel_image_path/boot_args）、`/drives/rootfs`（path_on_host/is_root_device）、`/actions`（InstanceStart/SendCtrlAltDel）
- 状态机：NotStarted → Running（InstanceStart）→ NotStarted（SendCtrlAltDel）
- 协议双向一致：非法 payload 被 mock 拒绝（400）
- **唯一无法模拟**：内核真正引导（需真实 `/dev/kvm` + vmlinux/rootfs 镜像）→ 见 **`docs/firecracker-kvm-deployment.md`（真实 KVM 节点部署清单，方案 B）**

---

## 5. 常见问题排查

| 现象 | 原因 | 解决 |
|------|------|------|
| Pod 创建 `forbidden: violates PodSecurity` | busybox 以 root 运行，namespace 是 restricted | `kubectl label ns <ns> pod-security.kubernetes.io/enforce=privileged --overwrite`（仅测试环境） |
| Pod `CreateContainerConfigError` / runAsNonRoot | 镜像需 root | Agent `security.runAsNonRoot: false` |
| Pod `RuntimeHandler "runc" not supported` | dockershim 不支持 RuntimeClass | Agent `runtime.class: ""`（默认运行时） |
| Operator 启动即退出 | NATS subject 含 `/` 非法 / 订阅失败 | 用修复后的二进制（subject 点分隔） |
| Worker 启动失败（进程无日志） | `setsid nohup` 组合在 SSH 会话异常 | 改用 `nohup ... < /dev/null &` 直接启动 |
| Worker/Operator 进程消失 | SSH 断开 SIGHUP | 用 `nohup ... < /dev/null &`（worker 用 `StartAsync`+`select{}` 保持 poll） |
| Sandbox 卡 Provisioning（relay） | 空数组 `allowedTools: []` 误触发 relay | 用修复后的二进制（`len()>0` 判断） |
| Sandbox `spec.suspend` 无效 | CRD 缺字段 | 更新 `config/crd/agent.runtime.io_sandboxes.yaml` 并 `kubectl apply` |
| WorkflowRun 一直 RUNNING（事件不推进） | worker 只发 NODE_STARTED 未发 NODE_SUCCEEDED | worker Dispatch 同时发 NODE_SUCCEEDED 事件 |
| WorkflowRun 事件被丢弃（events=0） | runID 语义不匹配（事件是 WorkflowID，status.runId 是 Temporal RunID） | 用修复后的二进制（`status.workflowId` 匹配）；`cmd/nats-inspect` 抓包确认 |
| WorkflowRun `spec.workflowId` 字段无效 | CRD 缺字段 | 更新 `config/crd/agent.runtime.io_workflowruns.yaml` 并 `kubectl apply` |
| API Server 启动 `load kubeconfig: ...` | 未传 `--kubeconfig` 且不在集群内 | 加 `--kubeconfig=/root/.kube/config`（本集群 api-server 跑在宿主机，非 Pod） |
| API Server 审计 `/api/v1/audit` 永远空数组 | 未配 `--nats-url`（默认 NoopStore） | 加 `--nats-url=nats://127.0.0.1:4222` 接入 JetStream 审计存储 |
| API Server `bind: address already in use` | 8080 被 Operator metrics 占用 | `--addr=:8090`（Operator 默认 `--metrics-bind-address=:8080`） |
| Operator 启动即崩溃 `failed to wait for plugin caches to sync: timed out ... *v1.Plugin` | 集群缺 Plugin CRD | `kubectl apply -f config/crd/agent.runtime.io_plugins.yaml`（operator 注册了 Plugin 控制器，无 CRD 缓存无法同步） |
| 插件安装后 status 反复变 `failed`，message=`plugin: already exists` | Reconcile 重复触发，`Registry.Install` 对同版本非幂等返回 ErrPluginExists | 用修复后的二进制（controller 将 `ErrPluginExists` 视为幂等成功） |
| Operator/Worker 后台进程随 SSH 断开消失 | `nohup ... &` 在一次性 SSH 命令里仍被 SIGHUP | 用 `systemd-run --unit=ar-xxx <binary> ...` 创建瞬时 systemd 单元（实测可靠） |
| 磁盘不足（本机编译） | C 盘满 | `go clean -cache` 释放空间 |
| 无法访问外网 | 虚拟机内网 | 本机 `docker save` → SCP → `docker load` |

---

## 6. 快速验证清单（回归用）

```bash
# 一个命令跑完 M1+M4 核心回归
kubectl apply -f /tmp/tenant.yaml && sleep 6 \
  && kubectl apply -f /tmp/agent.yaml && sleep 20 \
  && echo "=== tenant/agent/sandbox/pod ===" \
  && kubectl get tenant && kubectl get agent,sandbox,pods -n tenant-a \
  && echo "=== netpol/quota ===" \
  && kubectl get netpol,resourcequota -n tenant-a \
  && echo "=== suspend/resume ===" \
  && kubectl patch sandbox sb-code-reviewer -n tenant-a --type=json -p '[{"op":"add","path":"/spec/suspend","value":true}]' \
  && sleep 5 && kubectl get sandbox -n tenant-a \
  && kubectl patch sandbox sb-code-reviewer -n tenant-a --type=json -p '[{"op":"replace","path":"/spec/suspend","value":false}]' \
  && sleep 5 && kubectl get sandbox -n tenant-a
```

M3 编排回归（需 NATS + Temporal + worker 已启动）：
```bash
# 触发编排并验证事件推进 → SUCCEEDED
kubectl delete workflowrun -n tenant-a --all
kubectl apply -f - <<'EOF'
apiVersion: agent.runtime.io/v1
kind: WorkflowRun
metadata: {name: m3-regress, namespace: tenant-a}
spec: {workflowRef: m3-pipeline, input: {tenantId: tenant-a}}
EOF
sleep 15
kubectl get workflowrun -n tenant-a \
  -o jsonpath='{range .items[*]}phase={.status.phase} node={.status.currentNode} events={.status.eventsCount} results={.status.nodeResults}{"\n"}{end}'
# 期望：phase=SUCCEEDED, events=4（若 phase=RUNNING 且 events=0，按第 4.4 节诊断）
```

---

## 7. 多集群联邦（M5，使用 32 时）

> 虚拟机 32 未开机/未准备，以下为接入后的步骤。

1. 在两台虚拟机分别部署 Operator（各自连本地集群）
2. 用 `federation.Router` 注册对端集群（`cluster-a`/`cluster-b`，配置 `TrustedFrom` 双向信任）
3. A2A Gateway `WithFederator` 启用跨集群委派
4. 本地无目标 Agent 时，`SendTask` 自动跨集群路由（D-4 双向信任校验）

---

## 8. 数据文件位置（虚拟机）

| 文件 | 用途 |
|------|------|
| `/root/operator.log` | Operator 日志 |
| `/root/worker.log` | Worker 日志 |
| `/root/apiserver.log` | API Server 日志 |
| `/root/*.yaml` | 测试清单（tenant/agent/workflow 等） |
| `docker ps` | NATS/Temporal/PostgreSQL 容器 |

---

## 附：测试辅助命令

```bash
# 查看所有 agent.runtime.io 资源
kubectl get tenant,agent,sandbox,workflow,workflowrun,toolbinding,mcpendpoint -A

# 清理租户（注意 Terminating namespace 可能卡住）
kubectl delete tenant <name>
kubectl delete ns <name> --force --grace-period=0

# 查看 NATS 状态
curl -s http://127.0.0.1:8222/jsz | head

# 查看 Temporal 执行
docker exec temporal tctl --namespace default workflow list --all
```
