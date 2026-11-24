package agent

// report_finding 的入参容错与幂等键（上游 0.4.101 P0-4 同款语义）。
//
// 模型把字段名写歪是常态：`type/class` 代替 vulnclass、`desc/description`
// 代替 summary、`proof/poc/request/response` 代替 evidence、`risk/level`
// 代替 severity、`assets/asset_id` 代替 asset_ids，甚至整段入参被包成
// JSON 字符串或多套一层 `{"finding":{...}}`。这些写法此前会直接解析失败或
// 丢字段，一条已确认的漏洞因此进不了库。
//
// 这里在解析前做一层确定性归一：剥外壳、别名折叠、中文严重度转英文、
// 标量资产转数组、缺字段兜底。归一本身幂等，合法入参逐字节不变。
//
// 幂等键：模型没给 root_cause_key 时，从「类别 + 首个 URL 路径 + 首个参数名」
// 推导一个稳定根因键，让同一参数位上的重复上报走既有 root_cause_key 去重链路
// （证据仍完整保留）。

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/just805/pangu/db"
)

// findingFieldAliases 常见别名 → 标准字段名。键小写。
var findingFieldAliases = map[string]string{
	"vulnerability":  "vulnclass",
	"type":           "vulnclass",
	"class":          "vulnclass",
	"category":       "vulnclass",
	"vuln_type":      "vulnclass",
	"vuln_class":     "vulnclass",
	"title":          "name",
	"desc":           "summary",
	"description":    "summary",
	"detail":         "summary",
	"proof":          "evidence",
	"poc":            "evidence",
	"request":        "evidence",
	"response":       "evidence",
	"risk":           "severity",
	"level":          "severity",
	"assets":         "asset_ids",
	"asset_id":       "asset_ids",
	"target_ids":     "asset_ids",
	"target_id":      "asset_ids",
	"rootcausekey":   "root_cause_key",
	"rootcause":      "root_cause_key",
	"traffic":        "traffic_refs",
	"evidencehintid": "evidence_hint_id",
}

// severityAliases 中文/常见写法 → 标准英文档。
var severityAliases = map[string]string{
	"严重": "critical", "critical": "critical", "危急": "critical",
	"高危": "high", "高": "high", "high": "high",
	"中危": "medium", "中": "medium", "medium": "medium", "moderate": "medium",
	"低危": "low", "低": "low", "low": "low",
	"信息": "low", "info": "low", "informational": "low",
}

// normalizeFindingInput 对 report_finding 入参做容错归一。
func normalizeFindingInput(in json.RawMessage) json.RawMessage {
	obj, ok := decodeFindingObject(in)
	if !ok {
		return in
	}
	// 外壳：{"finding":{...}} / {"data":{...}} / {"fact":{...}}
	for _, shell := range []string{"finding", "data", "fact", "vulnerability"} {
		if raw, exists := obj[shell]; exists {
			if inner, ok := decodeFindingObject(raw); ok && !hasAnyKey(obj, "vulnclass", "name", "summary", "evidence") {
				obj = inner
				break
			}
		}
	}
	// 别名折叠（标准键已存在时不覆盖）。
	for k, raw := range obj {
		std, hit := findingFieldAliases[strings.ToLower(strings.ReplaceAll(k, "_", ""))]
		if !hit {
			// 也尝试原文小写（含下划线的别名，如 vuln_class）
			std, hit = findingFieldAliases[k]
		}
		if !hit || std == k {
			continue
		}
		if _, exists := obj[std]; !exists {
			obj[std] = raw
			delete(obj, k)
		}
	}
	// severity 归一。
	if raw, exists := obj["severity"]; exists {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if std, ok := severityAliases[strings.ToLower(strings.TrimSpace(s))]; ok {
				if b, err := json.Marshal(std); err == nil {
					obj["severity"] = b
				}
			}
		}
	} else {
		obj["severity"], _ = json.Marshal("medium")
	}
	// asset_ids：标量/逗号串 → 数字数组。
	if raw, exists := obj["asset_ids"]; exists {
		if arr, ok := coerceIDArray(raw); ok {
			obj["asset_ids"], _ = json.Marshal(arr)
		}
	}
	// 缺 summary：用 name，再用 evidence 首行。
	if !nonEmptyString(obj["summary"]) {
		if nonEmptyString(obj["name"]) {
			obj["summary"] = obj["name"]
		} else if s := firstLineString(obj["evidence"], 200); s != "" {
			obj["summary"], _ = json.Marshal(s)
		}
	}
	// 缺 vulnclass：按文本关键词推导，再做标准类别归一。
	if !nonEmptyString(obj["vulnclass"]) {
		if cls := deriveVulnClassFromText(rawString(obj["summary"]) + " " + rawString(obj["name"]) + " " + rawString(obj["evidence"])); cls != "" {
			obj["vulnclass"], _ = json.Marshal(cls)
		}
	} else if s := rawString(obj["vulnclass"]); s != "" {
		if norm := db.NormalizeVulnClass(s); norm != "" && norm != s {
			obj["vulnclass"], _ = json.Marshal(norm)
		}
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return in
	}
	return out
}

