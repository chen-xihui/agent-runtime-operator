# 云原生 AI Agent 编排与沙箱平台 — 设计文档

> 项目代号：**agent-runtime-operator**
> 版本：v0.4（已吸收三轮评审修订 + 一轮评审补修）
> 文档状态：Review
>
> 修订记录：
> - **v0.2**：首轮评审——编排职责收敛（P0-1）、沙箱事件通路（P0-2）、移除托管模式（P0-3）、威胁模型补应用层（P1-1）、快照迁移降级（P1-2）、幂等键（P1-3）、MCP 数据级 ABAC（P1-4）、FaultInjection 移除（P2-2）、state/phase 统一（P2-3）、M1 分步（P2-4）等 12 处。
> - **v0.3**：二轮评审——事件名统一 `NODE_SUCCEEDED/FAILED`（N-1）、DSL 改"通用 Workflow + 数据驱动"（D-1）、3.1 架构图对齐 ADR-02（N-2）、编排/A2A 通道分工（D-2）、A2A 默认禁止跨租户（D-4）、`runID` 闭环（N-3）、冷启动指标标注里程碑（D-3）等 7 处。
> - **v0.4**：三轮评审——Temporal 确定性约束（R-1）、condition 表达式引擎 CEL（R-2）、NATS subject 级 ACL（R-3）、ToolBinding/MCPEndpoint 职责收敛（R-4）、WorkflowRun 低频快照回写（R-5）、沙箱回收策略（R-6）、API 工具注册角色（R-7）等 7 处。
> - **v0.4.1**：评审补修——`WorkflowRunStatus` 状态字段对齐 P2-3 统一为 `phase`（B1）；补齐 `ToolBinding`/`MCPEndpoint` Go 类型（C4）；A2A 能力声明收敛至 `a2a.enabled`（C2）；`EventTrigger.From` 跨租户触发需 FederationPolicy（C3）；`RetrySpec` 校验语义（C1）；`relayReady` 作为 `Provisioning→Running` 前置条件（S2）。
> - **v0.4.2**：二轮评审补修——类型风格统一 `map[string]interface{}`（N1）；明确 `ToolBinding`/`MCPEndpoint` 对象名即引用键（N2）；`GetEvents` 路径与 `GetWorkflowRun` 对齐为 `workflow-runs/{id}/events`（N5）。

---

## 1. 项目背景与目标

### 1.1 背景

随着 AI Agent（智能体）从"单机对话工具"走向"云原生生产服务"，业界面临三大痛点：

1. **运行时不可控**：Agent 执行环境直接跑在宿主机/普通容器内，恶意代码、不可信插件、越权系统调用缺乏隔离，安全风险极高。
2. **编排无标准**：Agent 之间靠硬编码调用，缺乏事件驱动的异步编排能力，难以支撑复杂 Multi-Agent 协作场景。
3. **协议孤岛**：工具接入、Agent 互操作缺乏统一协议，生态封闭、无法跨厂商复用（MCP / A2A / ACP 等标准割裂）。

### 1.2 目标

构建一个**支持多租户隔离的云原生 AI Agent 编排与沙箱平台**，实现：

- 以 **Kubernetes + Operator 模式** 作为 Agent 运行底座，声明式管理 Agent 生命周期。
- 基于 **gVisor / Firecracker** 提供高安全等级的多租户沙箱隔离。
- 设计 **基于事件驱动的 Multi-Agent 编排框架**，支持 DAG / 条件 / 重试 / 补偿等复杂流程。
- 结合 **MCP / A2A 协议** 实现 Agent 间互操作与工具调用标准化。
- 为上层业务（AI Coding、企业工作流、数据工厂）提供统一的 Agent 云底座。

### 1.3 关键指标（SMART）

| 指标 | 目标值 |
|------|--------|
| 单集群租户隔离等级 | 达到主机级安全（gVisor/Firecracker 内核隔离） |
| 沙箱冷启动时间 | < 500ms（M4 起，Firecracker 快照；M1-M3 以普通/gVisor 启动为准） |
| Agent 事件分发吞吐 | ≥ 10k events/s（单编排节点） |
| Agent 编排成功率 | ≥ 99.9%（含重试与补偿） |
| 支持协议 | MCP（工具）、A2A（Agent 互操作）双标准 |
| 多租户数据隔离 | 100% 逻辑+物理双层隔离 |

---

## 2. 技术选型

### 2.1 选型矩阵

