package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// MCP 服务端出口（M-13）的协议层测试：不依赖数据库的握手与工具清单。
func TestMCPInitializeAndToolsList(t *testing.T) {
	s := &Server{}
	// initialize
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	s.mcpEndpoint(rec, req)
	if rec.Code != 200 {
		t.Fatalf("initialize 状态码 %d", rec.Code)
	}
	var resp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			ProtocolVersion string         `json:"protocolVersion"`
			ServerInfo      map[string]any `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if resp.JSONRPC != "2.0" || resp.ID != 1 {
		t.Errorf("JSON-RPC 信封不符: %+v", resp)
	}
	if resp.Result.ProtocolVersion == "" || resp.Result.ServerInfo["name"] != "pangu" {
		t.Errorf("initialize 结果不符: %+v", resp.Result)
	}

	// tools/list
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	s.mcpEndpoint(rec, req)
	var listResp struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("解析 tools/list: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range listResp.Result.Tools {
		names[tl.Name] = true
		if tl.InputSchema == nil {
			t.Errorf("工具 %s 缺少 inputSchema", tl.Name)
		}
	}
	for _, want := range []string{
		"list_findings", "get_finding", "list_tasks", "get_task",
		"list_assets", "search_traffic", "get_traffic", "search_vulns",
	} {
		if !names[want] {
			t.Errorf("工具清单缺少 %s", want)
		}
	}
	// 工具名重复会让外部 Agent 的 tools/call 路由到错误实现。
	if len(names) != len(listResp.Result.Tools) {
		t.Errorf("工具名有重复：%d 个唯一名 / %d 个工具", len(names), len(listResp.Result.Tools))
	}
	// 只读边界：不应暴露任何写工具。
	for _, banned := range []string{"create_task", "run_task", "delete_finding"} {
		if names[banned] {
			t.Errorf("不应暴露写工具 %s", banned)
		}
	}
}

func TestMCPUnknownMethodAndNotification(t *testing.T) {
	s := &Server{}
	// 未知方法 → -32601
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"nope"}`))
	s.mcpEndpoint(rec, req)
	var errResp struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
	if errResp.Error.Code != -32601 {
		t.Errorf("未知方法应返回 -32601，实际 %d", errResp.Error.Code)
	}

	// 通知（initialized）→ 202 且无响应体
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	s.mcpEndpoint(rec, req)
	if rec.Code != 202 {
		t.Errorf("通知应返回 202，实际 %d", rec.Code)
	}

	// 非 POST → 405
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/mcp", nil)
	s.mcpEndpoint(rec, req)
	if rec.Code != 405 {
		t.Errorf("GET 应返回 405，实际 %d", rec.Code)
	}
}
