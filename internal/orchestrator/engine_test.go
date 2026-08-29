package orchestrator

import (
	"strings"
	"testing"
)

// TemporalEngine 需要真实 Temporal 客户端；此处验证防御逻辑（nil client / 空数据）。
func TestTemporalEngine_NilClient(t *testing.T) {
	e := NewTemporalEngine(nil, "test-queue")
	if _, err := e.Execute(&ExecutionData{Nodes: map[string]*Node{"a": {ID: "a"}}}, nil); err == nil {
		t.Fatal("expected error with nil client")
	}
	if err := e.Cancel("run-1"); err == nil {
		t.Fatal("expected error with nil client")
	}
}

func TestTemporalEngine_EmptyData(t *testing.T) {
	// 即使有 client（此处 nil），空数据也应校验失败
	e := NewTemporalEngine(nil, "test-queue")
	_, err := e.Execute(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty data error, got %v", err)
	}
}

func TestTemporalEngine_Interface(t *testing.T) {
	// 编译期断言：TemporalEngine 实现 DAGEngine 接口
	var _ DAGEngine = NewTemporalEngine(nil, "q")
}