| 领域 | 选型 | 备选 | 说明 |
|------|------|------|------|
| 语言 | **Go 1.22+** | Rust | Operator 生态最佳语言，cobra/client-go/controller-runtime 成熟 |
| 集群编排 | **Kubernetes 1.28+** | — | 声明式 API、CRD、调谐循环 |
| Operator 框架 | **controller-runtime + kubebuilder** | operator-sdk | 官方标准，脚手架完善 |
| 沙箱运行时 | **gVisor (runsc) + Firecracker** | Kata Containers | gVisor 用户态内核+Firecracker 微VM 双引擎 |
| 事件总线 | **NATS JetStream** | Kafka | 轻量、支持 JetStream 持久化与分布式流 |
| 工作流编排 | **Temporal**（通用 Workflow + DSL 数据驱动） | Prefect / Argo | 长时运行、确定性重放、补偿 |
| Agent 互操作 | **A2A Protocol (Google)** | ACP | 跨 Agent 发现与任务委派；封装于 `a2a` 模块抽象层后，A2A/ACP 可替换（评审 P2-1） |
| 工具调用 | **MCP (Model Context Protocol)** | 自研插件 | 标准工具接入 |
| 存储 | **etcd（CR 状态） + PostgreSQL（业务元数据）** | — | 分离控制面/数据面 |
| 可观测 | **OpenTelemetry + Prometheus + Grafana** | — | 全链路追踪、指标 |
| 安全 | **OPA/Gatekeeper + Kyverno + NetworkPolicy** | — | 策略即代码 |
| AI Runtime | **vLLM / Ollama（推理） + Agent SDK（LangGraph/自研）** | — | 推理与 Agent 逻辑分离 |

### 2.2 架构决策记录（ADR 摘要）

- **ADR-01**：沙箱运行时采用"双引擎"——gVisor（快速/通用）+ Firecracker（强隔离/不可信负载），通过 RuntimeClass 切换。
- **ADR-02**：编排引擎职责收敛为**单一执行底座**——平台侧声明式 DSL **只负责描述流程**，DSL 解析器（`dsl-compiler`）在创建 WorkflowRun 时把 DSL 编译为**执行参数数据（DAG 描述）**，再由平台**预注册的通用编排 Workflow**（`GenericOrchestratorWorkflow`）按数据解释执行；实际执行、重试、补偿、确定性重放全部交给 Temporal。**平台不自研调度器/状态机**。（修订：吸收评审 P0-1、D-1）
  > **D-1 技术约束**：Temporal Workflow 是启动前预注册的 Go 代码，**无法在运行时动态编译任意 YAML**。故采用"**通用 Workflow + 数据驱动**"模式——DSL 只产出 DAG 数据结构，Worker 中预注册的通用 Workflow 依据数据逐节点驱动（发起 Activity、等待结果事件、触发重试/补偿）。
- **ADR-03**：事件总线统一采用 NATS JetStream，避免 Kafka 重依赖，降低运维成本。
- **ADR-04**：控制面（Operator）与数据面（沙箱）分离，控制面不可直接访问租户业务数据。

---

## 3. 整体架构

### 3.1 逻辑架构分层

```
┌─────────────────────────────────────────────────────────────────────┐
│                         控制面 (Control Plane)                        │
│                                                                      │
│   API Server  ───►  Agent Runtime Operator（controller-runtime）      │
│   (gRPC/REST)          │  Reconciler │ Webhook │ Metric               │
│                        ▼                                             │
│   ┌────────────────────────────────────────────────────────────┐     │
│   │      Multi-Agent 编排引擎 (Orchestrator)                     │     │
│   │      DSL 解析 → DAG 数据 → 委托通用 Temporal Workflow        │     │
│   │      (重试/补偿/状态机 由 Temporal 承担，平台不自研)           │     │
│   └────────────────────────────────────────────────────────────┘     │
└──────────────────────────────────┬──────────────────────────────────┘
                                   │ 调谐 / 事件
┌──────────────────────────────────▼──────────────────────────────────┐
│                          数据面 (Data Plane)                          │
│                                                                      │
│  ┌──────────────┐   ┌──────────────┐   ┌──────────────┐             │
│  │ Tenant-A     │   │ Tenant-B     │   │ Tenant-C     │             │
│  │ Namespace    │   │ Namespace    │   │ Namespace    │             │
│  │ ┌──────────┐ │   │ ┌──────────┐ │   │ ┌──────────┐ │             │
│  │ │ Sandbox  │ │   │ │ Sandbox  │ │   │ │ Sandbox  │ │             │
│  │ │ gVisor/  │ │   │ │ Firecrack│ │   │ │ gVisor   │ │             │
│  │ │ Firecrack│ │   │ │ -er VM   │ │   │ │          │ │             │
│  │ │  跑 Agent│ │   │ │  跑 Agent│ │   │ │  跑 Agent│ │             │
│  │ └──────────┘ │   │ └──────────┘ │   │ └──────────┘ │             │
│  └──────────────┘   └──────────────┘   └──────────────┘             │
└─────────────────────────────────────────────────────────────────────┘
```

### 3.2 核心组件

| 组件 | 职责 |
|------|------|
| **API Server** | 对外提供 Agent/工作流/沙箱的管理 API（gRPC + REST） |
| **Agent Runtime Operator** | CRD 调谐循环，负责 Sandbox、Agent、Workflow、ToolBinding 等资源创建与回收 |
| **Multi-Agent Orchestrator** | 事件驱动编排引擎，解析 DSL、执行 DAG、处理事件与补偿 |
| **Sandbox Controller** | 根据 RuntimeClass 创建 gVisor/Firecracker 沙箱，注入 Sidecar（MCP Proxy + Event Relay） |
| **Event Relay** (Sidecar) | 沙箱内 Agent 与外部事件总线/协议网关的**唯一安全出口**，持有网络凭证、做租户校验与审计 |
| **A2A Gateway** | Agent 注册中心 + 消息路由，实现 Agent-to-Agent 互操作 |
| **MCP Registry** | 工具注册、鉴权、白名单、注入 MCP Client |
| **Event Bus** | NATS JetStream，承载编排事件与 Agent 生命周期事件 |
| **Tenant Manager** | 租户 CRD、配额、RBAC、NetworkPolicy 编排 |

