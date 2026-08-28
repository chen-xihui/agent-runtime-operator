# agent-runtime-operator

云原生 AI Agent 编排与沙箱平台（Agent Runtime Operator）。

基于 **Kubernetes + Operator 模式**，提供多租户隔离的 Agent 沙箱、事件驱动的 Multi-Agent 编排，以及 MCP / A2A 协议标准化接入。

> 详细设计见 [`docs/design-doc.md`](docs/design-doc.md) 与 [`docs/core-interface.md`](docs/core-interface.md)。

## 当前状态（M1 基础底座）

已实现 M1 里程碑（评审 P2-4 拆分的两小步）：

- **M1-a**：Operator + Tenant/Sandbox/Agent CRD 调谐，普通 Pod 版 Agent
- **M1-b**：gVisor RuntimeClass + **Event Relay Sidecar**，Agent 经本地 `unix://` socket 收发事件（P0-2 事件通路）
- **M1 端到端验证** ✅：已在真实 K8S 集群验证 Tenant→Namespace、Agent→Sandbox→Pod（agent+event-relay 两容器）、relay socket 就绪、Sandbox 状态机 `Provisioning→Running`（relayReady=true 前置）

核心能力：

- **CRD 类型**（`api/v1`）：`Tenant` / `Agent` / `Sandbox` / `Workflow` / `WorkflowRun` / `ToolBinding` / `MCPEndpoint`
- **Operator 控制器**（`cmd/operator` + `internal/controllers`）：
  - `TenantReconciler`：创建租户 Namespace + ResourceQuota
  - `SandboxReconciler`：按状态机（`Pending → Provisioning → Running`）创建/回收沙箱 Pod；**仅 Event Relay 就绪（`relayReady=true`）才进入 `Running`（S2）**
  - `AgentReconciler`：创建 Agent 关联的 Sandbox，回写状态
- **沙箱 Pod 构建**（`internal/sandbox`）：支持普通 Pod（M1-a）与 gVisor RuntimeClass + Event Relay Sidecar 注入（M1-b）；共享 `agent-socket` 卷实现本地 socket 通路
- **Event Relay Sidecar**（`cmd/relay` + `internal/relay`）：本地 `unix://` socket 服务，作为沙箱唯一安全出口，支持事件投递/接收
- **接口契约**（`internal/{orchestrator,eventbus,mcp,a2a}`）：编排/事件/协议层接口，对应 M2/M3 里程碑
- **部署清单**（`config/`）：CRD、RuntimeClass、Manager、RBAC、Samples

## 快速开始

### 1. 本地编译

```bash
make build        # 编译 ./bin/operator 与 ./bin/event-relay
make vet          # go vet
make test         # 单元测试（含 relay socket 通路测试）
```

### 2. 部署到集群

```bash
# 安装 CRD 与 Operator（控制面）
kubectl apply -k config/default

# 注册沙箱运行时 RuntimeClass
kubectl apply -f config/runtimes/gvisor.yaml
kubectl apply -f config/runtimes/firecracker.yaml
```

### 3. 创建租户与 Agent（M1-a 验证）

```bash
# 创建租户（自动创建 Namespace + 配额）
kubectl apply -f config/samples/tenant_a.yaml

# 创建 gVisor 沙箱 Agent，观察调谐日志
kubectl apply -f config/samples/agent_gvisor.yaml
kubectl get agents -n tenant-a
kubectl get sandboxes -n tenant-a
kubectl get pods -n tenant-a
```

## 目录结构

```
agent-runtime-operator/
├── api/v1/               # CRD Go 类型定义
├── cmd/operator/         # Operator 主入口
├── cmd/relay/            # Event Relay Sidecar 入口（M1-b）
├── internal/
│   ├── controllers/      # Reconciler（Tenant/Agent/Sandbox）
│   ├── sandbox/          # 沙箱 Pod 构建与生命周期
│   ├── relay/            # Event Relay 本地 socket 服务（P0-2）
│   ├── orchestrator/     # 编排引擎接口（DSL → DAG → Temporal）
│   ├── eventbus/         # 事件总线接口（NATS JetStream）
│   ├── mcp/              # MCP Registry / Proxy 接口
│   └── a2a/              # A2A Gateway 接口
├── config/
│   ├── crd/              # CRD manifests
│   ├── runtimes/         # RuntimeClass / NetPol
│   ├── manager/          # Operator Deployment
│   ├── rbac/             # RBAC
│   ├── default/          # Kustomize 汇总
│   └── samples/          # 示例清单
├── docs/                 # 设计文档
├── Makefile
└── Dockerfile
```

## 里程碑（Roadmap）

| 阶段 | 里程碑 | 交付物 |
|------|--------|--------|
| M1 | 基础底座 | ✅ M1-a 普通 Pod；✅ M1-b gVisor RuntimeClass + Event Relay Sidecar（本地 socket 通路，S2 前置） |
| M2 | 协议层 | MCP Registry/Proxy、A2A Gateway、Agent 注册与工具调用 |
| M3 | 编排引擎 | DSL 解析、DAG 执行、事件驱动、重试/补偿、Human-in-the-loop |
| M4 | 强隔离 | Firecracker 接入、快照 Suspend/Resume、租户安全加固、审计 |
| M5 | 生产化 | 高可用、可观测、多集群联邦、SDK 与插件市场 |

## 关键技术选型

Go 1.22+ · Kubernetes Operator（controller-runtime + kubebuilder）· gVisor / Firecracker · NATS JetStream · Temporal · MCP / A2A

## License

Apache License 2.0
