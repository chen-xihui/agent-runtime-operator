// MCP Server 示例实现（stdio 传输）。
// 作为 Tool Server 接入平台的参考实现，暴露示例工具，供 MCP Proxy 转发端到端验证。
// 用法：go run ./cmd/mcp-server  （通过 stdin/stdout 走 MCP JSON-RPC 协议）
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	protocolVersion = "2024-11-05"
	jsonrpc         = "2.0"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// 示例工具表
var tools = []map[string]any{
	{
		"name":        "echo",
		"description": "Echo the input message",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}}},
	},
	{
		"name":        "get_weather",
		"description": "Get weather for a city",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}},
	},
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	// 增大 token 以容纳较大 payload
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		handle(&req)
	}
}

func handle(req *request) {
	var result any
	var rpcErr *rpcError

	switch req.Method {
	case "initialize":
		result = map[string]any{
			"protocolVersion": protocolVersion,
			"serverInfo":      map[string]any{"name": "example-mcp-server", "version": "1.0"},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		}
	case "notifications/initialized":
		// 通知，无响应
		return
	case "tools/list":
		result = map[string]any{"tools": tools}
	case "tools/call":
		result, rpcErr = handleToolCall(req.Params)
	default:
		rpcErr = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}

	resp := response{JSONRPC: jsonrpc, ID: req.ID, Result: result, Error: rpcErr}
	data, _ := json.Marshal(resp)
	fmt.Println(string(data))
}

type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func handleToolCall(raw json.RawMessage) (any, *rpcError) {
	var p callParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	switch p.Name {
	case "echo":
		msg, _ := p.Arguments["message"].(string)
		return toolsResult(fmt.Sprintf(`{"received":"%s"}`, msg)), nil
	case "get_weather":
		city, _ := p.Arguments["city"].(string)
		return toolsResult(fmt.Sprintf(`{"city":"%s","weather":"sunny","temp":25}`, city)), nil
	default:
		return nil, &rpcError{Code: -32602, Message: "tool not found: " + p.Name}
	}
}

func toolsResult(text string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": false,
	}
}
