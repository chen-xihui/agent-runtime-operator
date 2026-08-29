package orchestrator

import (
	"context"
	"strings"
	"testing"
)

func TestCELConditionEvaluator_Compile(t *testing.T) {
	e, err := NewCELConditionEvaluator()
	if err != nil {
		t.Fatalf("new evaluator: %v", err)
	}
	// 合法表达式
	if err := e.Compile("nodes.review.result.approved == false"); err != nil {
		t.Fatalf("compile valid expr: %v", err)
	}
	// 非法表达式（编译期报错，R-2）
	if err := e.Compile("nodes.review.result ><"); err == nil {
		t.Fatal("expected compile error for invalid expr")
	}
}

func TestCELConditionEvaluator_Eval(t *testing.T) {
	e, _ := NewCELConditionEvaluator()
	ctx := context.Background()

	// 节点结果为 approved=false → 条件成立
	ok, err := e.Eval(ctx, "nodes.review.result.approved == false", map[string]any{
		"nodes": map[string]any{
			"review": map[string]any{"result": map[string]any{"approved": false}},
		},
		"input": map[string]any{},
	})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !ok {
		t.Fatal("condition should be true when approved==false")
	}

	// approved=true → 条件不成立
	ok, _ = e.Eval(ctx, "nodes.review.result.approved == false", map[string]any{
		"nodes": map[string]any{
			"review": map[string]any{"result": map[string]any{"approved": true}},
		},
	})
	if ok {
		t.Fatal("condition should be false when approved==true")
	}
}

func TestCELConditionEvaluator_EvalInput(t *testing.T) {
	e, _ := NewCELConditionEvaluator()
	ctx := context.Background()

	// 使用 input 变量
	ok, err := e.Eval(ctx, "input.retryCount < 3", map[string]any{
		"input": map[string]any{"retryCount": 2},
	})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !ok {
		t.Fatal("expected true for retryCount=2 < 3")
	}

	// 非 bool 结果报错
	if _, err := e.Eval(ctx, "input.retryCount", map[string]any{"input": map[string]any{"retryCount": 5}}); err == nil {
		t.Fatal("expected error for non-bool result")
	}
}

func TestCELConditionEvaluator_InvalidExpr(t *testing.T) {
	e, _ := NewCELConditionEvaluator()
	_, err := e.Eval(context.Background(), "not a valid cel && expression", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "compile") {
		t.Fatalf("expected compile error, got %v", err)
	}
}