### 3.3 关键数据流

1. **创建 Agent**：API Server → 校验(Webhook) → 写 CRD `Agent` → Operator 调谐 → 创建 RuntimeClass 对应 Sandbox Pod + Sidecar → Agent 注册到 A2A Gateway。
2. **编排执行**：用户提交 `WorkflowRun` → Orchestrator 解析 DSL 生成 DAG 数据 → 以数据启动通用编排 Workflow → 逐节点经 Event Bus 发 `NODE_STARTED` → Sandbox 内 Agent 经 Event Relay 执行 → 回发 `NODE_SUCCEEDED/FAILED` → 推进下一节点。（修订：评审 D-1、N-1）
3. **工具调用**：Agent 内 MCP Client 通过 Sandbox Sidecar 的 MCP Proxy → MCP Registry 鉴权 → 转发到目标 Tool Server → 结果回传。

---

## 4. 核心模块设计

### 4.1 多租户沙箱隔离（Sandbox Controller）

#### 4.1.1 隔离模型

采用**命名空间 + 沙箱运行时 + 网络策略 + 存储隔离**四层模型：

| 隔离维度 | 机制 |
|----------|------|
| 计算隔离 | 每租户独立 Namespace，Pod 绑定专属 ServiceAccount |
| 内核隔离 | gVisor 用户态内核（runsc）拦截系统调用；Firecracker 微VM 提供虚拟化隔离 |
| 网络隔离 | NetworkPolicy 默认 Deny-All，按租户/白名单放行 |
| 存储隔离 | 每租户独立 PVC + 加密，PVC 只能被本租户 Pod 挂载 |
| 资源隔离 | ResourceQuota + LimitRange + PodPriorityClass 分层配额 |
| 密钥隔离 | 每租户独立 Vault 路径 / Secret，注入严格受限 |

#### 4.1.2 运行时选择策略（RuntimeClass）

```yaml
# 场景驱动（注：出于隔离一致性，不提供"托管模式"共享进程方案，避免破坏多租户隔离）
- 场景: 低信任、跑第三方/不可信代码   → runtimeclass: firecracker (微VM)
- 场景: 高吞吐、性能敏感、内部 Agent  → runtimeclass: gvisor (runsc)
- 场景: 纯工具调用、无任意代码执行    → runtimeclass: gvisor (runsc) 精简镜像
```
> **修订（评审 P0-3）**：移除原"进程内/托管模式"，避免多租户共享进程破坏隔离目标。所有场景均通过 RuntimeClass 获得隔离，仅镜像/资源大小不同。

#### 4.1.3 Sandbox 生命周期状态机

```
Pending → Provisioning → Running → (Suspend/Resume) → Terminating → Terminated
                │
                └→ Failed (可重试) → Terminating
```

- **Provisioning**：拉取镜像、创建网络、挂载 PVC、启动 Sidecar。
- **Running**：Agent 进程就绪，可接收事件。
- **Suspend**：Firecracker 使用快照（snapshot）挂起，节省内存。
- **Terminated**：释放资源，保留审计日志与 PVC。

> **修订（评审 S2）**：`Provisioning → Running` 的迁移以 **Event Relay Sidecar 就绪（`relayReady: true`）为前置条件**。Relay 未就绪不得进入 `Running`，避免 Agent 过早启动而缺失安全出口（4.1.4 事件通路）导致事件投递失败或绕过出网审计。

**资源回收策略（修订：评审 R-6）**：
- **空闲回收**：Sandbox 在 `MaxLifetimeMin` 内无任务/无心跳则自动回收（对应 `Agent.spec.security.maxLifetimeMin`）；
- **租户配额上限**：每租户同时存活的 Sandbox 数量受 `Tenant.spec.quota.maxSandboxes` 约束，超限时新 Sandbox 进入 `Pending` 排队；
- **回收优先级**：先回收 `Suspended` 与空闲 `Running`，保障高优任务资源。

#### 4.1.4 沙箱网络与事件通路（修订：评审 P0-2）

> 解决"隔离沙箱内的 Agent 如何安全访问事件总线 / MCP / A2A"的关键问题。

**设计原则：Sandbox 内不直接持有任何外部凭证，所有出网经由本 Pod 的 Sidecar 代理。**

