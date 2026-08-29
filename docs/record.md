已完成内容
1. Go 工程脚手架（go.mod，Go 1.22 + controller-runtime v0.18.5 + k8s v0.30.1，兼容本地 Go 1.22）

2. 核心 CRD Go 类型（api/v1/）

Tenant / Agent / Sandbox / Workflow / WorkflowRun / ToolBinding / MCPEndpoint
遵循设计约束：统一 phase 状态字段（P2-3）、ToolBinding/MCPEndpoint 职责收敛（R-4）、A2A 能力以 a2a.enabled 为准（C2）
手写 zz_generated.deepcopy.go 满足 controller-runtime 深拷贝需求
3. Operator 主入口（cmd/operator/main.go）— 注册 scheme、启动 Manager、健康探针

4-5. 控制器（internal/controllers/）

TenantReconciler：创建租户 Namespace + ResourceQuota
SandboxReconciler：按状态机 Pending → Provisioning → Running 调谐，含 Relay 就绪前置（S2）
AgentReconciler：自动创建关联 Sandbox 并回写状态
internal/sandbox/：构建沙箱 Pod，支持 gVisor RuntimeClass 与 Event Relay Sidecar 注入（M1-b）
6. 部署清单（config/）— 7 个 CRD、gVisor/Firecracker RuntimeClass、Manager、RBAC、Samples、Kustomize

7. 构建脚本（Makefile、Dockerfile、.gitignore）

接口契约（internal/{orchestrator,eventbus,mcp,a2a}）— 预置编排/事件/协议层接口，对应 M2/M3

验证结果
go build ./... ✅
go vet ./... ✅
go build -o bin/operator.exe ./cmd/operator ✅
下一步建议
M1-a 验证需真实 Kubernetes 集群（kubectl apply -k config/default 后创建 tenant_a + agent_gvisor）
后续可进入 M1-b（Event Relay Sidecar 真实就绪探测）与 M2（MCP/A2A 协议层实现）


M1-b（Event Relay Sidecar 真实就绪探测）已全部完成并通过验证。

本次完成的改进
1. Event Relay Sidecar 真实实现（cmd/relay + internal/relay）

本地 unix:// socket 服务，作为沙箱唯一安全出口（P0-2）
支持 DeliverToAgent（投递事件给 Agent）与 ReceiveFromAgent（接收 Agent 上报事件）
Agent 仅通过本地 socket 通信，不持有外部凭证
2. Relay 真实就绪探测（internal/sandbox/controller.go）

EnsurePod 现在返回 PodState{PodReady, RelayReady}，通过检查 Pod 内 event-relay 容器的 Ready 状态判断 relay 就绪（不再用"Pod Ready 即视为 relay 就绪"的占位）
Relay 容器就绪探测：test -S /var/run/agent.sock（socket 文件存在即就绪）
3. 状态机正确性（S2）（internal/controllers/sandbox_controller.go）

Provisioning → Running 现在以 relayReady=true 为前置条件
Running 阶段若 relay 掉线则回退 Provisioning 重新编排
4. Pod 构建完善（internal/sandbox/pod.go）

Agent 与 Relay 容器共享 agent-socket 卷（本地 socket 通路）
使用常量统一容器名/socket 路径
5. 单元测试（relay_test.go + pod_test.go）

验证 relay socket 事件投递/接收通路
验证普通 Pod（无 relay）与带 relay 的 Pod 构建正确性
验证结果
go build ./... ✅
go vet ./... ✅
go test ./... ✅（relay 0.993s、sandbox 0.500s 通过）
下一步建议
M1-b 端到端验证需真实集群：构建 relay 镜像并推送到 registry，再创建启用 A2A/MCP 的 Agent 观察 relay 就绪与状态机迁移
之后可进入 M2 协议层：实现 MCP Registry/Proxy 与 A2A Gateway（当前已有接口契约，下一步是 NATS 接入 + 具体实现）


M1-b 端到端验证（虚拟机 192.168.0.31 K8S 集群）✅

集群修复（老集群 v1.23 证书全过期）
- 启用 containerd CRI 插件（disabled_plugins=["cri"] → []）
- kubeadm certs renew all 续期证书
- 重新生成 kubelet.conf（kubeadm init phase kubeconfig kubelet）+ 修复 bootstrap-kubelet.conf
- 验证控制面恢复：master Ready，kubectl 正常

