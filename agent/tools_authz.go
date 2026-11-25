package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	actool "github.com/Autumn-27/norma/tool"

	"github.com/just805/pangu/db"
)

// ═══ M-03：角色权限矩阵工具 ═══
//
// 越权测试（IDOR / 垂直越权 / 未授权访问）的前提是「有两个身份、知道谁能访问什么」。
// 之前这个前提只在模型的工作记忆里：每条越权意图重新从流量猜一遍，猜完就丢，
// 缺口也看不见。两个工具把它落成任务级数据：
//
//	authz_record —— 记录/更新矩阵条目（角色 × 接口 × 期望/实测/证据）
//	authz_matrix —— 读矩阵 + 聚合缺口（哪些组合还没测、哪些结论没证据）
//
// 矩阵只是线索：expected≠observed 不等于漏洞，确认仍要 traffic_id 指向的真实
// 请求响应——与平台证据闸门同一口径。

const (
	authzMaxEntries = 50
	authzPathMax    = 512
	authzRoleMax    = 64
)

// authzMethods 是允许的 HTTP 方法白名单。
var authzMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true,
}

// normalizeAuthzState 归一 allow|deny|unknown（接受中文写法）。
func normalizeAuthzState(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "allow", "allowed", "允许", "可访问", "成功":
		return "allow"
	case "deny", "denied", "forbid", "blocked", "拒绝", "禁止", "拦截":
		return "deny"
	case "", "unknown", "未知", "未测":
		return "unknown"
	}
	return ""
}

// normalizeAuthzEntry 校验并归一一条矩阵记录。空串错误表示通过。
func normalizeAuthzEntry(e db.AuthzEntry) (db.AuthzEntry, string) {
	e.Role = strings.TrimSpace(e.Role)
	if e.Role == "" {
		return e, "role 必填：这条记录属于哪个身份/角色（如 anon、user-a、admin、tenant-a）"
	}
	if len([]rune(e.Role)) > authzRoleMax {
		e.Role = string([]rune(e.Role)[:authzRoleMax])
	}
	e.Path = strings.TrimSpace(e.Path)
	if e.Path == "" {
		return e, "path 必填：接口路径（以 / 开头，不含域名，如 /api/orders/1234）"
	}
	if !strings.HasPrefix(e.Path, "/") {
		return e, fmt.Sprintf("path 必须以 / 开头（不含 host）：%q", e.Path)
	}
	if len([]rune(e.Path)) > authzPathMax {
		e.Path = string([]rune(e.Path)[:authzPathMax])
	}
	e.Method = strings.ToUpper(strings.TrimSpace(e.Method))
	if e.Method == "" {
		e.Method = "GET"
	}
	if !authzMethods[e.Method] {
		return e, fmt.Sprintf("method 只能是 GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS：%q", e.Method)
	}
	if v := normalizeAuthzState(e.Expected); v == "" {
		return e, fmt.Sprintf("expected 只能是 allow|deny|unknown：%q", e.Expected)
	} else {
		e.Expected = v
	}
	if v := normalizeAuthzState(e.Observed); v == "" {
		return e, fmt.Sprintf("observed 只能是 allow|deny|unknown：%q", e.Observed)
	} else {
		e.Observed = v
	}
	if e.StatusCode < 0 || e.StatusCode > 599 {
		return e, fmt.Sprintf("status_code 必须是 0-599：%d", e.StatusCode)
	}
	e.Host = strings.TrimSpace(e.Host)
	e.TrafficID = strings.TrimSpace(e.TrafficID)
	e.Note = strings.TrimSpace(e.Note)
	return e, ""
}