```
┌────────────────────── Sandbox Pod (Tenant-A) ─────────────────────┐
│                                                                   │
│  ┌────────────────┐         unix:///var/run/agent.sock             │
│  │   Agent 容器    │ ──────────────────────────────────┐           │
│  │  (gVisor/FC)   │  仅可通过本地 socket 通信           │           │
│  └────────────────┘                                   ▼           │
│                                              ┌─────────────────┐   │
│                                              │  Event Relay     │   │
│                                              │  + MCP Proxy     │   │
│                                              │  (Sidecar, 可信)  │   │
│                                              │  · 持有 NATS 凭证  │   │
│                                              │  · 租户校验/鉴权   │   │
│                                              │  · 事件签名校验     │   │
│                                              │  · 出网审计/DLP    │   │
│                                              └───────┬─────────┘   │
└──────────────────────────────────────────────────────┼─────────────┘
                                                       │ 双向 TLS + 租户隔离主题
                            ┌──────────────────────────┼──────────────┐
                            ▼                          ▼              ▼
                     [NATS JetStream]          [MCP Registry]   [A2A Gateway]
```

**机制要点：**
1. **本地 socket 隔离**：Agent 通过 `unix://` socket 访问同 Pod 的 Event Relay，**无网络接口、无密钥**。
2. **凭证集中于 Sidecar**：NATS / MCP / A2A 的所有 TLS 与鉴权凭证只存在于可信的 Event Relay，**不注入 Agent 容器**。
3. **事件签名与租户校验**：Relay 校验事件携带的 `tenantId + AgentID` 签名，只允许本租户主题，防事件伪造与跨租户投递。
4. **统一出网代理**：MCP Proxy 与 Event Relay 合一，作为沙箱**唯一 egress 点**，便于做域名白名单、参数脱敏、数据防泄露（DLP）。
5. **审计**：所有进出 Agent 的事件/工具调用在 Relay 落审计日志。

**NATS 层租户隔离（修订：评审 R-3）**：仅依赖"凭证不注入 Agent"还不够——各租户 Relay 连接 NATS 时使用**独立的 user/token + subject 级 ACL**（NATS 原生权限），保证：
- 每租户 Relay 只能**订阅/发布本租户前缀的 subject**（如 `tenant-a.>`）；
- Relay 无法越权访问他租户主题，即使 Relay 凭证泄露，攻击面也限定在本租户；
- 编排控制面使用**平台级管理员凭证**，仅其可访问跨租户控制主题（`control.>`），Agent/Relay 一律无权。

> 此设计同时保障：不可信的 Agent 即使被攻破，也无法直接访问 NATS 凭证、无法伪造他租户事件、无法绕过工具鉴权；且 Relay 侧凭证泄露也不波及他租户（R-3 补充）。

#### 4.1.5 安全加固要点

- 强制 `runAsNonRoot`、`readOnlyRootFilesystem`、`seccomp` 默认拒绝。
- 禁用 privileged 容器与 hostPath（Gatekeeper 强制）。
- Sandbox 内无 `kubelet`、无 Docker socket 暴露。
- 出网统一走 Egress 代理，双向 TLS，域名白名单。
- 运行时逃逸检测（Falco + 容器运行时事件）。

---

### 4.2 事件驱动 Multi-Agent 编排框架

#### 4.2.1 设计理念

- **声明式 DSL**：用户用 YAML 描述流程（节点、依赖、条件、重试），而非编写命令式代码。
- **数据驱动解释执行（非自研调度）**：DSL 在创建 `WorkflowRun` 时被**解析为 DAG 数据**，交给平台**预注册的通用编排 Workflow**（`GenericOrchestratorWorkflow`）解释执行；执行/重试/补偿/确定性重放全部由 Temporal 承担，平台侧不自研调度器与状态机（ADR-02）。避免两套调度逻辑的维护成本与不一致风险。（修订：评审 D-1）
- **事件驱动**：节点执行由事件触发（如 `NODE_STARTED`），节点完成后发事件推进流程，天然异步、可横向扩展。Temporal Activity 负责向沙箱派发任务并等待结果事件。

#### 4.2.2 编排 DSL 示例

```yaml
apiVersion: agent.runtime.io/v1
kind: Workflow
metadata:
  name: code-review-pipeline
  namespace: tenant-a
spec:
  entrypoint: main
  events:
    # 订阅外部事件触发整个工作流
    - on: "repo.commit.pushed"
  nodes:
    - id: analyze
      agent: code-analyzer
      action: analyze_repo
    - id: review
      agent: code-reviewer
      action: review_code
      dependsOn: [analyze]
      retry: { max: 3, backoff: "exponential" }
    - id: comment
      agent: comment-bot
      action: post_comment
      dependsOn: [review]
      condition: "{{ review.result.approved == false }}"
    - id: cleanup
      agent: infra-agent
      action: cleanup_sandbox
      dependsOn: [review]
      always: true   # 无论成败都执行（补偿/清理）
```

**表达式引擎与入口语义（修订：评审 R-2）**：
- **条件表达式**：`condition` 采用 **CEL（Common Expression Language）** 求值，运行于确定性上下文。绑定变量：`nodes.<nodeID>.result`（前一节点结果对象）、`input`（运行输入）、`env`（只读环境信息）。示例 `condition: 'nodes.review.result.approved == false'` 需按 CEL 语法（示例中的 `{{ }}` 仅为占位示意，正式实现用 CEL）。
- **entrypoint**：声明入口节点组。多节点并行起始可用 `entrypoint: [analyze, fetch_context]`；缺省为 `dependsOn` 为空的节点。
- **表达式校验**：DSL 解析阶段对 CEL 做静态编译校验，非法表达式在提交时即报错，不在运行时才失败。
- **超时/重试**：`timeout` 使用 Go duration 语义（如 `"5m"`），由 Temporal 的 workflow/activity timeout 实现。