部署
- 部署全部 7 个 CRD
- 构建 relay 镜像（busybox + 静态 relay 二进制），本机 docker save → scp → 虚拟机 docker load
- operator 以进程运行（--enable-relay --relay-image=agent-runtime/event-relay:latest）

端到端验证结果
- Tenant → Namespace（tenant-a）✅
- Agent → Sandbox（sb-code-reviewer）✅
- Sandbox → Pod（agent + event-relay 两容器，共享 agent-socket 卷）✅
- Event Relay 监听 /var/run/agent.sock ✅
- Sandbox 状态机：Provisioning → Running，relayReady=true（S2 前置）✅
- Agent status 回写 Running ✅

验证中发现并修复的产品缺陷
1. Scheme 未注册类型：groupversion_info.go 缺 SchemeBuilder.Register → "no kind is registered"
2. buildResourceQuota nil map panic：Hard map 未初始化
3. runAsNonRoot 未传递：Agent.spec.security → Sandbox.spec.runAsNonRoot（新增字段+CRD）
4. relay 注入条件：改为 sb.Spec.EnableRelay && cfg.EnableRelay（per-sandbox 权威）
5. agentCommand：entrypoint 完整命令时不再追加多余 Args
6. ImagePullPolicy：relay/agent 容器设 IfNotPresent（避免 latest tag 强制拉取）
7. restrictedSecurityContext：runAsNonRoot 可由 spec 覆盖（不再硬编码 true）
8. 验证环境注意：本集群 dockershim 不支持 RuntimeClass；PodSecurity restricted 与 root 镜像冲突（验证时降级）


M2 协议层（核心模块实现）🔄

已实现
- MCP Registry（internal/mcp/registry.go）：工具注册/注销/List，数据级 ABAC 鉴权（P1-4）、限流、脱敏
- MCP Proxy（internal/mcp/proxy.go）：鉴权→数据过滤注入→调用→脱敏→审计（DLP，P1-1）
- A2A Gateway（internal/a2a/gateway.go）：AgentCard 注册/发现、任务委派、消息路由、跨租户默认禁止（D-4）+ 联邦信任
- NATS 事件总线（internal/eventbus/nats.go）：NATS JetStream，租户隔离 subject（R-3）、发布/订阅/投递

验证
- go build ./... ✅ / go vet ./... ✅
- 单元测试：mcp（registry+proxy）、a2a（gateway）✅
- NATS 集成测试（本机 docker 起 nats:2.10）：PublishSubscribe、TenantIsolation（R-3）、JetStream ✅

Agent↔Registry/Gateway 控制器联动 ✅

已实现（internal/registration/sync.go）
- SyncAgentTools：读取 ToolBinding/MCPEndpoint CRD，注入工具授权到 MCP Registry
  - ToolBinding.AgentRefs 过滤（Agent 级 vs 租户级，R-4）
  - 同步工具描述（endpoint/auth/rateLimit/redact）
- RegisterAgentCard：Agent Running 时注册 AgentCard 到 A2A Gateway（仅 A2A enabled，C2）
- AgentReconciler 集成：Sandbox Running 时触发 syncRegistration；依赖注入（Syncer）经 main.go
- 单元测试：ToolBinding 授权注入、AgentRefs 过滤、AgentCard 注册（仅 A2A 启用时）✅

MCP 工具调用端到端 ✅

已实现
- MCP Client（internal/mcp/client.go）：JSON-RPC 2.0 + MCP 协议（initialize / tools/call），解析结构化结果
- stdio 传输（transport_stdio.go）：启动 MCP Server 子进程，stdin/stdout 通信
- streamable HTTP 传输（transport_http.go）：远程 MCP Server（含 SSE 响应解析）
- MCPInvoker（invoker.go）：按 Transport 选择传输并缓存客户端，作为 Proxy 底层调用器
- MCP Server 示例（cmd/mcp-server）：stdio 传输，暴露 echo / get_weather 工具

端到端验证（真实 MCP Server）
- TestE2E_ProxyToMCPServer：Proxy → MCPInvoker → 真实 MCP Server（stdio）
  验证：注册→鉴权→数据过滤注入→MCP 协议转发→脱敏→审计 完整链路 ✅
- TestE2E_MCPServer_Echo：stdio 传输握手 + tools/call ✅

M3 编排引擎（核心实现）🔄

