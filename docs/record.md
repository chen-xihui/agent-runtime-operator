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

Human-in-the-loop（approval 节点）✅

已实现（internal/orchestrator/approval*.go + workflow.go）
- NodeKindApproval（kind: approval）节点：暂停流程等待人工审批
- RequestApprovalActivity：触发 AGENT_ASK_HUMAN，通知外部工单系统（ApprovalNotifier 可注入）
- runApprovalNode：确定性等待审批结果 Signal（approval-result）
  - workflow.Selector 监听 approval-result + 超时 Timer（R-1 确定性）
  - 审批通过 → APPROVED；拒绝 → REJECTED；超时未审批 → 按拒绝处理
  - 拒绝时非 Always 节点 → 整个运行失败
- 单元测试（Temporal testsuite）：ApprovalApproved（通过后 DAG 继续）/ ApprovalRejected（拒绝致失败）✅

M4 强隔离（核心实现）🔄

已实现
- RuntimeAdapter（internal/runtime/adapter.go）
  - Runtime 接口：Name / Suspend / Resume
  - gVisor（降级无操作，SnapshotSupported=false）、Firecracker（SuspendCapable，SnapshotSupported=true）
  - Registry：按 RuntimeClass 选择适配器，未知运行时兜底 gVisor
- Sandbox Suspend/Resume 运维（internal/controllers/sandbox_controller.go）
  - SandboxSpec.Suspend *bool 运维意图（+CRD/deepcopy）
  - Running→Suspended（Suspend）与 Suspended→Running（Resume）状态机迁移
- 租户安全加固（internal/controllers/tenant_controller.go）
  - 租户创建默认 NetworkPolicy Deny-All（Ingress+Egress，PodSelector 全选）
  - RBAC 增加 networkpolicies 权限
- 单元测试：Registry 选择 / 适配器 SuspendResume / 未知运行时 / Sandbox SuspendResume / NetworkPolicy 构建 / wantSuspend ✅
- 全部 9 包 build / vet / test 通过 ✅

M4 收尾 ✅

已实现
- DLP 审计存储（internal/audit/store.go）
  - Record（租户/Agent/动作/资源/成功/时间）、Filter 查询
  - MemoryStore（进程内，时间倒序+Limit）、NoopStore（默认）
  - MCP Proxy 接入 WithAuditStore：全量工具调用审计落库（P1-1），成功+失败均记录
- Firecracker 快照运维完善（internal/runtime/adapter.go）
  - Suspend 生成快照元数据（SnapshotID/state/mem），Resume 从快照恢复
  - 无快照直接 Resume 报错（明确语义）
- 单元测试：audit Write/Query/NilRecord/Noop、mcp AuditStore（成功+失败落库）、runtime FirecrackerResumeWithoutSnapshot ✅
- 全部 10 包 build / vet / test 通过 ✅

M5 生产化（可观测性）🔄

已实现
- Metrics（internal/metrics/metrics.go）
  - 编排：runs_started / run_duration / events_total
  - 沙箱：state_transitions / active（按运行时）
  - 工具：tool_calls（按租户/工具/结果）、mcp_errors
  - 注册到 controller-runtime /metrics 端点（Prometheus）
  - 接入点：WorkflowRunReconciler（run 指标）、NodeEventProcessor（事件）、SandboxReconciler（状态迁移）、MCP Proxy（工具调用）
- 全链路追踪（internal/telemetry/trace.go）
  - W3C Trace Context（traceparent）生成/解析/子 span 派生
  - eventbus Subscribe 自动注入 traceparent（链路连续）
- 结构化日志（internal/telemetry/logging.go）
  - 租户/Agent 维度索引（WithTenant）
  - 敏感字段脱敏（token/secret/password/credential，DLP）
- 单元测试：metrics Observe/Values、telemetry trace 生成/解析/子 span、logging RedactMap/RedactString/IsSensitiveKey ✅
- 全部 12 包 build / vet / test 通过 ✅

M5 高可用 + 多集群联邦 ✅

已实现
- 高可用（config/manager/manager.yaml）
  - replicas=2 + RollingUpdate
  - Pod 反亲和（preferredAntiAffinity，hostname 拓扑）
  - LeaderElection（仅 leader 调谐，其余 standby，已有 --leader-elect + LeaderElectionID）