#### 4.2.3 执行引擎架构

```
                 ┌───────────────────────────────┐
   WorkflowRun ─►│   DSL Parser                  │
                 │   (DSL → DAG 执行数据)         │
                 └──────────────┬────────────────┘
                                │ 以 DAG 数据启动
                 ┌──────────────▼────────────────┐
                 │  Temporal (执行底座)            │
                 │  GenericOrchestratorWorkflow   │
                 │  (预注册，按 DAG 数据驱动)       │
                 │   + Activity(派发/等待结果)      │
                 │  · 重试 / 补偿 / 确定性重放     │
                 └──────────────┬────────────────┘
                                │ Activity 发布/订阅事件
                       ┌────────▼────────┐
                       │  Event Bus       │
                       │  (NATS JetStream)│
                       └────────┬────────┘
             ┌──────────────────┼──────────────────┐
             ▼                  ▼                  ▼
      [Sandbox Agent A]  [Sandbox Agent B]  [Tool Server MCP]
```

> **编排职责边界**：Temporal 负责"何时/如何推进流程"（控制流）——通用 Workflow 按 DAG 数据驱动；NATS 负责"Agent 执行结果如何回传"（数据流）。两者解耦，Activity 只做"派发任务 + 等待/校验结果事件"两件事，逻辑简单、可水平扩展。

> **Temporal 确定性约束（修订：评审 R-1）**：Temporal Workflow 代码必须**确定性**（无随机数、无 `time.Now()`、无直接外部 I/O），否则重放时结果不一致。因此 `GenericOrchestratorWorkflow` 遵循：
> 1. **事件等待只用 Temporal 原生原语**——`workflow.Await` / `workflow.Signal` / `workflow.Sleep`，**禁止在 Workflow 内直接调用 NATS I/O**；
> 2. **I/O 全部收敛到 Activity**——NATS 事件由 Activity 消费并落库，再通过 Signal 通知 Workflow 推进；
> 3. 表达式求值、状态查询等仅用确定性的输入（DAG 数据 + 已落库结果），不依赖运行时环境。

#### 4.2.4 事件模型

核心事件类型：

| 事件 | 含义 |
|------|------|
| `WORKFLOW_STARTED` | 工作流开始 |
| `NODE_STARTED` | 节点进入执行 |
| `NODE_SUCCEEDED` | 节点成功 |
| `NODE_FAILED` | 节点失败（可触发重试） |
| `NODE_SKIPPED` | 条件不满足跳过 |
| `AGENT_ASK_HUMAN` | Agent 需要人工介入 |
| `WORKFLOW_COMPLETED` | 工作流结束 |

事件采用 **CloudEvents 规范** 封装，携带 `id / source / type / subject / data / tracingparent`。

**幂等性设计（修订：评审 P1-3）**：NATS JetStream 默认 at-least-once，因此编排对重复事件必须幂等。

- **幂等键**：`event.ID + runID + nodeID`。
- **去重机制**：Temporal Activity 消费 `NODE_SUCCEEDED` 前，先以幂等键查 Temporal 侧已记录状态，已处理则直接跳过，避免重复推进 DAG。
- **状态推进一致性**：节点结果写入与流程推进放在同一 Activity/事务内完成，防止"事件已发但状态未更新"的不一致。
- **投递模式**：关键控制事件（`NODE_SUCCEEDED / NODE_FAILED`）使用 JetStream 持久化 subject + 消费者 Ack，确保不丢。（修订：评审 N-1，统一事件名）

**WorkflowRun 状态回写（修订：评审 R-5）**：Temporal 是事实状态源，K8s CRD 的 `status` 是**只读视图**，采用**低频快照同步**避免 etcd 写放大：
- 由**专用 `WorkflowRunStatusController`**（非每次事件）监听，在"每节点完成"或"运行结束"时从 Temporal 查询一次进度并更新 `WorkflowRun.status`；
- 高频的节点级中间事件**不写 etcd**，通过 `GetEvents` API 从事件总线/查询服务拉取；
- 一致性策略：`status` 允许短暂落后于 Temporal（准实时），以 CRD 为审计/运维入口，以 Temporal 为准。

#### 4.2.5 人工审批节点（Human-in-the-loop）

支持 `kind: Approval` 节点，暂停流程等待人工通过/拒绝，通过 A2A Gateway 通知外部工单系统。

---

### 4.3 协议层：MCP + A2A

#### 4.3.1 MCP（Model Context Protocol）— 工具接入

- **定位**：Agent ↔ 工具的标准化接口。
- **角色**：Agent 是 MCP **Client**，工具是 MCP **Server**。
- **实现**：
  - 平台内置 **MCP Registry**，管理工具注册、版本、鉴权、调用配额。
  - Sandbox Sidecar 内置 **MCP Proxy**，统一代理出网，做参数脱敏与审计。
  - 支持 `stdio`（进程内）与 `streamable HTTP`（远程）两种传输。

