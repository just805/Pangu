package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/just805/pangu/db"
	"github.com/just805/pangu/traffic"
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
		{
			Name:        "get_task",
			Description: "按 id 取任务详情：目标、状态、超时、范围内各类资产计数。只读。",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "integer", "description": "任务 id"}},
				"required":   []string{"id"},
			},
		},
		{
			Name:        "list_assets",
			Description: "查资产台账（全局共享的资产图）。可按任务或类型过滤。只读，返回紧缩字段。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "integer", "description": "只看该任务关联的资产（可选）"},
					"type": map[string]any{
						"type":        "string",
						"enum":        []string{"root_domain", "subdomain", "ip", "app", "service", "endpoint"},
						"description": "资产类型（可选，省略=全部）",
					},
					"limit": map[string]any{"type": "integer", "description": "返回条数，默认 30，上限 200"},
				},
			},
		},
		{
			Name:        "search_traffic",
			Description: "检索 MITM 录制的 HTTP 往来（只返回索引，不含正文）。正文用 get_traffic 按 id 取。只读。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"host":   map[string]any{"type": "string", "description": "主机（子串匹配，建议必填以免扫全库）"},
					"method": map[string]any{"type": "string", "description": "请求方法（精确，如 GET/POST）"},
					"query":  map[string]any{"type": "string", "description": "元数据/正文的模糊检索词"},
					"body":   map[string]any{"type": "string", "description": "仅在请求/响应正文里全文搜索（≥3 字符）"},
					"status": map[string]any{"type": "string", "description": "状态码（\"404\"）或分类（\"4xx\"）"},
					"limit":  map[string]any{"type": "integer", "description": "返回条数，默认 20，上限 100"},
				},
			},
		},
		{
			Name:        "get_traffic",
			Description: "按 id 取一条录制的完整请求/响应原文。只读；超大正文会截断并标注。",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "string", "description": "search_traffic 返回的 id"}},
				"required":   []string{"id"},
			},
		},
		{
			Name:        "search_vulns",
			Description: "查平台漏洞情报库（NVD/KEV/PoC）：按组件+版本查影响区间，或按 CVE 号取详情与 PoC 列表。只读。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"vendor":  map[string]any{"type": "string"},
					"product": map[string]any{"type": "string"},
					"version": map[string]any{"type": "string"},
					"cve_id":  map[string]any{"type": "string", "description": "已知 CVE 号（如 CVE-2024-1234），优先于组件查询"},
					"limit":   map[string]any{"type": "integer", "description": "返回条数，默认 20，上限 50"},
				},
			},
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

	case "get_task":
		var args struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(params.Arguments, &args)
		if args.ID <= 0 {
			writeMCPError(w, req.ID, -32602, "id 必填")
			return
		}
		task, err := pg.GetTask(args.ID)
		if err != nil {
			writeMCPError(w, req.ID, -32603, err.Error())
			return
		}
		if task == nil {
			writeMCPError(w, req.ID, -32602, fmt.Sprintf("任务 %d 不存在", args.ID))
			return
		}
		detail := map[string]any{
			"id": task.ID, "name": task.Name, "description": task.Description, "goal": task.Goal,
			"status": task.Status, "paused": task.Paused, "created_at": task.CreatedAt,
			"timeout_seconds": task.TimeoutSeconds, "deadline_at": task.DeadlineAt,
		}
		// 资产覆盖的分子分母：外部 Agent 最常问「这个任务测了什么」。
		if as := s.m.Assets(); as != nil {
			if counts, err := as.CountsByTypeForTask(task.ID); err == nil && len(counts) > 0 {
				detail["asset_counts"] = counts
			}
		}
		mcpTextResult(w, req.ID, detail)

	case "list_assets":
		var args struct {
			TaskID int64  `json:"task_id"`
			Type   string `json:"type"`
			Limit  int    `json:"limit"`
		}
		_ = json.Unmarshal(params.Arguments, &args)
		as := s.m.Assets()
		if as == nil {
			writeMCPError(w, req.ID, -32603, "资产存储未就绪")
			return
		}
		limit := mcpClampLimit(args.Limit, 30, 200)
		typ := strings.TrimSpace(args.Type)
		var rows []*db.Asset
		var err error
		if args.TaskID > 0 {
			rows, err = as.QueryByTask(args.TaskID, typ, limit, 0)
		} else {
			rows, err = as.QueryByType(typ, limit, 0)
		}
		if err != nil {
			writeMCPError(w, req.ID, -32603, err.Error())
			return
		}
		out := make([]map[string]any, 0, len(rows))
		for _, a := range rows {
			out = append(out, mcpAssetBrief(a))
		}
		mcpTextResult(w, req.ID, map[string]any{"count": len(out), "assets": out})

	case "search_traffic":
		tr := s.m.Traffic()
		if tr == nil {
			writeMCPError(w, req.ID, -32603, "流量录制未启用（系统设置 → 流量捕获）")
			return
		}
		var args struct {
			Host   string `json:"host"`
			Method string `json:"method"`
			Query  string `json:"query"`
			Body   string `json:"body"`
			Status string `json:"status"`
			Limit  int    `json:"limit"`
		}
		_ = json.Unmarshal(params.Arguments, &args)
		limit := mcpClampLimit(args.Limit, 20, 100)
		rows, total, err := tr.Page(traffic.PageQuery{
			Host:   strings.TrimSpace(args.Host),
			Method: strings.TrimSpace(args.Method),
			Query:  strings.TrimSpace(args.Query),
			Body:   strings.TrimSpace(args.Body),
			Status: strings.TrimSpace(args.Status),
		}, 1, limit)
		if err != nil {
			writeMCPError(w, req.ID, -32603, err.Error())
			return
		}
		out := make([]map[string]any, 0, len(rows))
		for _, e := range rows {
			out = append(out, map[string]any{
				"id": e.ID, "ts": e.TS, "host": e.Host, "method": e.Method,
				"status": e.Status, "url": e.URL, "resp_len": e.RespLen,
			})
		}
		mcpTextResult(w, req.ID, map[string]any{"total": total, "exchanges": out})

	case "get_traffic":
		tr := s.m.Traffic()
		if tr == nil {
			writeMCPError(w, req.ID, -32603, "流量录制未启用（系统设置 → 流量捕获）")
			return
		}
		var args struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(params.Arguments, &args)
		id := strings.TrimSpace(args.ID)
		if id == "" {
			writeMCPError(w, req.ID, -32602, "id 必填")
			return
		}
		reqText, respText, err := tr.Get(id)
		if err != nil {
			writeMCPError(w, req.ID, -32603, err.Error())
			return
		}
		mcpTextResult(w, req.ID, map[string]any{
			"id":             id,
			"request":        mcpTruncateText(reqText, mcpMaxTextBytes),
			"response":       mcpTruncateText(respText, mcpMaxTextBytes),
			"truncated_hint": "单段上限 32KB；完整内容见平台「流量查看器」",
		})

	case "search_vulns":
		vi := s.m.vulnintel
		if vi == nil {
			writeMCPError(w, req.ID, -32603, "漏洞情报库未就绪")
			return
		}
		var args struct {
			Vendor  string `json:"vendor"`
			Product string `json:"product"`
			Version string `json:"version"`
			CVEID   string `json:"cve_id"`
			Limit   int    `json:"limit"`
		}
		_ = json.Unmarshal(params.Arguments, &args)
		ctx := r.Context()
		if cve := strings.TrimSpace(args.CVEID); cve != "" {
			d, err := vi.GetVuln(ctx, cve)
			if err != nil {
				writeMCPError(w, req.ID, -32603, err.Error())
				return
			}
			if d == nil {
				writeMCPError(w, req.ID, -32602, fmt.Sprintf("%s 不在情报库", cve))
				return
			}
			res := map[string]any{"vuln": d}
			if pocs, err := vi.GetPoCs(ctx, cve); err == nil && len(pocs) > 0 {
				res["pocs"] = pocs
			}
			mcpTextResult(w, req.ID, res)
			return
		}
		limit := mcpClampLimit(args.Limit, 20, 50)
		hits, err := vi.SearchVulns(ctx, strings.TrimSpace(args.Vendor), strings.TrimSpace(args.Product), strings.TrimSpace(args.Version), limit)
		if err != nil {
			writeMCPError(w, req.ID, -32603, err.Error())
			return
		}
		mcpTextResult(w, req.ID, map[string]any{"count": len(hits), "hits": hits})

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

