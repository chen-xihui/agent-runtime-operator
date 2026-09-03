package admission

import (
	"context"

	"github.com/example/agent-runtime-operator/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// 本文件提供 controller-runtime CustomValidator/CustomDefaulter 接入层，
// 每个校验器在 Create/Update 时调用 validate.go 的纯函数，把校验逻辑接到 admission 请求上。
// 纯函数保持无依赖，便于任意调用方（webhook/handler/CLI/测试）复用。

var (
	_ admission.CustomValidator = AgentValidator{}
	_ admission.CustomDefaulter = AgentDefaulter{}
	_ admission.CustomValidator = WorkflowValidator{}
	_ admission.CustomValidator = WorkflowRunValidator{}
	_ admission.CustomValidator = TenantValidator{}
	_ admission.CustomValidator = MCPEndpointValidator{}
	_ admission.CustomValidator = ToolBindingValidator{}
)

// ===================== Agent =====================

// AgentValidator 在 Create/Update/Delete 时校验 Agent
type AgentValidator struct{}

func (AgentValidator) ValidateCreate(_ context.Context, raw runtime.Object) (admission.Warnings, error) {
	a, ok := raw.(*v1.Agent)
	if !ok {
		return nil, nil
	}
	return nil, ValidateAgent(a)
}

func (AgentValidator) ValidateUpdate(_ context.Context, _, raw runtime.Object) (admission.Warnings, error) {
	a, ok := raw.(*v1.Agent)
	if !ok {
		return nil, nil
	}
	return nil, ValidateAgent(a)
}

func (AgentValidator) ValidateDelete(context.Context, runtime.Object) (admission.Warnings, error) { return nil, nil }

// AgentDefaulter 在创建/更新时注入 Agent 默认值
type AgentDefaulter struct{}

func (AgentDefaulter) Default(_ context.Context, raw runtime.Object) error {
	a, ok := raw.(*v1.Agent)
	if !ok {
		return nil
	}
	DefaultAgent(a)
	return nil
}

// ===================== Workflow =====================

type WorkflowValidator struct{}

func (WorkflowValidator) ValidateCreate(_ context.Context, raw runtime.Object) (admission.Warnings, error) {
	wf, ok := raw.(*v1.Workflow)
	if !ok {
		return nil, nil
	}
	return nil, ValidateWorkflow(wf)
}

func (WorkflowValidator) ValidateUpdate(_ context.Context, _, raw runtime.Object) (admission.Warnings, error) {
	wf, ok := raw.(*v1.Workflow)
	if !ok {
		return nil, nil
	}
	return nil, ValidateWorkflow(wf)
}

func (WorkflowValidator) ValidateDelete(context.Context, runtime.Object) (admission.Warnings, error) { return nil, nil }

// ===================== WorkflowRun =====================

type WorkflowRunValidator struct{}

func (WorkflowRunValidator) ValidateCreate(_ context.Context, raw runtime.Object) (admission.Warnings, error) {
	run, ok := raw.(*v1.WorkflowRun)
	if !ok {
		return nil, nil
	}
	return nil, ValidateWorkflowRun(run)
}

func (WorkflowRunValidator) ValidateUpdate(_ context.Context, _, raw runtime.Object) (admission.Warnings, error) {
	run, ok := raw.(*v1.WorkflowRun)
	if !ok {
		return nil, nil
	}
	return nil, ValidateWorkflowRun(run)
}

func (WorkflowRunValidator) ValidateDelete(context.Context, runtime.Object) (admission.Warnings, error) { return nil, nil }

// ===================== Tenant =====================

type TenantValidator struct{}

func (TenantValidator) ValidateCreate(_ context.Context, raw runtime.Object) (admission.Warnings, error) {
	t, ok := raw.(*v1.Tenant)
	if !ok {
		return nil, nil
	}
	return nil, ValidateTenant(t)
}

func (TenantValidator) ValidateUpdate(_ context.Context, _, raw runtime.Object) (admission.Warnings, error) {
	t, ok := raw.(*v1.Tenant)
	if !ok {
		return nil, nil
	}
	return nil, ValidateTenant(t)
}

func (TenantValidator) ValidateDelete(context.Context, runtime.Object) (admission.Warnings, error) { return nil, nil }

// ===================== MCPEndpoint / ToolBinding =====================

type MCPEndpointValidator struct{}

func (MCPEndpointValidator) ValidateCreate(_ context.Context, raw runtime.Object) (admission.Warnings, error) {
	ep, ok := raw.(*v1.MCPEndpoint)
	if !ok {
		return nil, nil
	}
	return nil, ValidateMCPEndpoint(ep)
}
func (MCPEndpointValidator) ValidateUpdate(_ context.Context, _, raw runtime.Object) (admission.Warnings, error) {
	ep, ok := raw.(*v1.MCPEndpoint)
	if !ok {
		return nil, nil
	}
	return nil, ValidateMCPEndpoint(ep)
}
func (MCPEndpointValidator) ValidateDelete(context.Context, runtime.Object) (admission.Warnings, error) { return nil, nil }

type ToolBindingValidator struct{}

func (ToolBindingValidator) ValidateCreate(_ context.Context, raw runtime.Object) (admission.Warnings, error) {
	tb, ok := raw.(*v1.ToolBinding)
	if !ok {
		return nil, nil
	}
	return nil, ValidateToolBinding(tb)
}
func (ToolBindingValidator) ValidateUpdate(_ context.Context, _, raw runtime.Object) (admission.Warnings, error) {
	tb, ok := raw.(*v1.ToolBinding)
	if !ok {
		return nil, nil
	}
	return nil, ValidateToolBinding(tb)
}
func (ToolBindingValidator) ValidateDelete(context.Context, runtime.Object) (admission.Warnings, error) { return nil, nil }