```
[MCP Client in Sandbox] --(unix socket / streamable HTTP)--> [MCP Proxy Sidecar]
        --> [MCP Registry: 白名单 + 数据级 ABAC + 限流] --> [Tool Server MCP]
```

- **数据级授权（修订：评审 P1-4）**：工具鉴权不止"是否能调用"（粗粒度），还引入 **数据范围过滤（ABAC）**。调用时根据「租户上下文 + Agent 角色 + 请求参数」动态注入数据过滤条件，如：*tenant-a 的 Agent 只能查 tenant-a 的订单数据*。采用 OpenFGA / Rego 策略引擎，确保跨租户数据不可达。

```
Authorize(tenantID, agentID, toolName, params)
    └→ 解析策略 → 注入数据过滤条件(scope) → 放行 / 拒绝 / 脱敏
```

#### 4.3.2 A2A（Agent-to-Agent）— Agent 互操作

- **定位**：Agent ↔ Agent 的标准化通信（Google 2025 推出）。
- **核心原语**：
  - `AgentCard`：Agent 能力描述与发现。
  - `Task`：任务委派，支持 `send/taskGet`、`message/send`。
  - `Message`：多轮对话与结果流转。
  - `Artifact`：结构化产物传递（文件、代码、数据）。
- **平台落地**：
  - **A2A Gateway** 作为注册中心与消息路由，Agent 启动时注册 `AgentCard`。
  - 编排节点通过 A2A 委派子任务给其他 Agent，实现 Agent 间互操作。
  - 支持跨集群的 A2A 代理（基于身份证书）。

```
[Agent A] --(A2A send/taskGet)--> [A2A Gateway] --> [Agent B]
              (发现 AgentCard / 路由 / 鉴权 / 追踪)
```

- **通道分工（修订：评审 D-2）**：为避免同一节点两套机制混用，明确职责——
  - **编排引擎 → Agent**：走**事件总线**（`NODE_STARTED` 派发、`NODE_SUCCEEDED/FAILED` 回传），由 Temporal Activity + Event Relay 负责；
  - **Agent 之间协作**（子任务委派、信息共享）走 **A2A**。
  - 即：**编排是"编排者驱动"，A2A 是"Agent 自主协作"**，两者在节点执行层面互不冲突。

- **跨租户边界（修订：评审 D-4）**：**默认禁止跨租户 A2A 调用**，与 ADR-04 多租户隔离目标一致。仅在租户间建立**显式联邦信任关系**（如 `FederationPolicy` 双向授权 + 联合 AgentCard）后才允许跨租户委派，所有跨租户调用强制走审计。

#### 4.3.3 协议协同矩阵

| 交互方向 | 协议 | 典型场景 |
|----------|------|----------|
| Agent → 工具 | MCP | 检索代码库、调用数据库、执行命令 |
| Agent → Agent | A2A | 多 Agent 协作、子任务委派 |
| 用户 → Agent | 自有 API/AG-UI | 对话、任务提交 |
| Agent → 外部服务 | A2A/MCP 网关 | 对接工单、CRM、CI/CD |

---

## 5. 数据模型（CRD 设计）

### 5.1 核心 CRD 一览

| CRD | 用途 |
|-----|------|
| `Tenant` | 租户定义，配额、策略 |
| `Agent` | Agent 定义，绑定镜像、沙箱运行时、MCP 白名单 |
| `Sandbox` | 沙箱实例状态 |
| `Workflow` | 编排 DSL 定义 |
| `WorkflowRun` | 工作流的一次执行实例（含执行状态） |
| `ToolBinding` | **Agent ↔ 工具权限绑定**（哪些 Agent 可用哪些工具 + 数据范围），是权限的唯一来源 |
| `MCPEndpoint` | **工具 Server 连接信息**（端点地址、传输、凭证），只管"工具在哪"，不管权限 |

> **职责收敛（修订：评审 R-4）**：`Agent.spec.mcp` 仅做**引用**（`endpoints` 指向 `MCPEndpoint`，`allowedTools` 指向 `ToolBinding` 中已授权的工具），**不在 Agent CRD 内重复定义权限**，避免三处配置不一致。

### 5.2 `Agent` CRD 示例

```yaml
apiVersion: agent.runtime.io/v1
kind: Agent
metadata:
  name: code-reviewer
  namespace: tenant-a
spec:
  image: registry.internal/agents/code-reviewer:v1.2
  runtime:                      # 沙箱运行时
    class: firecracker          # gvisor | firecracker | kata
    resources: { cpu: "1", memory: "2Gi" }
  entrypoint: ["/agent", "serve"]
  mcp:
    allowedTools: ["code.search", "db.query", "cicd.trigger"]
    endpoints: [mcp-code, mcp-db]      # 引用 MCPEndpoint
  capabilities:
    protocols: [a2a]            # 参与 A2A 互操作
    agentCard: {...}
  a2a:
    tasks: ["review_code", "generate_comment"]
  security:
    runAsNonRoot: true
    networkPolicy: "egress-whitelist"
    envSecrets: [vault://tenant-a/agent-secrets]
status:
  phase: Running              # Pending/Provisioning/Running/Suspended/Terminating（统一用 phase）
  sandboxRef: sandbox-abc123
  lastHeartbeat: "2026-08-27T12:00:00Z"
  mcpConnectedTools: ["code.search", "db.query"]
```