// mcpMaxTextBytes 是单段请求/响应原文回给外部 Agent 的上限。外部 Agent 的上下文
// 比平台 worker 更不可控，整段 5MB 的响应会直接把它的窗口打爆；完整内容在平台
// 「流量查看器」里可查。
const mcpMaxTextBytes = 32 * 1024

// mcpClampLimit 统一收敛外部传入的条数，避免一次调用把库扫穿。
func mcpClampLimit(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// mcpTruncateText 按字节上限截断并标注，保证不切碎 UTF-8。
func mcpTruncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n…（已截断，原始 %d 字节，上限 %d）", len(s), n)
}

// mcpAssetBrief 只回外部 Agent 决策需要的字段：整行 assets 有 30+ 列，
// 全量返回既吵又容易把上下文塞满。
func mcpAssetBrief(a *db.Asset) map[string]any {
	m := map[string]any{"id": a.ID, "type": a.Type}
	put := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			m[k] = v
		}
	}
	if a.CompanyID != nil {
		m["company_id"] = *a.CompanyID
	}
	if len(a.TaskIDs) > 0 {
		m["task_ids"] = a.TaskIDs
	}
	put("domain", a.Domain)
	put("root_domain", a.RootDomain)
	put("ip", a.IP)
	put("url", a.URL)
	put("service_name", a.ServiceName)
	put("page_title", a.PageTitle)
	put("method", a.Method)
	if a.Port != nil {
		m["port"] = *a.Port
	}
	if a.StatusCode != nil {
		m["status_code"] = *a.StatusCode
	}
	if len(a.Technologies) > 0 {
		m["technologies"] = a.Technologies
	}
	return m
}
