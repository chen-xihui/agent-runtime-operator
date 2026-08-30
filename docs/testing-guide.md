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

## 3. 构建并部署 Operator + Worker

### 3.1 本机编译 Linux 版
```powershell
# 项目目录下
$env:CGO_ENABLED=0; $env:GOOS="linux"; $env:GOARCH="amd64"
go build -o /tmp/agent-runtime/operator ./cmd/operator
go build -o /tmp/agent-runtime/worker  ./cmd/worker
```

### 3.2 传送到虚拟机 + 启动
```bash
# 先杀旧进程（占用二进制时需先杀）
pkill -9 -f /root/operator; pkill -9 -f /root/worker; sleep 2
# 传送后（chmod +x）

# 启动 Worker（连 Temporal + NATS）——必须用 setsid 保持后台存活！
setsid nohup /root/worker --temporal-address=127.0.0.1:7233 --nats-url=nats://127.0.0.1:4222 > /root/worker.log 2>&1 < /dev/null &
disown

# 启动 Operator（连 Temporal + NATS，启用 WorkflowRun 控制器）
setsid nohup /root/operator --temporal-address=127.0.0.1:7233 --nats-url=nats://127.0.0.1:4222 > /root/operator.log 2>&1 < /dev/null &
disown

sleep 8
grep -cE "Starting Controller" /root/operator.log   # 期望 4（含 WorkflowRun）
grep -iE "workflowrun controller|event-driven" /root/operator.log  # 期望 enabled
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

```bash
# 1) 创建 Workflow
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

# 3) 验证
kubectl get workflowrun -n tenant-a \
  -o jsonpath='{range .items[*]}phase={.status.phase} runID={.status.runId}{"\n"}{end}'
# 期望 RUNNING（Temporal 执行中）

# Worker 执行日志
grep "dispatching node" /root/worker.log    # analyze → review（拓扑推进）
grep "orchestration completed" /root/worker.log  # nodeCount 2

# 4) 事件推进验证（期望最终 SUCCEEDED + nodeResults）
kubectl get workflowrun -n tenant-a -o jsonpath='{.items[0].status}{"\n"}'
```

**验证要点**：
- WorkflowRun 创建 → runID 回写（Temporal 执行启动）
- Worker 按 DAG 依赖拓扑派发节点（analyze 完成 → review）
- 节点事件经 NATS → NodeEventProcessor → 更新 status（R-5 快照）
- 全节点终态后判定 SUCCEEDED/FAILED

---

## 5. 常见问题排查

| 现象 | 原因 | 解决 |
|------|------|------|
| Pod 创建 `forbidden: violates PodSecurity` | busybox 以 root 运行，namespace 是 restricted | `kubectl label ns <ns> pod-security.kubernetes.io/enforce=privileged --overwrite`（仅测试环境） |
| Pod `CreateContainerConfigError` / runAsNonRoot | 镜像需 root | Agent `security.runAsNonRoot: false` |
| Pod `RuntimeHandler "runc" not supported` | dockershim 不支持 RuntimeClass | Agent `runtime.class: ""`（默认运行时） |
| Operator 启动即退出 | NATS subject 含 `/` 非法 / 订阅失败 | 用修复后的二进制（subject 点分隔） |
| Worker/Operator 进程消失 | SSH 断开 SIGHUP | 用 `setsid nohup ... < /dev/null &` + `disown` |
| Sandbox 卡 Provisioning（relay） | 空数组 `allowedTools: []` 误触发 relay | 用修复后的二进制（`len()>0` 判断） |
| Sandbox `spec.suspend` 无效 | CRD 缺字段 | 更新 `config/crd/agent.runtime.io_sandboxes.yaml` 并 `kubectl apply` |
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