- 多集群联邦（internal/federation/federator.go）
  - Cluster 描述（hub/spoke/standalone 角色、endpoint、TrustedFrom）
  - Router：注册/注销/List、Allowed（双向信任校验 D-4）、Route（跨集群转发）、Lookup（联邦发现）
  - IsValidClusterName（集群名校验，防注入）
  - WithRouteFn 可注入跨集群转发实现（可测）
- 单元测试：Register/List、Allowed TrustedFrom（D-4）、Route（允许/禁止/未配置）、Lookup、IsValidClusterName ✅
- 全部 13 包 build / vet / test 通过 ✅

待办（M5 收尾 / 后续）
- 开放 SDK 与插件市场
- Firecracker 实际 KVM 运行时接入、审计收集到外部存储
- 联邦接入 A2A Gateway（跨集群 Agent 委派端到端）


M1-M4 集群端到端验证（虚拟机 192.168.0.31）✅

验证结果
- M1：Tenant→Namespace、Agent→Sandbox→Pod（busybox 普通 Pod 版）✅
  - 状态机 Provisioning→Running 正确
- M4：租户默认 NetworkPolicy Deny-All（tenant-default-deny）✅
- M4：ResourceQuota（limits.cpu/memory）✅
- M4：Sandbox Suspend（Running→Suspended，spec.suspend=true）✅
- M4：Sandbox Resume（Suspended→Running，spec.suspend=false）✅
- M2：ToolBinding/MCPEndpoint CRD 创建 + Agent 联动（读取授权注入）无报错 ✅

发现并修复的产品 Bug
1. 空数组错误触发 Event Relay（agent_controller.go）
   - agent.Spec.MCP.AllowedTools != nil 对空数组 [] 返回 true
   - 修复：len(agent.Spec.MCP.AllowedTools) > 0
2. CRD 缺 suspend 字段（config/crd/agent.runtime.io_sandboxes.yaml）
   - Go 类型有 Suspend 但 CRD manifest 未同步，APIServer 拒绝
   - 修复：CRD manifest 添加 suspend 字段

验证环境处理
- setsid 启动 operator 确保后台存活（nohup 在 SSH 断开会被杀）
- tenant-m4 PodSecurity 降级 privileged（busybox 需 root，同 M1-b 处理）
- 清理 tenant-a Terminating namespace（旧资源残留）


M5 收尾：联邦接入 A2A + 开放 SDK ✅

已实现
- 联邦接入 A2A Gateway（internal/a2a/federation.go + gateway.go）
  - Federator 接口（Allowed/Route/Lookup）抽象自 federation.Router，避免包循环
  - MemoryGateway.WithFederator(clusterName, f) 启用跨集群委派
  - SendTask：本地 Agent → 本地委派；本地无目标 Agent → 跨集群联邦委派
    - 联邦发现（Lookup）→ 双向信任校验（D-4 Allowed）→ 路由转发
    - 无联邦/无信任 → ErrAgentNotFound
- 开放 SDK（sdk/client.go）
  - New（rest config）/ NewFromClient（复用 client）
  - Tenant：Create/Get/List
  - Agent：Create/Get/List
  - Sandbox：Get/List/Suspend/Resume（M4 快照运维）
  - Workflow：Create/GetWorkflowRun（GenerateName 唯一运行名）
- 单元测试：
  - A2A 联邦：CrossCluster（本地+跨集群）/ CrossClusterNotAllowed（D-4）/ NoFederator ✅
  - SDK：TenantLifecycle / AgentAndSandbox（含 Suspend/Resume）/ Workflow ✅
- 全部 14 包 build / vet / test 通过 ✅

待办（后续）
- 插件市场（SKD 生态扩展）
- Firecracker 实际 KVM 运行时接入、审计收集到外部存储


M3 编排 NATS+Temporal 集群端到端验证（虚拟机 192.168.0.31）🔄

