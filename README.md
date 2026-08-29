# agent-runtime-operator

云原生 AI Agent 编排与沙箱平台（Agent Runtime Operator）。

基于 **Kubernetes + Operator 模式**，提供多租户隔离的 Agent 沙箱、事件驱动的 Multi-Agent 编排，以及 MCP / A2A 协议标准化接入。

> 详细设计见 [`docs/design-doc.md`](docs/design-doc.md) 与 [`docs/core-interface.md`](docs/core-interface.md)。

## 当前状态（M1 ✅ + M2 进行中）

**M1 基础底座（已达成并经真实集群端到端验证）**：

- **M1-a**：Operator + Tenant/Sandbox/Agent CRD 调谐，普通 Pod 版 Agent
- **M1-b**：gVisor RuntimeClass + **Event Relay Sidecar**，Agent 经本地 `unix://` socket 收发事件（P0-2 事件通路）
- **M1 端到端验证** ✅：已在真实 K8S 集群验证 Tenant→Namespace、Agent→Sandbox→Pod（agent+event-relay 两容器）、relay socket 就绪、Sandbox 状态机 `Provisioning→Running`（relayReady=true 前置）

**M2 协议层（核心模块 + 控制器联动已实现，单元/集成测试通过）**：

- **MCP Registry**（`internal/mcp/registry.go`）：工具注册/注销/List，数据级 ABAC 鉴权（P1-4）、限流、脱敏
- **MCP Proxy**（`internal/mcp/proxy.go`）：代理调用（鉴权→数据过滤注入→调用→脱敏→审计），DLP 出网审计（P1-1）
- **A2A Gateway**（`internal/a2a/gateway.go`）：AgentCard 注册/发现、任务委派、消息路由、跨租户默认禁止（D-4）+ 联邦信任
- **NATS 事件总线**（`internal/eventbus/nats.go`）：NATS JetStream 实现，租户隔离 subject（R-3）、事件发布/订阅/投递
- **Agent↔Registry/Gateway 控制器联动**（`internal/registration`）：Agent Running 时自动读取 `ToolBinding`/`MCPEndpoint` CRD 注入工具授权（R-4），并注册 AgentCard 到 A2A Gateway
- **MCP 工具调用端到端**（`internal/mcp/{client,invoker,transport_*}.go`）：MCP Client 转发器（stdio + streamable HTTP 传输），MCP Proxy 经 `MCPInvoker` 转发到真实 Tool Server；已用真实 MCP Server 端到端验证完整调用链（鉴权→数据过滤→转发→脱敏→审计）

**M3 编排引擎（核心已实现，单元测试通过）**：

- **DSL Parser**（`internal/orchestrator/parser.go`）：`WorkflowSpec`→`Graph`（DAG），含环检测（Kahn 拓扑排序）、缺失依赖、入口校验
- **CEL 条件求值器**（`internal/orchestrator/cel.go`）：条件表达式（R-2），编译期静态校验 + 运行时求值，绑定 `nodes`/`input`/`env`
- **Compiler**（`internal/orchestrator/compiler.go`）：Graph→ExecutionData，RetrySpec 退避校验（C1）
- **DAG 引擎**（`internal/orchestrator/engine.go`）：委托 Temporal `GenericOrchestratorWorkflow` 数据驱动执行（ADR-02/P0-1，不自研调度器）
- **WorkflowRun 控制器**（`internal/controllers/workflowrun_controller.go`）：创建时解析 `Workflow`→编译→委托 Temporal 执行，回写 `runID`/`phase`（R-5 只读快照）；取消时调用引擎 Cancel
- **事件驱动节点推进**（`internal/controllers/workflowrun_events.go`）：订阅 NATS 事件总线，节点结果事件幂等推进 `status.nodeResults`/`currentNode`/`eventsCount`（P1-3 去重），全节点终态后判定 `SUCCEEDED`/`FAILED`
- **Temporal 通用编排 Workflow**（`internal/orchestrator/workflow.go` + `activity.go`）：`GenericOrchestratorWorkflow` 按 `ExecutionData` 数据驱动执行 DAG（拓扑推进、重试/补偿、条件节点、Always 节点），`DispatchNodeActivity` 派发节点到 Agent 沙箱；严格遵循 R-1 确定性约束（I/O 全收敛到 Activity，Signal 确定性等待）
- **Human-in-the-loop**（`internal/orchestrator/approval*.go`）：`kind: approval` 节点暂停流程，触发 `AGENT_ASK_HUMAN` 通知外部工单系统，确定性等待审批结果 Signal（通过/拒绝，超时按拒绝处理）；拒绝时非 Always 节点触发运行失败

**M4 强隔离（核心已实现，单元测试通过）**：

