package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/just805/pangu/db"
)

// ═══ M-13：MCP 服务端出口 ═══
//
// 平台此前只能作为 MCP **客户端**调用外部工具（mcphttp）；本文件反过来把平台的
// 只读能力暴露成 MCP 服务端，让 Claude Code / Codex 这类外部 Agent 直接读任务与
// 发现，不必自己写 HTTP 客户端。
//
// 设计边界（重要）：
//   - **只读**。建任务、重跑、取消等写操作不在暴露列表里——外部 Agent 不该有
//     操作权限，需要写时走开放 API（M-12）并显式设计。
//   - 鉴权复用 requireAuth：JWT 或 API Key 都可（同一个下游）。
//   - 手写最小 JSON-RPC，不引 MCP SDK：与项目「不引第三方文档库」的取向一致。

const mcpProtocolVersion = "2024-11-05"

type mcpRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// mcpServerTools 是暴露给外部 Agent 的只读工具清单。
func mcpServerTools() []mcpTool {
	return []mcpTool{
		{
			Name:        "list_findings",
			Description: "按任务/严重度/状态列出漏洞发现（只读）。返回紧缩字段：id/severity/vulnclass/name/status/task_id。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id":  map[string]any{"type": "string", "description": "任务 id（可选，省略=全部任务）"},
					"severity": map[string]any{"type": "string", "enum": []string{"critical", "high", "medium", "low"}},
					"status":   map[string]any{"type": "string", "description": "pending|in_progress|confirmed|resolved|fixed|false_positive|ignored|duplicate|risk_accepted"},
					"query":    map[string]any{"type": "string", "description": "名称/摘要/证据的模糊检索关键词"},
					"limit":    map[string]any{"type": "integer", "description": "返回条数，默认 20，上限 100"},
				},
			},
		},
		{
			Name:        "get_finding",
			Description: "按 id 取一条漏洞的完整详情（含摘要/证据/报告正文/CVSS）。只读。",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "integer", "description": "漏洞 id"}},
				"required":   []string{"id"},
			},
		},
		{
			Name:        "list_tasks",
			Description: "列出平台任务（id/描述/目标/状态），用于定位要读哪个任务的发现。只读。",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

// mcpEndpoint 处理 MCP Streamable HTTP 的 JSON-RPC 请求。
func (s *Server) mcpEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "只支持 POST（Streamable HTTP）")
		return
	}
	var req mcpRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMCPError(w, nil, -32700, "请求体不是合法 JSON")
		return
	}
	// 通知（无 id）不需要响应体，按协议返回 202。
	if len(req.ID) == 0 || string(req.ID) == "null" {
		if req.Method == "notifications/initialized" || strings.HasPrefix(req.Method, "notifications/") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}
	switch req.Method {
	case "initialize":
		writeMCPResult(w, req.ID, map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "pangu", "version": "1"},
		})
	case "ping":
		writeMCPResult(w, req.ID, map[string]any{})
	case "tools/list":
		writeMCPResult(w, req.ID, map[string]any{"tools": mcpServerTools()})
	case "tools/call":
		s.mcpToolsCall(w, r, req)
	default:
		writeMCPError(w, req.ID, -32601, "不支持的方法："+req.Method)
	}
}

func (s *Server) mcpToolsCall(w http.ResponseWriter, r *http.Request, req mcpRPCRequest) {
	pg := s.m.PG()
	if pg == nil {
		writeMCPError(w, req.ID, -32603, "需要 PostgreSQL")
		return
	}
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeMCPError(w, req.ID, -32602, "params 不是合法对象")
		return
	}
	switch params.Name {
	case "list_findings":
		var args struct {
			TaskID   string `json:"task_id"`
			Severity string `json:"severity"`
			Status   string `json:"status"`
			Query    string `json:"query"`
			Limit    int    `json:"limit"`
		}
		_ = json.Unmarshal(params.Arguments, &args)
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}
		rows, total, err := pg.ListFindingsPage(db.FindingFilter{
			TaskID: strings.TrimSpace(args.TaskID), Severity: strings.TrimSpace(args.Severity),
			Status: strings.TrimSpace(args.Status), Query: strings.TrimSpace(args.Query),
		}, 1, limit)
		if err != nil {
			writeMCPError(w, req.ID, -32603, err.Error())
			return
		}
		out := make([]map[string]any, 0, len(rows))
		for _, f := range rows {
			taskID := ""
			if f.TaskID != nil {
				taskID = strconv.FormatInt(*f.TaskID, 10)
			}
			out = append(out, map[string]any{
				"id": f.ID, "task_id": taskID, "severity": f.Severity,
				"vulnclass": f.VulnClass, "name": f.Name, "status": f.Status,
			})
		}
		mcpTextResult(w, req.ID, map[string]any{"total": total, "findings": out})

	case "get_finding":
		var args struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(params.Arguments, &args)
		if args.ID <= 0 {
			writeMCPError(w, req.ID, -32602, "id 必填")
			return
		}
		f, err := pg.GetFinding(args.ID)
		if err != nil {
			writeMCPError(w, req.ID, -32603, err.Error())
			return
		}
		if f == nil {
			writeMCPError(w, req.ID, -32602, fmt.Sprintf("漏洞 %d 不存在", args.ID))
			return
		}
		mcpTextResult(w, req.ID, map[string]any{
			"id": f.ID, "vulnclass": f.VulnClass, "name": f.Name, "severity": f.Severity,
			"status": f.Status, "summary": f.Summary, "evidence": f.Evidence, "report": f.Report,
			"cvss_vector": f.CVSSVector, "cvss_score": f.CVSSScore,
		})

	case "list_tasks":
		tasks, err := pg.ListTasks()
		if err != nil {
			writeMCPError(w, req.ID, -32603, err.Error())
			return
		}
		out := make([]map[string]any, 0, len(tasks))
		for _, t := range tasks {
			out = append(out, map[string]any{
				"id": t.ID, "description": t.Description, "goal": t.Goal, "status": t.Status,
			})
		}
		mcpTextResult(w, req.ID, map[string]any{"tasks": out})

	default:
		writeMCPError(w, req.ID, -32602, "未知工具："+params.Name+"（本服务端只暴露只读工具）")
	}
}

// mcpTextResult 把结果序列化成 MCP 的 content 结构。
func mcpTextResult(w http.ResponseWriter, id json.RawMessage, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeMCPError(w, id, -32603, err.Error())
		return
	}
	writeMCPResult(w, id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	})
}

func writeMCPResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": rawID(id), "result": result})
}

func writeMCPError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	writeJSON(w, 200, map[string]any{
		"jsonrpc": "2.0", "id": rawID(id),
		"error": map[string]any{"code": code, "message": msg},
	})
}

// rawID 让空 id 序列化为 null（JSON-RPC 要求）。
func rawID(id json.RawMessage) any {
	if len(id) == 0 {
		return nil
	}
	return json.RawMessage(id)
}