已实现（internal/orchestrator）
- DSL Parser（parser.go）：WorkflowSpec→Graph（DAG）
  - 环检测（Kahn 拓扑排序）、缺失依赖、重复节点、入口校验
  - 节点 timeout（Go duration）、RetrySpec 解析
- CEL 条件求值器（cel.go）：R-2 条件表达式
  - 编译期静态校验（非法表达式提交即报错）
  - 运行时求值，绑定 nodes.<id>.result / input / env
- Compiler（compiler.go）：Graph→ExecutionData
  - C1 RetrySpec.backoff 校验（none|fixed|exponential，fixed/exp 需 max>=1）
- DAG 引擎（engine.go）：Temporal 委托
  - GenericOrchestratorWorkflow 数据驱动执行（ADR-02/P0-1，不自研调度器）
  - Execute（数据校验→启动 Workflow）/ Cancel / OnEvent（幂等推进占位）

验证
- 单元测试：Parser（Valid/重复/缺失依赖/环/入口/timeout）、Compiler（Compile/backoff）、CEL（Compile/Eval/input/非bool）、Engine（nil client/空数据/接口）✅
- 全部 build / vet / test 通过 ✅

WorkflowRun 控制器 ✅

已实现（internal/controllers/workflowrun_controller.go）
- 创建时：解析引用的 Workflow CR → Parser 解析+校验 → Compiler 编译 → Temporal 引擎 Execute → 回写 runID/phase（R-5）
- 状态回写（R-5 只读低频快照）：RUNNING/SUCCEEDED/FAILED/CANCELLED + runID/currentNode/error
- 取消：标记 CANCELLED 或 DeletionTimestamp 时调用引擎 Cancel
- 依赖注入：Parser/Compiler/DAGEngine（可测试）
- main.go：--temporal-address flag 可选注册（无 Temporal 时禁用 WorkflowRun 控制器）
- 单元测试（fake client + status subresource + mock engine）：
  StartSuccess / WorkflowNotFound / CompileError（环）/ ExecuteError / Cancel ✅

事件驱动节点推进 ✅

已实现（internal/controllers/workflowrun_events.go）
- NodeEventProcessor：订阅 NATS 事件总线，处理节点结果事件
  - 幂等去重（P1-3）：seenEvents 记录已处理事件 ID，at-least-once 不重复推进
  - subject 约定 <runID>/<nodeID> 解析节点
  - 更新 status.nodeResults / currentNode / eventsCount
  - 完成判定：有 FAILED 节点 → FAILED；全 SUCCEEDED → SUCCEEDED
- main.go：--nats-url flag 订阅事件总线转发给 NodeEventProcessor（与 WorkflowRun 控制器联动）
- 单元测试：Progress（状态推进+完成判定）/ Idempotency（去重）/ Failure（FAILED）/ UnknownRunID / ParseNodeSubject ✅

Temporal 通用编排 Workflow ✅

已实现（internal/orchestrator/workflow.go + activity.go）
- GenericOrchestratorWorkflow：按 ExecutionData 数据驱动执行 DAG
  - 依赖拓扑推进（pendingDeps + fanOut，顺序执行就绪队列）
  - 节点派发（DispatchNodeActivity Activity，I/O 收敛到 Activity，R-1）
  - 节点结果 Signal 确定性等待（resultCh.Receive，R-1）
  - 重试（指数退避，shouldRetry；失败未超 max 重派发）
  - Always 补偿节点（派发失败仍继续，不阻断 DAG）
  - 条件节点（Condition 传入 Activity 求值，跳过则 NODE_SKIPPED）
- DispatchNodeActivity：条件求值（CEL）+ 派发 NODE_STARTED + Agent 任务派发
  - NodeDispatcher 可注入（Dispatch/EventSink/Condition），可测
- DispatchInput 扩展 Condition 字段（条件表达式携带）
- 单元测试（Temporal testsuite + mock activity + RegisterDelayedCallback 确定性发 Signal）：
  Sequential（DAG 顺序）/ NodeFailedThenRetry / EmptyData / ShouldRetry ✅
- 修复 relay 测试竞态（connectionCount 等待连接就绪，-count=3 稳定）

待办（M3 收尾 / M4）
- Human-in-the-loop（approval 节点，AGENT_ASK_HUMAN 事件）
- Firecracker 强隔离（M4）



