package agent

import (
	"testing"

	"github.com/just805/pangu/db"
)

func TestAuditFindingEvidenceStrategies(t *testing.T) {
	// http_diff：baseline + proof 两条流量 → pass
	a := AuditFindingEvidence("high", "任意内容", []db.TrafficRef{{Role: "baseline"}, {Role: "proof"}})
	if a.Strategy != "http_diff" || a.Verdict != "pass" {
		t.Fatalf("baseline+proof 应 pass: %+v", a)
	}
	// 只有 proof → fail 并点名缺 baseline
	a = AuditFindingEvidence("high", "任意内容", []db.TrafficRef{{Role: "proof"}})
	if a.Verdict != "fail" || len(a.Missing) == 0 {
		t.Fatalf("缺 baseline 应 fail: %+v", a)
	}
	// 双身份
	ev := "身份A Cookie: a=1 读到 200；身份B Cookie: b=2 返回 403，确认越权"
	a = AuditFindingEvidence("high", ev, nil)
	if a.Strategy != "dual_identity" || a.Verdict != "pass" {
		t.Fatalf("双身份应 pass: %+v", a)
	}
	// 可重放命令
	ev = "curl -s 'https://x/api?id=1'\nHTTP/1.1 200 OK\n{\"name\":\"张三\"}"
	a = AuditFindingEvidence("high", ev, nil)
	if a.Strategy != "poc_replay" || a.Verdict != "pass" {
		t.Fatalf("命令+输出应 pass: %+v", a)
	}
	// 低危不因缺口判 fail
	a = AuditFindingEvidence("low", "一句话", nil)
	if a.Verdict == "fail" {
		t.Fatalf("低危不应 fail: %+v", a)
	}
}

func TestAuditNoteOnlyForGaps(t *testing.T) {
	if auditNote(FindingAudit{Verdict: "pass"}) != "" {
		t.Fatal("pass 不应有提示")
	}
	note := auditNote(FindingAudit{Verdict: "fail", Strategy: "poc_replay", Missing: []string{"缺少可重放命令"}})
	if note == "" {
		t.Fatal("fail 应给出缺口提示")
	}
}
