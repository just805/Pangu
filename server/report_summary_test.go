package server

import (
	"strings"
	"testing"
	"time"

	"github.com/just805/pangu/db"
	"github.com/just805/pangu/report"
)

// execSummaryDigest：严重度统计与 top 12 截断输入的形状正确。
func TestExecSummaryDigest(t *testing.T) {
	s := &Server{}
	findings := []*db.DBFinding{
		{Name: "弱口令登录后台", Severity: "critical", Summary: "admin/admin 登录成功，可读取全量订单数据", AssetIDs: []int64{7}},
		{Name: "CVE-2023-44487 Rapid Reset", Severity: "high", Summary: strings.Repeat("长摘要", 60)},
		{Name: "信息泄露", Severity: "low", Summary: "低危"},
	}
	in := report.TaskDocxInput{
		Title:      "渗透测试报告 — 演示任务",
		Goal:       "验证越权",
		Findings:   findings,
		AssetNames: map[int64]string{7: "admin.example.com"},
	}
	d := s.execSummaryDigest(in)
	for _, want := range []string{
		"渗透测试报告 — 演示任务",
		"验证越权",
		"严重(critical) 1", "高危(high) 1", "低危(low) 1",
		"弱口令登录后台", "admin.example.com",
		"CVE-2023-44487 Rapid Reset",
	} {
		if !strings.Contains(d, want) {
			t.Fatalf("digest 缺 %q:\n%s", want, d)
		}
	}
	// 长摘要被截断（… 结尾），top 排序严重度在前。
	if strings.Contains(d, strings.Repeat("长摘要", 60)) {
		t.Fatal("长摘要未截断")
	}
	if strings.Index(d, "弱口令登录后台") > strings.Index(d, "CVE-2023-44487") {
		t.Fatal("critical 应排在 high 前")
	}
}

// execSummaryKey：任务维度隔离。
func TestExecSummaryKey(t *testing.T) {
	if !strings.HasPrefix(execSummaryKey("42"), "exec_summary_task_42") {
		t.Fatal("key 形状错误")
	}
	_ = time.Now
}