func (t *ToolSet) authzRecord() actool.CoreTool {
	return t.writeExpTool("authz_record",
		"记录/更新【角色权限矩阵】条目：哪个身份、对哪个接口、期望能不能访问、实测结果与证据。\n"+
			"用途：越权测试（IDOR/垂直越权/未授权）前先建矩阵、测完回填——同一接口的多角色结论复用，"+
			"缺口（哪些组合还没测）一眼可见，不用每条意图重新猜一遍。\n"+
			"⚠️ 只是线索不是结论：expected≠observed 要再用 traffic_id 指向的真实请求响应确认，"+
			"确认后照常 report_finding。同一 role+host+method+path 重复提交会覆盖更新，不会堆重复行。",
		obj(map[string]any{
			"entries": map[string]any{
				"type":        "array",
				"description": fmt.Sprintf("矩阵条目数组（1-%d 条）", authzMaxEntries),
				"items": obj(map[string]any{
					"role":        str("身份/角色标签（必填）：anon、user-a、user-b、admin、tenant-a 等"),
					"host":        str("目标主机（可选）：api.example.com"),
					"method":      str("HTTP 方法，默认 GET"),
					"path":        str("接口路径（必填，以 / 开头，不含域名）"),
					"expected":    str("按设计该角色能否访问：allow | deny | unknown"),
					"observed":    str("实测结果：allow | deny | unknown（没测就省略/unknown）"),
					"status_code": intp("实测 HTTP 状态码（可选）"),
					"traffic_id":  str("支撑实测结论的流量 ID（traffic_search 返回；没有证据的结论会记入 no_evidence）"),
					"note":        str("备注（可选）：如「A 的 token 读到 B 的订单」"),
				}, "role", "path"),
			},
			"intent_id": idp("产生这些记录的意图 id（可选；worker 默认填当前意图）"),
		}, "entries"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Entries  []db.AuthzEntry `json:"entries"`
				IntentID json.RawMessage `json:"intent_id"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if len(a.Entries) == 0 {
				return actool.Errorf("entries 不能为空"), nil
			}
			if len(a.Entries) > authzMaxEntries {
				a.Entries = a.Entries[:authzMaxEntries]
			}
			intent := pid(a.IntentID)
			if intent <= 0 {
				intent = t.ownerNode
			}
			store := t.ts.Authz()
			ids := make([]int64, 0, len(a.Entries))
			errs := map[string]string{}
			for i, raw := range a.Entries {
				e, msg := normalizeAuthzEntry(raw)
				if msg != "" {
					errs[fmt.Sprint(i)] = msg
					continue
				}
				e.IntentID = intent
				id, err := store.Upsert(ctx, t.taskID, t.ts.ID(), e)
				if err != nil {
					errs[fmt.Sprint(i)] = err.Error()
					continue
				}
				ids = append(ids, id)
			}
			out := map[string]any{"ids": ids, "count": len(ids)}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			if st, err := store.Stats(t.taskID); err == nil {
				out["stats"] = st
			}
			return jsonResult(out)
		})
}

func (t *ToolSet) authzMatrixTool() actool.CoreTool {
	return t.readExpTool("authz_matrix",
		"查看本任务的【角色权限矩阵】与缺口统计：哪些 角色×接口 组合还没测（observed=unknown）、"+
			"哪些结论没有流量证据（no_evidence）、哪些期望与实测不一致（suspicious，越权线索）。\n"+
			"派越权意图前先看它，避免重复测试已知结论；也可用它确认某个接口是否已有基线。",
		obj(map[string]any{
			"role":  str("只看某个角色（可选；省略=全部）"),
			"limit": intp("返回条数，默认 200，上限 500"),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Role  string `json:"role"`
				Limit int    `json:"limit"`
			}
			_ = json.Unmarshal(in, &a)
			store := t.ts.Authz()
			entries, err := store.List(t.taskID, strings.TrimSpace(a.Role), a.Limit)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			st, statsErr := store.Stats(t.taskID)
			out := map[string]any{"entries": entries, "count": len(entries)}
			if statsErr == nil {
				out["stats"] = st
				if st.Unknown > 0 {
					out["hint"] = fmt.Sprintf("还有 %d 条组合未测（observed=unknown）；suspicious=%d 是 expected 与 observed 不一致的越权线索，"+
						"确认前请用 traffic_id 核实真实请求响应。", st.Unknown, st.Suspicious)
				}
				if st.NoEvidence > 0 {
					out["evidence_hint"] = fmt.Sprintf("有 %d 条实测结论没有绑定 traffic_id——没有证据的结论在报告里不可复核，请补绑流量。", st.NoEvidence)
				}
			}
			return jsonResult(out)
		})
}