部署
- NATS（nats:2.10，JetStream，端口 4222/8222）
- PostgreSQL 13（temporal 支撑库，端口 5432）
- Temporal（auto-setup + postgres，端口 7233/7234/7235/7239，default namespace）
- Temporal Worker（独立进程 cmd/worker，注册 GenericOrchestratorWorkflow + DispatchNodeActivity）
- Operator（--temporal-address + --nats-url，WorkflowRun 控制器 + NodeEventProcessor 启用）

验证结果
- Workflow m3-pipeline（analyze → review 顺序 DAG）创建 ✅
- WorkflowRun 创建 → Temporal 执行启动（runID 回写 RUNNING）✅
- GenericOrchestratorWorkflow 执行 2 节点 DAG：dispatching node analyze → dispatching node review ✅
- 编排完成：orchestration completed, nodeCount 2 ✅（Temporal 侧执行成功）

发现并修复的 Bug（本地编译通过，待部署验证）
1. NATS subject 含 '/' 非法字符
   - worker EventSink 用 "workflow/"+nodeID，NATS subject 不允许 '/'
   - 修复：改为点分隔 "workflow."+nodeID，runID 经 CloudEvent.Data 传递
   - NodeEventProcessor：parseNodeSubject（subject 分隔）→ parseNodeEvent（Data 解析）
2. worker 进程在完成后退会
   - worker.Run(InterruptCh()) 在后台环境退出
   - 修复：改为 StartAsync() + select{} 阻塞，保持 worker 持续 poll
3. DispatchNodeActivity 依赖 context 注入 dispatcher
   - worker 无法预绑定 context
   - 修复：改为包级 SetDefaultNodeDispatcher，worker 启动时配置
4. DispatchInput.RunID 作为 WorkflowID 用于 SignalWorkflow（getRunID 返回 WorkflowID）
   - 导出 NodeResultSignalName 常量供 worker 发节点结果 Signal

M3 事件推进集群验证 ✅

端到端验证结果（192.168.0.31）
- Workflow m3-pipeline（analyze → review 顺序 DAG）
- WorkflowRun 创建 → Temporal 执行 → Worker 派发节点
- 事件推进完整链路：
  Worker 发 NODE_STARTED + NODE_SUCCEEDED → NATS → operator NodeEventProcessor → 更新 status
- 最终状态：phase=SUCCEEDED, wfID=agent-orchestration-<id>, node=review, events=4
- nodeResults：analyze={SUCCEEDED}, review={SUCCEEDED} ✅

Root Cause（事件推进不生效）与修复
1. EventSink 只发 NODE_STARTED，缺 NODE_SUCCEEDED（终态）
   - worker 的 Dispatch 只发 Temporal Signal 推进 Workflow，未发 NODE_SUCCEEDED 事件
   - 修复：Dispatch 同时发 NODE_SUCCEEDED 到 NATS
2. runID 语义不匹配：worker 事件携带 WorkflowID，但 WorkflowRun status.RunID 是 Temporal RunID
   - findRunByID 匹配失败，事件被丢弃
   - 修复：
     - WorkflowRunStatus 新增 WorkflowID 字段（+CRD）
     - TemporalEngine.Execute 返回 (runID, workflowID)
     - WorkflowRun reconciler 回写 WorkflowID（updateRunStatus）
     - NodeEventProcessor.findRunByID 同时匹配 RunID 与 WorkflowID
- 诊断工具：cmd/nats-inspect（订阅 NATS 抓事件，确认 worker 发布链路）

验证辅助
- worker 用 nohup 直接启动（setsid nohup 组合在 SSH 会话异常），StartAsync+select 保持 poll
- cmd/nats-inspect 编译传送用于抓包诊断


插件市场（M5 收尾）✅

已实现（internal/plugin/）
- Registry（registry.go）：插件注册中心
  - Install（语义化版本管理，同版本冲突/降级拒绝）、Uninstall、Enable/Disable、Get
  - List（类型过滤）/ Discover（标签+类型发现）
  - Call（仅 enabled 状态可调用）、Versions（版本历史）
  - 插件类型：tool/skill/hook/visual
- AgentPlugins（store.go）：Agent 插件扩展
  - Mount/Unmount（幂等）、Installed、ListEnabled、HasPlugin、Call
  - 按 Agent 挂载插件，获得对应能力扩展
