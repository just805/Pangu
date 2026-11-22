package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/just805/pangu/db"
)

func chainFixture() *db.AttackChain {
	return &db.AttackChain{
		Steps: []*db.AttackChainStep{
			{
				Seq: 1, FindingID: 11, Title: ".env 泄露数据库凭据", Severity: "high",
				VulnClass: "credential_leak", Status: "pending", AssetIDs: []int64{1},
				IntentID: 101, IntentSummary: "在 Web 根目录寻找敏感配置",
				Proofs: []db.AttackChainProof{
					{Kind: "fact", NodeID: 201, Label: "事实 #201：在 .env 中找到数据库口令"},
					{Kind: "evidence", Label: "证据：GET /.env => 200"},
				},
			},
			{
				Seq: 2, FindingID: 12, Title: "后台命令执行", Severity: "critical",
				VulnClass: "rce", Status: "confirmed", AssetIDs: []int64{1},
				IntentID: 102, IntentSummary: "用窃取的口令登录后台并执行命令",
				Proofs: []db.AttackChainProof{
					{Kind: "traffic", TrafficCount: 2, Label: "已绑定 2 条流量证据"},
				},
			},
		},
		Edges: []db.FindingChainEdge{{Src: 11, Dst: 12, Rel: "enables"}},
	}
}

func TestAttackChainMarkdownSection(t *testing.T) {
	in := TaskDocxInput{
		Title:       "渗透测试报告 — 攻击链测试",
		GeneratedAt: time.Now(),
		AssetNames:  map[int64]string{1: "portal.example.com"},
		AttackChain: chainFixture(),
	}
	md := TaskMarkdown(in)
	for _, want := range []string{"## 四、攻击路径", "后台命令执行", "证明点", "→ 步骤 2（推进）", "portal.example.com"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q\n%s", want, md)
		}
	}
	// 章节顺延：修复路线图必须是第六章。
	if !strings.Contains(md, "## 六、修复优先级路线图") {
		t.Fatal("remediation chapter should be renumbered to 六")
	}
}

func TestAttackChainMarkdownEmpty(t *testing.T) {
	md := TaskMarkdown(TaskDocxInput{Title: "t", GeneratedAt: time.Now()})
	if !strings.Contains(md, "## 四、攻击路径") || !strings.Contains(md, "未形成可识别的攻击链") {
		t.Fatalf("empty chain should be explained, got:\n%s", md)
	}
}

func TestAttackChainPNG(t *testing.T) {
	png := attackChainPNG(chainFixture(), map[int64]string{1: "portal.example.com"})
	if len(png) == 0 {
		t.Fatal("attack chain PNG should be generated (embedded CJK font required)")
	}
	// PNG 魔数
	if !bytes.HasPrefix(png, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("not a PNG: % x", png[:8])
	}
}

func TestBuildTaskDocxWithAttackChain(t *testing.T) {
	in := TaskDocxInput{
		Title:       "渗透测试报告 — 攻击链测试",
		GeneratedAt: time.Now(),
		AssetNames:  map[int64]string{1: "portal.example.com"},
		AttackChain: chainFixture(),
	}
	doc := BuildTaskDocx(in)
	var buf bytes.Buffer
	if err := doc.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty docx")
	}
}
