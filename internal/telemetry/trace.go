// Package telemetry 提供轻量全链路追踪（M5 可观测性）。
// 实现 W3C Trace Context（traceparent）的生成、解析与跨组件传播，
// 通过 eventbus CloudEvent.TraceParent 在编排引擎 / Agent / MCP 间透传。
package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// TraceContext W3C Trace Context
type TraceContext struct {
	// TraceID 32 位十六进制
	TraceID string
	// SpanID 16 位十六进制
	SpanID string
	// TraceFlags 采样标志（01 = sampled）
	TraceFlags string
}

// NewTraceContext 生成新的 trace 上下文（sampled）
func NewTraceContext() *TraceContext {
	return &TraceContext{
		TraceID:    randomHex(16),
		SpanID:     randomHex(8),
		TraceFlags: "01",
	}
}

// Traceparent 生成 W3C traceparent 头（version-traceid-spanid-flags）
func (t *TraceContext) Traceparent() string {
	return fmt.Sprintf("00-%s-%s-%s", t.TraceID, t.SpanID, t.TraceFlags)
}

// WithChild 从当前 trace 派生子 span（保留 traceID，生成新 spanID）
func (t *TraceContext) WithChild() *TraceContext {
	return &TraceContext{
		TraceID:    t.TraceID,
		SpanID:     randomHex(8),
		TraceFlags: t.TraceFlags,
	}
}

// ParseTraceparent 解析 W3C traceparent 字符串
// 返回 nil 表示解析失败（非法格式）。
func ParseTraceparent(s string) *TraceContext {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "-")
	if len(parts) != 4 {
		return nil
	}
	if len(parts[1]) != 32 || len(parts[2]) != 16 {
		return nil
	}
	return &TraceContext{
		TraceID:    parts[1],
		SpanID:     parts[2],
		TraceFlags: parts[3],
	}
}

// ParseOrNew 解析 traceparent；非法则生成新 trace（保证链路连续）
func ParseOrNew(s string) *TraceContext {
	if tc := ParseTraceparent(s); tc != nil {
		return tc
	}
	return NewTraceContext()
}

// randomHex 生成 n 字节的十六进制随机串
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// 兜底：用固定值（生产不应触发）
		return strings.Repeat("0", n*2)
	}
	return hex.EncodeToString(b)
}
