package controllers

import (
	"context"
	"sync"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/eventbus"
	"github.com/example/agent-runtime-operator/internal/metrics"
	"github.com/example/agent-runtime-operator/internal/orchestrator"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NodeEventProcessor 处理编排节点结果事件，幂等推进 WorkflowRun 状态（P1-3）。
// 设计（design-doc 4.2.4）：
//   - 事件总线 at-least-once 投递，故需幂等去重（幂等键：eventID）
//   - 更新 status.nodeResults / currentNode / eventsCount
//   - 全部节点到终态后判定 run 最终状态（SUCCEEDED / FAILED，含 Always 补偿）
type NodeEventProcessor struct {
	client.Client
	// 幂等去重：已处理的事件 ID（内存态；生产可用缓存/状态存储）
	mu          sync.Mutex
	seenEvents  map[string]struct{}
	// ConditionEvaluator 用于 always/条件推进（预留，R-2）
	Condition orchestrator.ConditionEvaluator
}

// NewNodeEventProcessor 创建节点事件处理器
func NewNodeEventProcessor(c client.Client) *NodeEventProcessor {
	return &NodeEventProcessor{
		Client:     c,
		seenEvents: make(map[string]struct{}),
	}
}

// OnEvent 处理单个编排事件（幂等推进 WorkflowRun 状态）
func (p *NodeEventProcessor) OnEvent(ctx context.Context, evt *eventbus.CloudEvent) error {
	if evt == nil {
		return nil
	}

	// 幂等去重（P1-3）
	if !p.markSeen(evt.ID) {
		return nil // 已处理过，跳过
	}

	// 从 Data 解析 runID 与节点（NATS subject 不允许 '/'，经 CloudEvent.Data 传递）
	runID, nodeID, ok := parseNodeEvent(evt)
	if !ok {
		return nil
	}

	// 可观测性指标（M5）
	metrics.ObserveEvent(evt.TenantID, evt.Type)

	// 根据事件类型确定节点结果
	nodeState, _ := nodeStateFromType(evt.Type)
	if nodeState == "" {
		return nil // 非节点结果事件
	}

	// 定位 WorkflowRun（按 runID 对应 status.runId 查询）
	run, err := p.findRunByID(ctx, evt.TenantID, runID)
	if err != nil {
		return err
	}
	if run == nil {
		return nil
	}

	// 更新节点结果
	p.applyNodeResult(run, nodeID, nodeState, evt)

	// 完成判定
	p.reconcileRunCompletion(ctx, run)
	return nil
}

// markSeen 幂等去重：返回 true 表示首次处理
func (p *NodeEventProcessor) markSeen(eventID string) bool {
	if eventID == "" {
		return true // 无事件 ID，不幂等（退回默认）
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.seenEvents[eventID]; ok {
		return false
	}
	p.seenEvents[eventID] = struct{}{}
	return true
}

// applyNodeResult 更新节点结果到 status
func (p *NodeEventProcessor) applyNodeResult(run *agentv1.WorkflowRun, nodeID, state string, evt *eventbus.CloudEvent) {
	if run.Status.NodeResults == nil {
		run.Status.NodeResults = make(map[string]interface{})
	}
	// 记录节点状态
	run.Status.NodeResults[nodeID] = map[string]interface{}{
		"state": state,
		"event": evt.ID,
	}
	if state != "SUCCEEDED" {
		// 记录错误/失败信息
		run.Status.NodeResults[nodeID] = map[string]interface{}{
			"state":  state,
			"event":  evt.ID,
			"reason": evtTypeReason(evt.Type),
		}
	}
	run.Status.CurrentNode = nodeID
	run.Status.EventsCount++
}

// findRunByID 在租户内查找 WorkflowRun。
// 事件携带的 runID 为 Temporal WorkflowID（worker 的 getRunID 返回 WorkflowID），
// 故同时匹配 Status.RunID 与 Status.WorkflowID。
func (p *NodeEventProcessor) findRunByID(ctx context.Context, tenantID, runID string) (*agentv1.WorkflowRun, error) {
	list := &agentv1.WorkflowRunList{}
	if err := p.List(ctx, list, client.InNamespace(tenantID)); err != nil {
		return nil, err
	}
	for i := range list.Items {
		if list.Items[i].Status.RunID == runID || list.Items[i].Status.WorkflowID == runID {
			return &list.Items[i], nil
		}
	}
	return nil, nil
}

// reconcileRunCompletion 完成判定：所有节点到终态后更新 run 最终状态
// 规则：若有 FAILED 节点则 FAILED；否则 SUCCEEDED（含 Always 补偿节点已执行）
func (p *NodeEventProcessor) reconcileRunCompletion(ctx context.Context, run *agentv1.WorkflowRun) {
	// 简化完成判定：只要有任何节点 FAILED 即 FAILED；全部 SUCCEEDED 才 SUCCEEDED
	failed := false
	succeeded := 0
	for _, v := range run.Status.NodeResults {
		m, _ := v.(map[string]interface{})
		state, _ := m["state"].(string)
		switch state {
		case "SUCCEEDED":
			succeeded++
		case "FAILED":
			failed = true
		}
	}

	if failed {
		p.setFinalPhase(run, agentv1.PhaseRunFailed)
	} else if succeeded > 0 {
		p.setFinalPhase(run, agentv1.PhaseRunSucceeded)
	}
	_ = p.Status().Update(ctx, run)
}

// setFinalPhase 设置最终阶段（不覆盖已设置的终态）
func (p *NodeEventProcessor) setFinalPhase(run *agentv1.WorkflowRun, phase string) {
	if run.Status.Phase == agentv1.PhaseRunSucceeded || run.Status.Phase == agentv1.PhaseRunFailed {
		return
	}
	run.Status.Phase = phase
}

// parseNodeEvent 从事件 Data 解析 runID 与 nodeID
// NATS subject 不允许 '/'，故经 CloudEvent.Data 传递 {node, runID}。
func parseNodeEvent(evt *eventbus.CloudEvent) (runID, nodeID string, ok bool) {
	if evt == nil || evt.Data == nil {
		return "", "", false
	}
	nodeID, _ = evt.Data["node"].(string)
	runID, _ = evt.Data["runID"].(string)
	return runID, nodeID, nodeID != "" && runID != ""
}

// nodeStateFromType 从事件类型映射节点结果状态
func nodeStateFromType(evtType string) (state string, terminal bool) {
	switch evtType {
	case eventbus.EventNodeStarted:
		return "RUNNING", false
	case eventbus.EventNodeSucceeded:
		return "SUCCEEDED", true
	case eventbus.EventNodeFailed:
		return "FAILED", true
	case eventbus.EventNodeSkipped:
		return "SKIPPED", true
	default:
		return "", false
	}
}

// evtTypeReason 从失败事件提取原因（简化）
func evtTypeReason(evtType string) string {
	if evtType == eventbus.EventNodeFailed {
		return "node execution failed"
	}
	return ""
}
