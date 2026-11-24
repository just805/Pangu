package agent

import (
	"encoding/json"
	"testing"
)

func decodeFindingForTest(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(normalizeFindingInput(json.RawMessage(raw)), &m); err != nil {
		t.Fatalf("normalize 输出不可解析: %v", err)
	}
	return m
}

func TestNormalizeFindingInputAliases(t *testing.T) {
	m := decodeFindingForTest(t, `{
		"type":"sqli", "title":"登录注入", "desc":"登录接口可注入",
		"proof":"curl ... output", "risk":"高危", "assets":"3,7"
	}`)
	if m["vulnclass"] != "注入" {
		t.Fatalf("type 应折叠为 vulnclass 并归一: %v", m["vulnclass"])
	}
	if m["name"] != "登录注入" {
		t.Fatalf("title 应折叠为 name: %v", m["name"])
	}
	if m["summary"] != "登录接口可注入" {
		t.Fatalf("desc 应折叠为 summary: %v", m["summary"])
	}
	if m["evidence"] != "curl ... output" {
		t.Fatalf("proof 应折叠为 evidence: %v", m["evidence"])
	}
	if m["severity"] != "high" {
		t.Fatalf("中文严重度应转英文: %v", m["severity"])
	}
	ids, ok := m["asset_ids"].([]any)
	if !ok || len(ids) != 2 {
		t.Fatalf("逗号串资产应转数组: %v", m["asset_ids"])
	}
}

func TestNormalizeFindingInputUnwrapsStringAndShell(t *testing.T) {
	inner := `{"finding":{"vulnclass":"XSS","severity":"medium","summary":"反射型","evidence":"<script>"}}`
	quoted, _ := json.Marshal(inner)
	m := decodeFindingForTest(t, string(quoted))
	if m["vulnclass"] != "XSS" || m["summary"] != "反射型" {
		t.Fatalf("字符串包裹 + finding 外壳应被剥开: %v", m)
	}
}

func TestNormalizeFindingInputFallbacks(t *testing.T) {
	m := decodeFindingForTest(t, `{"name":"某接口未授权读取用户信息"}`)
	if m["severity"] != "medium" {
		t.Fatalf("缺 severity 应默认 medium: %v", m["severity"])
	}
	if m["summary"] != "某接口未授权读取用户信息" {
		t.Fatalf("缺 summary 应从 name 兜底: %v", m["summary"])
	}
	if m["vulnclass"] != "未授权访问" {
		t.Fatalf("缺 vulnclass 应按关键词推导: %v", m["vulnclass"])
	}
}

func TestNormalizeFindingInputIdempotentOnValid(t *testing.T) {
	raw := `{"vulnclass":"XSS","name":"n","severity":"high","summary":"s","evidence":"e","asset_ids":[1,2]}`
	var before, after map[string]any
	_ = json.Unmarshal([]byte(raw), &before)
	_ = json.Unmarshal(normalizeFindingInput(json.RawMessage(raw)), &after)
	for k, v := range before {
		got, ok := after[k]
		if !ok || !jsonEqual(v, got) {
			t.Fatalf("合法字段被改写: %s: %v -> %v", k, v, got)
		}
	}
}

func TestDeriveIdemRootCause(t *testing.T) {
	got := deriveIdemRootCause("注入", "GET https://data.example.com/zq/api/data_set/123/get?dataset_id=9 返回 200")
	if got != "idem:注入:/zq/api/data_set/123/get:dataset_id" {
		t.Fatalf("幂等键不符合预期: %q", got)
	}
	if deriveIdemRootCause("XSS", "没有 URL 的纯文本") != "" {
		t.Fatal("提取不到路径/参数时应返回空（不去重）")
	}
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
