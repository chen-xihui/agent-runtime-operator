// Package sdk 提供 Agent Runtime 平台的开放 SDK（M5）。
// 封装对核心 CRD 的管理操作，供外部调用者（平台用户/集成方）以编程方式
// 创建租户、管理 Agent、编排工作流。
package sdk

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
)

// Client Agent Runtime 平台 SDK 客户端
type Client struct {
	client client.Client
}

// New 创建 SDK 客户端（使用指定 rest config 连接集群）
func New(cfg *rest.Config) (*Client, error) {
	scheme := runtime.NewScheme()
	if err := agentv1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("add scheme: %w", err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}
	return &Client{client: c}, nil
}

// NewFromClient 从现有 controller-runtime client 创建 SDK
func NewFromClient(c client.Client) *Client {
	return &Client{client: c}
}

// ===================== Tenant =====================

// CreateTenant 创建租户
func (s *Client) CreateTenant(ctx context.Context, t *agentv1.Tenant) error {
	return s.client.Create(ctx, t)
}

// GetTenant 获取租户
func (s *Client) GetTenant(ctx context.Context, name string) (*agentv1.Tenant, error) {
	t := &agentv1.Tenant{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: name}, t); err != nil {
		return nil, err
	}
	return t, nil
}

// ListTenants 列出所有租户
func (s *Client) ListTenants(ctx context.Context) (*agentv1.TenantList, error) {
	l := &agentv1.TenantList{}
	if err := s.client.List(ctx, l); err != nil {
		return nil, err
	}
	return l, nil
}

// ===================== Agent =====================

// CreateAgent 创建 Agent（自动触发 Sandbox 调谐）
func (s *Client) CreateAgent(ctx context.Context, ns string, a *agentv1.Agent) error {
	a.Namespace = ns
	return s.client.Create(ctx, a)
}

// GetAgent 获取 Agent
func (s *Client) GetAgent(ctx context.Context, ns, name string) (*agentv1.Agent, error) {
	a := &agentv1.Agent{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, a); err != nil {
		return nil, err
	}
	return a, nil
}

// ListAgents 列出租户内所有 Agent
func (s *Client) ListAgents(ctx context.Context, ns string) (*agentv1.AgentList, error) {
	l := &agentv1.AgentList{}
	if err := s.client.List(ctx, l, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	return l, nil
}

// DeleteAgent 删除 Agent（关联 Sandbox 由回收逻辑处理）
func (s *Client) DeleteAgent(ctx context.Context, ns, name string) error {
	return s.client.Delete(ctx, &agentv1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	})
}

// ===================== Sandbox =====================

// GetSandbox 获取 Sandbox
func (s *Client) GetSandbox(ctx context.Context, ns, name string) (*agentv1.Sandbox, error) {
	sb := &agentv1.Sandbox{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, sb); err != nil {
		return nil, err
	}
	return sb, nil
}

// ListSandboxes 列出租户内所有 Sandbox
func (s *Client) ListSandboxes(ctx context.Context, ns string) (*agentv1.SandboxList, error) {
	l := &agentv1.SandboxList{}
	if err := s.client.List(ctx, l, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	return l, nil
}

// SuspendSandbox 挂起沙箱（M4，Firecracker 快照 Suspend）
func (s *Client) SuspendSandbox(ctx context.Context, ns, name string) error {
	sb := &agentv1.Sandbox{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, sb); err != nil {
		return err
	}
	sb.Spec.Suspend = boolPtr(true)
	return s.client.Update(ctx, sb)
}

// ResumeSandbox 恢复沙箱（M4，Firecracker 快照 Resume）
func (s *Client) ResumeSandbox(ctx context.Context, ns, name string) error {
	sb := &agentv1.Sandbox{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, sb); err != nil {
		return err
	}
	sb.Spec.Suspend = boolPtr(false)
	return s.client.Update(ctx, sb)
}

// ===================== Workflow =====================

// CreateWorkflow 创建编排工作流
func (s *Client) CreateWorkflow(ctx context.Context, ns string, w *agentv1.Workflow) error {
	w.Namespace = ns
	return s.client.Create(ctx, w)
}

// CreateWorkflowRun 创建编排运行（触发 Temporal 执行）
// 使用 GenerateName 自动生成唯一运行名（多次触发同工作流产生多个运行实例）。
func (s *Client) CreateWorkflowRun(ctx context.Context, ns string, wfRef string, input map[string]interface{}) (*agentv1.WorkflowRun, error) {
	run := &agentv1.WorkflowRun{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "run-",
		},
		Spec: agentv1.WorkflowRunSpec{
			WorkflowRef: wfRef,
			Input:       input,
		},
	}
	run.Namespace = ns
	if err := s.client.Create(ctx, run); err != nil {
		return nil, err
	}
	return run, nil
}

