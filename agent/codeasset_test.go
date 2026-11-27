package agent

import (
	"strings"
	"testing"

	"github.com/just805/pangu/db"
)

// ── 三种扫描器解析 ──────────────────────────────────────────

func TestParseSemgrepResults(t *testing.T) {
	raw := []byte(`{
	  "results": [
	    {"check_id":"go.lang.security.audit.sqli","path":"internal/dao/order.go",
	     "start":{"line":42},"end":{"line":44},
	     "extra":{"severity":"ERROR","message":"SQL string concat","lines":"db.Query(\"select * from t where id=\"+id)"}},
	    {"check_id":"go.lang.correctness.unused","path":"main.go",
	     "start":{"line":7},"end":{"line":7},
	     "extra":{"severity":"INFO","message":"unused var"}}
	  ]
	}`)
	rows, err := parseSemgrepResults(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("应解析 2 条，实际 %d", len(rows))
	}
	if rows[0].Severity != "high" {
		t.Errorf("ERROR 应归一为 high，实际 %q", rows[0].Severity)
	}
	if rows[1].Severity != "low" {
		t.Errorf("INFO 应归一为 low，实际 %q", rows[1].Severity)
	}
	if rows[0].FilePath != "internal/dao/order.go" || rows[0].LineStart == nil || *rows[0].LineStart != 42 {
		t.Errorf("路径/行号映射错误: %+v", rows[0])
	}
	if rows[0].Fingerprint == "" || rows[0].Fingerprint == rows[1].Fingerprint {
		t.Error("指纹必须存在且互不相同")
	}
}

// 凭据卫生：gitleaks 的 Secret 是明文密钥，绝不能落库。
func TestParseGitleaksMasksSecret(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE"
	raw := []byte(`[{"RuleID":"aws-access-key","Description":"AWS key","File":"config/prod.env",
	  "StartLine":3,"EndLine":3,"Secret":"` + secret + `"}]`)
	rows, err := parseGitleaksResults(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("应解析 1 条，实际 %d", len(rows))
	}
	if strings.Contains(rows[0].Snippet, secret) {
		t.Fatalf("明文密钥落库了：%q", rows[0].Snippet)
	}
	if !strings.Contains(rows[0].Snippet, "AKIA") {
		t.Errorf("应保留前 4 位便于复核，实际 %q", rows[0].Snippet)
	}
	if !strings.Contains(rows[0].Snippet, "脱敏") {
		t.Errorf("应标注已脱敏，实际 %q", rows[0].Snippet)
	}
	if rows[0].Severity != "high" {
		t.Errorf("泄露凭据默认 high，实际 %q", rows[0].Severity)
	}
}

func TestParseTrivyResults(t *testing.T) {
	raw := []byte(`{"Results":[
	  {"Target":"go.mod","Vulnerabilities":[
	    {"VulnerabilityID":"CVE-2024-0001","PkgName":"golang.org/x/net","InstalledVersion":"0.1.0",
	     "Severity":"CRITICAL","Title":"bad thing"},
	    {"VulnerabilityID":"CVE-2024-0002","PkgName":"golang.org/x/text","InstalledVersion":"0.2.0",
	     "Severity":"UNKNOWN","Title":"unknown"}]},
	  {"Target":"package-lock.json","Vulnerabilities":[
	    {"VulnerabilityID":"CVE-2024-0003","PkgName":"lodash","InstalledVersion":"4.0.0","Severity":"HIGH"}]}
	]}`)
	rows, err := parseTrivyResults(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("应解析 3 条，实际 %d", len(rows))
	}
	if rows[0].PackageEcosystem != "go" {
		t.Errorf("go.mod 应识别为 go 生态，实际 %q", rows[0].PackageEcosystem)
	}
	if rows[2].PackageEcosystem != "npm" {
		t.Errorf("package-lock.json 应识别为 npm 生态，实际 %q", rows[2].PackageEcosystem)
	}
	if rows[1].Severity != "info" {
		t.Errorf("UNKNOWN 应归一为 info，实际 %q", rows[1].Severity)
	}
	if rows[0].PackageName != "golang.org/x/net" || rows[0].RuleID != "CVE-2024-0001" {
		t.Errorf("组件/编号映射错误: %+v", rows[0])
	}
}

