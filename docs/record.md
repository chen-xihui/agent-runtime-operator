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