### 5.3 `Sandbox` CRD 状态字段

```
status:
  phase: Running               # Pending/Provisioning/Running/Suspended/Terminating
  runtimeClass: firecracker
  podName: sandbox-abc123
  networkPolicy: tenant-a-default-deny
  mountInfo: { pvc: agent-data, encrypted: true }
  relayReady: true             # Event Relay Sidecar 就绪(见 4.1.4)
  lastTransitionTime: ...
```

> **修订（评审 P2-2）**：移除原 `faultInjection` 字段（无对应章节，语义悬空）。若需混沌/故障演练，将在独立专项文档中设计，不占用核心 CRD。
> **修订（评审 P2-3）**：状态字段统一为 `phase`，Agent/Sandbox 均不再使用 `state` 避免语义重复。

---

## 6. API 设计

### 6.1 对外接口（gRPC + REST）

| 方法 | REST Path | 说明 |
|------|-----------|------|
| CreateAgent | `POST /v1/tenants/{t}/agents` | 创建 Agent |
| GetAgent | `GET /v1/tenants/{t}/agents/{id}` | 查询 Agent |
| ListAgents | `GET /v1/tenants/{t}/agents` | 分页列表 |
| DeleteAgent | `DELETE /v1/tenants/{t}/agents/{id}` | 删除 Agent |
| SubmitWorkflow | `POST /v1/tenants/{t}/workflows` | 提交编排 |
| RunWorkflow | `POST /v1/tenants/{t}/workflow-runs` | 触发执行，返回 `runID`（幂等键组成部分） |
| GetWorkflowRun | `GET /v1/tenants/{t}/workflow-runs/{id}` | 查询执行状态（默认读 CRD 低频快照，准实时；如需精确进度用 `GetEvents` 拉事件流） |
| CancelRun | `POST /v1/tenants/{t}/workflow-runs/{id}/cancel` | 取消执行 |
| GetEvents | `GET /v1/tenants/{t}/workflow-runs/{id}/events` | 拉取执行事件流（与 GetWorkflowRun 路径对齐，N5） |
| RegisterTool | `POST /v1/tools` | 注册 MCP 工具（**平台管理员**全局注册）；租户自注册见 `/v1/tenants/{t}/tools`（修订：评审 R-7） |
| InvokeTool | `POST /v1/tenants/{t}/agents/{id}/invoke` | 手动触发 Agent（按 Agent 可用工具调用） |
| Sandbox ops | `POST /v1/tenants/{t}/sandboxes/{id}/{suspend|resume|snapshot}` | 沙箱运维 |

### 6.2 鉴权与租户校验

- 所有 API 通过 **平台 IAM**（RBAC + 租户上下文）鉴权。
- 请求携带租户身份，Server 侧强制 `tenant` 与 Token 中的 tenant 一致，防止越权。
- 采用 **mTLS** 保护数据面与控制面通信。

---

## 7. 安全设计

### 7.1 威胁模型（Top Threats）

| 威胁 | 缓解措施 |
|------|----------|
| 沙箱逃逸 → 宿主机 | gVisor/Firecracker 内核隔离 + seccomp + AppArmor + 只读根文件系统 |
| 租户间数据泄露 | 每租户独立 Namespace/PVC/密钥，NetworkPolicy 默认 Deny-All |
| 越权调用工具 | MCP Registry 白名单 + 参数脱敏 + 调用审计 |
| 供应链投毒（镜像） | 镜像签名校验（Cosign）+ SBOM + 准入控制 |
| 事件注入/伪造 | 事件签名 + 双向 TLS + 租户隔离的 JetStream 主题 + **事件经 Event Relay 校验（P0-2）** |
| Prompt/数据注入 | Agent 输入清洗、上下文隔离、敏感信息过滤 |
| **Agent 应用层越权行为**（修订：评审 P1-1） | 出网统一走 Relay 做 DLP 与域名白名单；工具调用数据级 ABAC（P1-4）；行为异常检测 |
| **数据被 Agent 批量外发**（修订：评审 P1-1） | 出网数据防泄露（DLP）+ 速率限制 + 敏感字段脱敏 + 全量出网审计 |
| 失控 Agent（资源耗尽） | ResourceQuota + LimitRange + 沙箱超时与自动回收 |

> **修订说明（P1-1）**：原威胁模型只覆盖"内核/系统层逃逸"，现补充"**应用层数据泄露**"与"**Prompt/数据注入导致的越权行为**"两类高发威胁及其数据面缓解。

### 7.2 纵深防御（Defense in Depth）

```
租户边界(RBAC) → 网络层(NetPol/mTLS) → 运行层(沙箱) → 内核层(seccomp) → 数据层(加密) → 审计层(全链路)
```

### 7.3 审计与合规

- 所有 Agent 动作、工具调用、数据访问记录不可篡改审计日志。
- 支持数据留存策略与租户隔离审计（每租户独立审计索引）。