// deriveIdemRootCause 在模型未提供根因键时生成稳定幂等键：
// idem:<类别>:<URL 路径>:<参数名>。提取不到路径/参数时退回空串（不去重）。
func deriveIdemRootCause(vulnclass, text string) string {
	cls := db.NormalizeVulnClass(strings.TrimSpace(vulnclass))
	if cls == "" {
		return ""
	}
	path := extractURLPath(text)
	param := extractFirstParam(text)
	parts := []string{"idem", cls}
	if path != "" {
		parts = append(parts, path)
	}
	if param != "" {
		parts = append(parts, param)
	}
	if len(parts) == 2 {
		return ""
	}
	return strings.Join(parts, ":")
}

var (
	urlPathRe = regexp.MustCompile(`https?://[^/\s"'<>]+(/[^\s?"'<>]*)`)
	paramRe   = regexp.MustCompile(`[?&]([A-Za-z_][A-Za-z0-9_]{0,40})=`)
)

func extractURLPath(text string) string {
	if m := urlPathRe.FindStringSubmatch(text); len(m) == 2 {
		p := strings.TrimRight(m[1], "/.")
		if len(p) > 120 {
			p = p[:120]
		}
		return p
	}
	return ""
}

func extractFirstParam(text string) string {
	if m := paramRe.FindStringSubmatch(text); len(m) == 2 {
		return strings.ToLower(m[1])
	}
	return ""
}

// deriveVulnClassFromText 按关键词推导标准类别；推不出返回空串（不猜）。
func deriveVulnClassFromText(text string) string {
	s := strings.ToLower(text)
	rules := []struct {
		cls string
		kw  []string
	}{
		{"XSS", []string{"xss", "跨站脚本"}},
		{"SSRF", []string{"ssrf"}},
		{"XXE", []string{"xxe", "xml外部实体"}},
		{"RCE", []string{"rce", "命令执行", "代码执行", "远程代码"}},
		{"反序列化", []string{"反序列化", "deserial"}},
		{"任意文件上传", []string{"文件上传", "upload", "webshell"}},
		{"任意文件读取", []string{"文件读取", "路径穿越", "目录穿越", "lfi", "任意下载"}},
		{"未授权访问", []string{"未授权", "越权", "idor", "unauthorized", "访问控制"}},
		{"弱口令", []string{"弱口令", "默认密码", "默认凭据", "弱密码"}},
		{"注入", []string{"sql注入", "sqli", "注入", "injection"}},
		{"信息泄露", []string{"信息泄露", "敏感信息", "泄露", "leak"}},
		{"配置错误", []string{"cors", "配置错误", "misconfig"}},
	}
	for _, r := range rules {
		for _, kw := range r.kw {
			if strings.Contains(s, kw) {
				return r.cls
			}
		}
	}
	return ""
}

func decodeFindingObject(in json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(in) == 0 {
		return nil, false
	}
	// JSON 字符串包裹：先解成字符串再解对象。
	if in[0] == '"' {
		var s string
		if json.Unmarshal(in, &s) != nil {
			return nil, false
		}
		return decodeFindingObject(json.RawMessage(strings.TrimSpace(s)))
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(in, &obj) != nil || obj == nil {
		return nil, false
	}
	return obj, true
}

func hasAnyKey(obj map[string]json.RawMessage, keys ...string) bool {
	for _, k := range keys {
		if _, ok := obj[k]; ok {
			return true
		}
	}
	return false
}

func nonEmptyString(raw json.RawMessage) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != ""
}

func rawString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func firstLineString(raw json.RawMessage, max int) string {
	s := strings.TrimSpace(rawString(raw))
	if s == "" {
		return ""
	}
	line := s
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if runes := []rune(line); len(runes) > max {
		line = string(runes[:max]) + "…"
	}
	return line
}

// coerceIDArray 把 ["1"], [1], "1", "1,2" 统一成 []int64。
func coerceIDArray(raw json.RawMessage) ([]int64, bool) {
	var nums []int64
	if json.Unmarshal(raw, &nums) == nil {
		return nums, true
	}
	var strs []string
	if json.Unmarshal(raw, &strs) == nil {
		out := make([]int64, 0, len(strs))
		for _, s := range strs {
			if n, ok := parseID(s); ok {
				out = append(out, n)
			}
		}
		return out, true
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return []int64{n}, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		out := []int64{}
		for _, part := range strings.Split(s, ",") {
			if v, ok := parseID(part); ok {
				out = append(out, v)
			}
		}
		return out, true
	}
	return nil, false
}

func parseID(s string) (int64, bool) {
	var n int64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	return n, err == nil && n > 0
}