// GetWorkflowRun 获取编排运行状态
func (s *Client) GetWorkflowRun(ctx context.Context, ns, name string) (*agentv1.WorkflowRun, error) {
	run := &agentv1.WorkflowRun{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, run); err != nil {
		return nil, err
	}
	return run, nil
}

// ListWorkflowRuns 列出租户内所有 WorkflowRun
func (s *Client) ListWorkflowRuns(ctx context.Context, ns string) (*agentv1.WorkflowRunList, error) {
	l := &agentv1.WorkflowRunList{}
	if err := s.client.List(ctx, l, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	return l, nil
}

// CancelWorkflowRun 取消编排运行。
// 说明：CRD status 为只读快照，取消意图由 reconciler 识别——将 status.phase 置为
// CANCELLED 后，WorkflowRunReconciler 会调用 Temporal 引擎 Cancel（design-doc 6.1 CancelRun）。
func (s *Client) CancelWorkflowRun(ctx context.Context, ns, name string) error {
	run := &agentv1.WorkflowRun{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, run); err != nil {
		return err
	}
	if run.Status.Phase == agentv1.PhaseRunCancelled {
		return nil // 幂等：已取消
	}
	run.Status.Phase = agentv1.PhaseRunCancelled
	return s.client.Status().Update(ctx, run)
}

// WorkflowEvent 编排运行事件（R-5 低频快照派生，准实时）
type WorkflowEvent struct {
	RunID     string `json:"runId"`
	Node      string `json:"node"`
	Type      string `json:"type"` // NODE_SUCCEEDED / NODE_FAILED / NODE_STARTED / WORKFLOW_COMPLETED
	State     string `json:"state,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// GetWorkflowRunEvents 返回运行的事件流。
// 设计文档 GetEvents 语义：从事件总线拉取精确事件流（N5）；此处 CRD 无事件历史，
// 以 status 只读快照（nodeResults/currentNode/phase）派生节点级事件，供准实时查询。
func (s *Client) GetWorkflowRunEvents(ctx context.Context, ns, name string) ([]WorkflowEvent, error) {
	run, err := s.GetWorkflowRun(ctx, ns, name)
	if err != nil {
		return nil, err
	}
	var events []WorkflowEvent
	// 节点结果 → 终态事件
	for node, v := range run.Status.NodeResults {
		evt := WorkflowEvent{RunID: run.Status.RunID, Node: node}
		if m, ok := v.(map[string]interface{}); ok {
			if st, ok := m["state"].(string); ok {
				switch st {
				case "SUCCEEDED":
					evt.Type = "NODE_SUCCEEDED"
					evt.State = st
				case "FAILED":
					evt.Type = "NODE_FAILED"
					evt.State = st
				default:
					evt.Type = "NODE_"+st
					evt.State = st
				}
			}
		}
		events = append(events, evt)
	}
	// 运行终态补充 WORKFLOW_COMPLETED
	switch run.Status.Phase {
	case agentv1.PhaseRunSucceeded, agentv1.PhaseRunFailed, agentv1.PhaseRunCancelled:
		events = append(events, WorkflowEvent{
			RunID: run.Status.RunID,
			Type:  "WORKFLOW_COMPLETED",
			State: run.Status.Phase,
		})
	}
	if events == nil {
		events = []WorkflowEvent{}
	}
	return events, nil
}

func boolPtr(b bool) *bool { return &b }