- 单元测试：InstallAndGet / VersionUpgrade（升级/降级）/ EnableDisableCall / DiscoverAndList / AgentPlugins MountAndCall / CompareVersions ✅
- 全部 15 包 build / vet / test 通过 ✅


REST API Server ✅

已实现
- cmd/api-server：HTTP 服务入口（--addr / --kubeconfig，支持 in-cluster）
- internal/apiserver/server.go：REST 处理器（对接 SDK）
  - Tenant：GET/POST /api/v1/tenants，GET /api/v1/tenants/{name}
  - Agent：GET/POST /api/v1/tenants/{tenant}/agents
  - Sandbox：GET /api/v1/tenants/{tenant}/sandboxes/{name}
    - POST .../suspend、POST .../resume（M4 快照运维）
  - Workflow：POST /api/v1/tenants/{tenant}/workflows
    - GET /api/v1/tenants/{tenant}/workflowruns/{name}
  - /healthz 健康检查
  - 错误映射：NotFound→404、AlreadyExists→409、非法 body→400、panic 恢复中间件
- Go 1.22 method+path 路由（r.PathValue）
- 单元测试（httptest + fake client）：
  Health / CreateAndGetTenant（含 404）/ ListTenants / CreateAndGetSandbox / BadRequest（400）✅
- 全部 16 包 build / vet / test 通过 ✅


DLP 审计收集到外部存储 + 查询 ✅

已实现
- 审计查询 REST 接口（internal/apiserver/server.go）
  - GET /api/v1/audit?tenant=&agent=&action=&resource=&limit=（过滤 + Limit）
  - Server.WithAuditStore 注入审计存储（默认 NoopStore）
- NATS JetStream 审计存储（internal/audit/nats_store.go）
  - NewNatsStore：连接 NATS + 创建 audit-events 持久化 stream（FileStorage）
  - Write：审计记录发布到 JetStream（subject: audit.<tenant>.<action>）
  - Query：从 stream 拉取并按 Filter 过滤
  - 可替换 MemoryStore（进程内）与 NatsStore（持久化）
- 单元测试：API QueryAudit（租户/agent 过滤 + limit）、audit NatsStore 集成（本机 nats:2.10 实测）✅
- 全部 16 包 build / vet / test 通过 ✅


插件市场接入 CRD（Plugin 控制器）✅

已实现
- Plugin CRD 类型（api/v1/plugin_types.go）
  - PluginSpec（version/type/description/author/tags/requiresAgents/enabled）+ PluginStatus（state/installedVersion/message）
  - Cluster 作用域，shortName=plug，subresource.status
  - 注册 scheme + deepcopy + CRD manifest + sample（plugin_code_search.yaml）
- PluginReconciler（internal/controllers/plugin_controller.go）
  - Plugin CRD → PluginRegistry 联动（安装/启用/禁用）
  - 按 spec.enabled 设置插件状态，回写 status（state/installedVersion/message）
  - 安装失败回写 failed + message
- main.go：构造 PluginRegistry 全局单例 + 注册 PluginReconciler（M5 插件市场）
- 单元测试（fake client + status subresource）：InstallEnabled / Disabled / NotFound ✅
- 全部 16 包 build / vet / test 通过 ✅


沙箱资源管理（R-6）✅

已实现（internal/controllers/sandbox_controller.go）
- 自动回收（R-6）：
  - Sandbox Running 时读取关联 Agent.spec.security.maxLifetimeMin
  - 超过最大存活时长（从 LastTransitionTime 计算）→ DestroyPod + phase=Terminated
  - Agent 删除时回收沙箱
- 租户配额校验（R-6）：
  - Sandbox Provisioning 时读取 Tenant.spec.quota.maxSandboxes
  - 租户内非终态沙箱数达上限 → 排队等待（不创建 Pod，RequeueAfter 10s）
- 单元测试（fake client + status subresource）：
  RecycleExceededLifetime（超时回收）/ NoRecycleWithinLifetime（未超时不回收）/ QuotaExceeded（配额满排队）✅
- 测试 scheme 补充 corev1 注册（Pod）
- 全部 16 包 build / vet / test 通过 ✅



