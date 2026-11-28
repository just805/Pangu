package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/just805/pangu/db"
)

func TestNormalizeFactVerdict(t *testing.T) {
	cases := map[string]string{
		"":               FactConfirmed,
		"confirmed":      FactConfirmed,
		"  Confirmed ":   FactConfirmed,
		"ruled_out":      FactRuledOut,
		"Ruled-Out":      FactRuledOut,
		"已排除":            FactRuledOut,
		"open_proof_gap": FactOpenProofGap,
		"未验证":            FactOpenProofGap,
		"待验证":            FactOpenProofGap,
		"nonsense":       "", // 无法识别时返回空串，由调用方报错
	}
	for in, want := range cases {
		if got := normalizeFactVerdict(in); got != want {
			t.Errorf("normalizeFactVerdict(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateFactVerdict(t *testing.T) {
	ev := "curl -s -o /dev/null -w '%{http_code}' https://t/x?id=1 -> 403 WAF blocked"

	// ruled_out 缺 control_point：必须打回，并提示改判 open_proof_gap。
	if msg := validateFactVerdict(FactRuledOut, "", ev, "observed"); msg == "" {
		t.Fatal("ruled_out 缺 control_point 应被拒绝")
	} else if !strings.Contains(msg, "open_proof_gap") {
		t.Errorf("提示应给出 open_proof_gap 退路，实际: %s", msg)
	}

	// ruled_out 缺 evidence：打回。
	if msg := validateFactVerdict(FactRuledOut, "WAF 在参数层拦截", "", "observed"); msg == "" {
		t.Fatal("ruled_out 缺 evidence 应被拒绝")
	}

	// ruled_out 齐备：通过。
	if msg := validateFactVerdict(FactRuledOut, "WAF 在参数层拦截，第 2 步注入", ev, "observed"); msg != "" {
		t.Fatalf("完整的 ruled_out 应通过，实际: %s", msg)
	}

	// open_proof_gap 缺 evidence：打回。
	if msg := validateFactVerdict(FactOpenProofGap, "", "", "inferred"); msg == "" {
		t.Fatal("open_proof_gap 缺 evidence 应被拒绝")
	}

	// open_proof_gap 标 observed：自相矛盾，打回。
	if msg := validateFactVerdict(FactOpenProofGap, "", "只探了一次，没有确定性输出", "observed"); msg == "" {
		t.Fatal("open_proof_gap + observed 应被拒绝")
	} else if !strings.Contains(msg, "ruled_out") {
		t.Errorf("提示应指向 ruled_out 退路，实际: %s", msg)
	}

	// open_proof_gap + inferred：通过。
	if msg := validateFactVerdict(FactOpenProofGap, "", "只探了一次，没有确定性输出", "inferred"); msg != "" {
		t.Fatalf("open_proof_gap + inferred 应通过，实际: %s", msg)
	}

	// confirmed 不做额外要求（历史行为不变）。
	if msg := validateFactVerdict(FactConfirmed, "", "", ""); msg != "" {
		t.Fatalf("confirmed 应直接通过，实际: %s", msg)
	}
}

func TestSeverityFloorNote(t *testing.T) {
	mech := "HTTP/1.1 200 OK\n{\"code\":0,\"data\":[...]}"
	// 有机械证据 + low → 提示复核。
	if note := severityFloorNote("low", mech); note == "" {
		t.Fatal("有机械证据却报 low 应给出复核提示")
	}
	// 有机械证据 + high → 不提示（上限闸门负责）。
	if note := severityFloorNote("high", mech); note != "" {
		t.Errorf("high 不应有下限提示，实际: %s", note)
	}
	// 无机械证据 + low → 不提示（本来就是低危）。
	if note := severityFloorNote("low", "只看到版本号 banner"); note != "" {
		t.Errorf("无机械证据不应有下限提示，实际: %s", note)
	}
}

func TestVerdictOfNode(t *testing.T) {
	mk := func(kind string, payload string) *db.Node {
		return &db.Node{ID: 1, Kind: kind, Payload: json.RawMessage(payload)}
	}
	// 历史事实（无 verdict 字段）按 confirmed。
	if got := verdictOfNode(mk(db.KindFact, `{"summary":"指纹"}`)); got != FactConfirmed {
		t.Errorf("无 verdict 字段应为 confirmed，实际 %q", got)
	}
	// 非 fact 节点不参与三态。
	if got := verdictOfNode(mk(db.KindFinding, `{"verdict":"ruled_out"}`)); got != FactConfirmed {
		t.Errorf("finding 节点应为 confirmed，实际 %q", got)
	}
	// 正常读取。
	if got := verdictOfNode(mk(db.KindFact, `{"verdict":"ruled_out","control_point":"WAF"}`)); got != FactRuledOut {
		t.Errorf("应为 ruled_out，实际 %q", got)
	}
	// 坏 payload 不 panic，按 confirmed。
	if got := verdictOfNode(mk(db.KindFact, `{broken`)); got != FactConfirmed {
		t.Errorf("坏 payload 应为 confirmed，实际 %q", got)
	}
	// nil 安全。
	if got := verdictOfNode(nil); got != FactConfirmed {
		t.Errorf("nil 应为 confirmed，实际 %q", got)
	}
}

func TestDispatchParentVerdictError(t *testing.T) {
	ruledOut := &db.Node{
		ID:      42,
		Kind:    db.KindFact,
		Payload: json.RawMessage(`{"summary":"参数不允许注入，WAF 拦截","verdict":"ruled_out","control_point":"WAF"}`),
	}
	confirmed := &db.Node{
		ID:      43,
		Kind:    db.KindFact,
		Payload: json.RawMessage(`{"summary":"识别了技术栈 nginx 1.25"}`),
	}
	if msg := dispatchParentVerdictError([]*db.Node{confirmed}); msg != "" {
		t.Fatalf("confirmed 父节点不应被拒，实际: %s", msg)
	}
	msg := dispatchParentVerdictError([]*db.Node{confirmed, ruledOut})
	if msg == "" {
		t.Fatal("ruled_out 父节点应被拒绝")
	}
	if !strings.Contains(msg, "42") {
		t.Errorf("提示应包含被拒节点 id，实际: %s", msg)
	}
	// 空集合安全。
	if msg := dispatchParentVerdictError(nil); msg != "" {
		t.Fatalf("空集合不应报错，实际: %s", msg)
	}
}