---

## 8. 可观测性

- **Metrics**：Prometheus 采集编排延迟、事件吞吐、沙箱状态、错误率。
- **Tracing**：OpenTelemetry 全链路（W3C traceparent 贯穿事件与 A2A 消息）。
- **Logging**：结构化日志，租户维度索引，敏感信息脱敏。
- **事件回放**：基于 JetStream 的事件历史支持问题排查与流程复现。

---

## 9. 部署与运维

### 9.1 集群拓扑

- 控制面（Operator + API + Orchestrator）与数据面（沙箱节点）分离。
- 沙箱节点标记专用 `taint/toleration`，防止业务 Pod 误入。
- Firecracker 节点需 `/dev/kvm`；gVisor 节点无特殊要求。

### 9.2 安装

```bash
# 1. 安装 CRD 与 Operator
make install && make deploy

# 2. 注册沙箱运行时 (RuntimeClass)
kubectl apply -f config/runtimes/gvisor.yaml
kubectl apply -f config/runtimes/firecracker.yaml

# 3. 部署基础设施 (NATS / PostgreSQL / MCP Registry)
helm install agent-infra charts/agent-infra
```

### 9.3 高可用与弹性

- Operator 多副本 + leader election。
- Orchestrator 无状态 + 事件总线持久化，支持水平扩展。
- 沙箱故障恢复：**首版支持跨节点重新调度（重建式）与同节点 Suspend/Resume**。跨节点 **Firecracker 快照迁移**涉及共享存储 + 内存快照一致性，运维复杂度高，**列为 M4 探索项**，不作为首版承诺。（修订：评审 P1-2）

---

## 10. 项目路线图（Milestone）

| 阶段 | 里程碑 | 交付物 |
|------|--------|--------|
| **M1** | 基础底座 | **分两小步降低风险**：① Operator + Tenant/Sandbox CRD 调谐，先跑通普通 Pod 版 Hello Agent；② 叠加 gVisor RuntimeClass + Event Relay Sidecar 跑通沙箱版（修订：评审 P2-4） |
| **M2** | 协议层 | MCP Registry/Proxy、A2A Gateway、Agent 注册与工具调用 |
| **M3** | 编排引擎 | DSL 解析、DAG 执行、事件驱动、重试/补偿、Human-in-the-loop |
| **M4** | 强隔离 | Firecracker 接入、快照 Suspend/Resume、租户安全加固、审计 |
| **M5** | 生产化 | 高可用、可观测、多集群联邦、开放 SDK 与插件市场 |

---

## 11. 目录结构规划（Go 工程）

```
agent-runtime-operator/
├── api/                    # CRD 类型定义 (v1)
│   └── v1/                 # Tenant, Agent, Sandbox, Workflow...
├── cmd/
│   ├── operator/           # Operator 入口
│   ├── api-server/         # gRPC/REST API Server
│   └── orchestrator/       # 编排引擎入口
├── internal/
│   ├── controllers/        # Reconciler
│   ├── sandbox/            # 沙箱控制器 (gVisor/Firecracker)
│   ├── orchestrator/       # DSL解析 + DAG + 事件驱动
│   ├── mcp/                # MCP Registry / Proxy
│   ├── a2a/                # A2A Gateway
│   ├── eventbus/           # NATS JetStream 封装
│   ├── tenant/             # 租户管理
│   └── security/           # 安全与审计
├── config/
│   ├── crd/                # CRD manifests
│   └── runtimes/           # RuntimeClass / NetPol
├── charts/                 # Helm charts
├── hack/                   # 脚本
└── docs/
```

---

## 12. 风险与对策

| 风险 | 等级 | 对策 |
|------|------|------|
| 沙箱性能损耗（gVisor 系统调用慢） | 高 | 性能敏感场景走 Firecracker；可调 RuntimeClass；I/O 热路径优化 |
| Operator 复杂度高、维护成本大 | 中 | 严格 ADR + 模块化 + 充分单测/集成测试 |
| 协议生态仍在演进（A2A/ACP 竞争） | 中 | 协议抽象层隔离，便于平滑替换 |
| 事件丢失/重复导致流程不一致 | 中 | JetStream 持久化 + `event.ID+runID+nodeID` 幂等键 + Ack 确认 + Temporal 确定性重放 |
| 多租户安全审计要求高 | 高 | 纵深防御 + 全链路审计 + 定期渗透测试 |

---

## 13. 附录：术语表

| 术语 | 说明 |
|------|------|
| Operator | 基于 Kubernetes 控制循环的自动化运维模式 |
| CRD | Custom Resource Definition，自定义资源定义 |
| gVisor | Google 的用户态内核，拦截系统调用提供隔离 |
| Firecracker | AWS 的微虚拟机（MicroVM）运行时 |
| MCP | Model Context Protocol，Agent 与工具交互协议 |
| A2A | Agent-to-Agent Protocol，Agent 间互操作协议 |
| DSL | 领域特定语言，此处为工作流编排声明语言 |
| DAG | 有向无环图，用于描述编排节点依赖关系 |

---

*本文档由 agent-runtime-operator 项目设计产出，欢迎评审与迭代。*