// 格式不对时必须报错而不是静默入库 0 条——否则模型会以为扫成功了。
func TestParseScanRejectsWrongFormat(t *testing.T) {
	if _, err := parseSemgrepResults([]byte(`{"foo":1}`)); err != nil {
		t.Errorf("空 results 应可解析（0 条），实际报错 %v", err)
	}
	if _, err := parseSemgrepResults([]byte(`not json`)); err == nil {
		t.Error("非 JSON 应报错")
	}
	if _, err := parseScanFile("nmap", "x.json"); err == nil {
		t.Error("未知扫描器应报错")
	}
}

// ── 折叠 ────────────────────────────────────────────────────

// critical/high 必须全留；低/中危按规则折叠，否则中型仓库一轮就能写进数万条。
func TestCollapseScanResults(t *testing.T) {
	var rows []db.CodeScanResult
	for i := 0; i < 10; i++ {
		rows = append(rows, db.CodeScanResult{Scanner: "semgrep", RuleID: "rule-high", Severity: "high"})
	}
	for i := 0; i < 10; i++ {
		rows = append(rows, db.CodeScanResult{Scanner: "semgrep", RuleID: "rule-low", Severity: "low"})
	}
	for i := 0; i < 5; i++ {
		rows = append(rows, db.CodeScanResult{Scanner: "semgrep", RuleID: "rule-med", Severity: "medium"})
	}
	kept, dropped := collapseScanResults(rows)
	if want := (10 - 3) + (5 - 3); dropped != want {
		t.Errorf("折叠数不符：dropped=%d，期望 %d", dropped, want)
	}
	var high, low, med int
	for _, r := range kept {
		switch r.Severity {
		case "high":
			high++
		case "low":
			low++
		case "medium":
			med++
		}
	}
	if high != 10 {
		t.Errorf("high 必须全留，实际 %d", high)
	}
	if low != 3 || med != 3 {
		t.Errorf("低/中危每规则应留 3 条，实际 low=%d med=%d", low, med)
	}
}

// ── 指纹 ────────────────────────────────────────────────────

func TestCodeFingerprintStableAndDiscriminating(t *testing.T) {
	line := 10
	base := db.CodeScanResult{Scanner: "semgrep", RuleID: "r1", FilePath: "a.go", LineStart: &line}
	f1 := codeFingerprint(base)
	f1b := codeFingerprint(base)
	if f1 != f1b {
		t.Error("同一输入指纹必须稳定，否则重复扫描会堆行")
	}
	changed := base
	other := 11
	changed.LineStart = &other
	if codeFingerprint(changed) == f1 {
		t.Error("不同行号必须产生不同指纹")
	}
	// 路径里带分隔符也不能撞键。
	a := db.CodeScanResult{Scanner: "semgrep", RuleID: "r|1", FilePath: "a.go"}
	b := db.CodeScanResult{Scanner: "semgrep", RuleID: "r", FilePath: "1|a.go"}
	if codeFingerprint(a) == codeFingerprint(b) {
		t.Error("分隔符拼键会导致撞键（这里用 sha256 应避免）")
	}
}

// ── 主机/关键词切分（在 db 包里，见 db/codeasset_test.go）──

func TestNormalizeScanSeverity(t *testing.T) {
	cases := map[string]string{
		"CRITICAL": "critical", "Error": "high", "WARNING": "medium",
		"info": "low", "UNKNOWN": "info", "": "info", "weird": "low",
	}
	for in, want := range cases {
		if got := normalizeScanSeverity(in); got != want {
			t.Errorf("normalizeScanSeverity(%q)=%q，期望 %q", in, got, want)
		}
	}
}