- **Sandbox Suspend/Resume 运维**（`internal/controllers/sandbox_controller.go`）：`spec.suspend` 运维意图，Running↔Suspended 状态机迁移（Firecracker 快照 Suspend/Resume 语义）
- **RuntimeAdapter**（`internal/runtime/adapter.go`）：gVisor/Firecracker 适配器，Firecracker 支持快照 Suspend/Resume（`SuspendCapable`，含快照元数据管理），gVisor 降级
- **租户安全加固**（`internal/controllers/tenant_controller.go`）：租户创建默认 NetworkPolicy **Deny-All**（Ingress+Egress），Agent 沙箱经 Event Relay 唯一安全出口
- **DLP 审计存储**（`internal/audit/store.go`）：审计记录（租户/Agent/工具/成功状态），MCP Proxy 全量出网审计落库（P1-1），支持查询过滤

**M5 生产化（可观测性已实现，单元测试通过）**：

- **Metrics**（`internal/metrics/metrics.go`）：编排运行/耗时/事件、沙箱状态迁移/活跃数、工具调用/错误率，接入 controller-runtime `/metrics` 端点（Prometheus）
- **全链路追踪**（`internal/telemetry/trace.go`）：W3C Trace Context（traceparent）生成/解析/子 span 派生，经 eventbus 跨编排引擎/Agent 透传
- **结构化日志**（`internal/telemetry/logging.go`）：租户/Agent 维度索引 + 敏感字段脱敏（token/secret/password/credential）

核心能力：

- **CRD 类型**（`api/v1`）：`Tenant` / `Agent` / `Sandbox` / `Workflow` / `WorkflowRun` / `ToolBinding` / `MCPEndpoint`
- **Operator 控制器**（`cmd/operator` + `internal/controllers`）：
  - `TenantReconciler`：创建租户 Namespace + ResourceQuota
  - `SandboxReconciler`：按状态机（`Pending → Provisioning → Running`）创建/回收沙箱 Pod；**仅 Event Relay 就绪（`relayReady=true`）才进入 `Running`（S2）**
  - `AgentReconciler`：创建 Agent 关联的 Sandbox，回写状态
- **沙箱 Pod 构建**（`internal/sandbox`）：支持普通 Pod（M1-a）与 gVisor RuntimeClass + Event Relay Sidecar 注入（M1-b）；共享 `agent-socket` 卷实现本地 socket 通路
- **Event Relay Sidecar**（`cmd/relay` + `internal/relay`）：本地 `unix://` socket 服务，作为沙箱唯一安全出口，支持事件投递/接收
- **编排引擎接口**（`internal/orchestrator`）：DSL 解析 / DAG / Temporal 委托接口，对应 M3
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
├── cmd/mcp-server/       # MCP Server 示例实现（Tool Server 参考）
├── internal/
│   ├── controllers/      # Reconciler（Tenant/Agent/Sandbox/WorkflowRun）
│   ├── sandbox/          # 沙箱 Pod 构建与生命周期
│   ├── audit/            # DLP 审计存储（工具调用/出网审计）
│   ├── metrics/          # 可观测性指标（Prometheus）
│   ├── telemetry/        # 全链路追踪（W3C traceparent）+ 日志脱敏
│   ├── runtime/          # RuntimeAdapter（gVisor/Firecracker，Suspend/Resume 快照）
│   ├── relay/            # Event Relay 本地 socket 服务（P0-2）
│   ├── orchestrator/     # 编排引擎（DSL Parser / CEL / Compiler / Temporal DAG）
│   ├── eventbus/         # 事件总线（NATS JetStream 实现）
│   ├── mcp/              # MCP Registry / Proxy / Client（stdio+HTTP）
│   ├── a2a/              # A2A Gateway
│   └── registration/     # Agent↔Registry/Gateway 控制器联动
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
| M1 | 基础底座 | ✅ M1-a 普通 Pod；✅ M1-b gVisor RuntimeClass + Event Relay Sidecar（本地 socket 通路，S2 前置）；✅ 真实集群端到端验证 |
| M2 | 协议层 | ✅ MCP Registry/Proxy、A2A Gateway、NATS 事件总线、Agent↔Registry/Gateway 控制器联动、MCP 工具调用端到端（真实 Tool Server 验证） |
| M3 | 编排引擎 | ✅ DSL 解析、CEL 条件、Temporal 委托、WorkflowRun 控制器、事件驱动节点推进、Temporal 通用编排 Workflow（数据驱动 DAG + 重试/补偿）、Human-in-the-loop（approval 节点） |
| M4 | 强隔离 | ✅ RuntimeAdapter（Firecracker 快照 Suspend/Resume）、Sandbox Suspend/Resume 运维、租户默认 NetworkPolicy Deny-All、DLP 审计存储；待：Firecracker 实际 KVM 运行时接入、审计收集到外部存储 |
| M5 | 生产化 | 🔄 Metrics（Prometheus）、全链路追踪（W3C）、结构化日志已实现；待：高可用、多集群联邦、开放 SDK |

## 关键技术选型

Go 1.22+ · Kubernetes Operator（controller-runtime + kubebuilder）· gVisor / Firecracker · NATS JetStream · Temporal · MCP / A2A

## License

Apache License 2.0
