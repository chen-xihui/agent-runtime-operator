package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// HTTPTransport 基于 streamable HTTP 的 MCP 传输（远程 MCP Server）
// 使用 MCP streamable HTTP 的 POST 端点，单请求/响应（非流式简化实现）。
type HTTPTransport struct {
	endpoint string
	client   *http.Client
	auth     string // Bearer token（可选）
	id       atomic.Int64
	mu       sync.Mutex
}

// NewHTTPTransport 创建 streamable HTTP 传输
func NewHTTPTransport(endpoint, auth string, timeout time.Duration) *HTTPTransport {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HTTPTransport{
		endpoint: endpoint,
		client:   &http.Client{Timeout: timeout},
		auth:     auth,
	}
}

// Call 发送 JSON-RPC 请求到远程 MCP Server
func (t *HTTPTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := t.id.Add(1)
	paramsRaw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	req := JSONRPCRequest{JSONRPC: jsonrpcVersion, ID: id, Method: method, Params: paramsRaw}
	body, _ := json.Marshal(req)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if t.auth != "" {
		httpReq.Header.Set("Authorization", "Bearer "+t.auth)
	}

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp http call %q: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mcp http status %d: %s", resp.StatusCode, string(data))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// 尝试解析为 JSON-RPC 响应
	var jr JSONRPCResponse
	if err := json.Unmarshal(data, &jr); err == nil && jr.ID == id {
		if jr.Error != nil {
			return nil, fmt.Errorf("mcp error: %s", jr.Error.Message)
		}
		return jr.Result, nil
	}

	// 可能是 SSE 流格式（text/event-stream），尝试从中提取 data 行
	if sse := extractSSEData(string(data)); sse != nil {
		return sse, nil
	}
	return nil, fmt.Errorf("mcp: unexpected response format: %s", string(data))
}

// extractSSEData 从 SSE 文本中提取 "data:" JSON
func extractSSEData(s string) json.RawMessage {
	// 简化：查找 "data:" 前缀行并解析
	for _, line := range bytes.Split([]byte(s), []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte("data:")) {
			payload := bytes.TrimSpace(trimmed[len("data:"):])
			var m map[string]any
			if json.Unmarshal(payload, &m) == nil {
				if result, ok := m["result"]; ok {
					raw, _ := json.Marshal(result)
					return raw
				}
			}
		}
	}
	return nil
}

// Close HTTP 传输无需显式关闭连接
func (t *HTTPTransport) Close() error { return nil }

var _ Transport = (*HTTPTransport)(nil)
