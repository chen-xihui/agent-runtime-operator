package orchestrator

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
)

// CELConditionEvaluator 基于 CEL 的条件求值器（R-2）
// 绑定变量：
//   - nodes.<id>.result  节点结果
//   - input              工作流输入
//   - env                环境变量
//
// Compile 在提交阶段静态校验表达式，非法表达式在提交时即报错（R-2）。
type CELConditionEvaluator struct {
	env *cel.Env
	mu  sync.RWMutex
	// 已编译表达式缓存：expr -> ast/program
	programs map[string]cel.Program
}

// NewCELConditionEvaluator 创建 CEL 求值器
func NewCELConditionEvaluator() (*CELConditionEvaluator, error) {
	env, err := cel.NewEnv(
		cel.Variable("nodes", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("input", cel.DynType),
		cel.Variable("env", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return nil, fmt.Errorf("create cel env: %w", err)
	}
	return &CELConditionEvaluator{env: env, programs: make(map[string]cel.Program)}, nil
}

// Compile 静态编译校验表达式
func (e *CELConditionEvaluator) Compile(expr string) error {
	if expr == "" {
		return fmt.Errorf("empty condition expression")
	}
	ast, iss := e.env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return fmt.Errorf("compile condition %q: %w", expr, iss.Err())
	}
	prg, err := e.env.Program(ast)
	if err != nil {
		return fmt.Errorf("program condition %q: %w", expr, err)
	}
	e.mu.Lock()
	e.programs[expr] = prg
	e.mu.Unlock()
	return nil
}

// Eval 运行时求值
func (e *CELConditionEvaluator) Eval(ctx context.Context, expr string, bindings map[string]any) (bool, error) {
	e.mu.RLock()
	prg, ok := e.programs[expr]
	e.mu.RUnlock()
	if !ok {
		if err := e.Compile(expr); err != nil {
			return false, err
		}
		e.mu.RLock()
		prg = e.programs[expr]
		e.mu.RUnlock()
	}

	// 组织 CEL 激活变量
	vars := map[string]any{}
	if bindings != nil {
		if nodes, ok := bindings["nodes"]; ok {
			vars["nodes"] = nodes
		}
		if input, ok := bindings["input"]; ok {
			vars["input"] = input
		}
		if env, ok := bindings["env"]; ok {
			vars["env"] = env
		}
	}

	out, _, err := prg.Eval(vars)
	if err != nil {
		return false, fmt.Errorf("eval condition %q: %w", expr, err)
	}
	// 校验结果为 bool
	b, ok := out.(types.Bool)
	if !ok {
		// 兜底：转换原生 bool
		if nb, ok2 := out.Value().(bool); ok2 {
			return nb, nil
		}
		return false, fmt.Errorf("condition %q did not evaluate to bool, got %T", expr, out)
	}
	return bool(b), nil
}

var _ ConditionEvaluator = (*CELConditionEvaluator)(nil)
