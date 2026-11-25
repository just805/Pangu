package agent

import "testing"

// CVSS v3.1 官方计算器给出的基准用例。
func TestParseCVSS31KnownScores(t *testing.T) {
	cases := []struct {
		vector   string
		score    float64
		severity string
	}{
		// 9.8：网络、低复杂度、无权限、无交互、机密/完整/可用全高
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8, "critical"},
		// 6.1：作用域变化 + 需要交互 + 低影响
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:C/C:L/I:L/A:N", 6.1, "medium"},
		// 7.8：本地 + 低权限
		{"CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:H", 7.8, "high"},
		// 0.0：三项影响都无
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N", 0.0, "none"},
		// 兼容 3.0 前缀
		{"CVSS:3.0/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8, "critical"},
	}
	for _, c := range cases {
		score, sev, ok := ParseCVSS31(c.vector)
		if !ok {
			t.Errorf("%s 应解析成功", c.vector)
			continue
		}
		if score != c.score {
			t.Errorf("%s 分数应为 %.1f，实际 %.1f", c.vector, c.score, score)
		}
		if sev != c.severity {
			t.Errorf("%s 档位应为 %s，实际 %s", c.vector, c.severity, sev)
		}
	}
}

func TestParseCVSS31RejectsIncomplete(t *testing.T) {
	bad := []string{
		"",
		"CVSS:3.1/AV:N/AC:L/PR:N",                                  // 缺项
		"CVSS:3.1/AV:X/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",             // AV 非法
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:Z/C:H/I:H/A:H",             // S 非法
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H/TE:X",        // 多了时间指标也无妨？这里只校验基础项，仍应能解析
		"CVSS:2.0/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",             // 版本不对
		"AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",                      // 缺版本前缀
	}
	for _, v := range bad {
		_, _, ok := ParseCVSS31(v)
		// 最后一组是「基础项齐全但多一个时间指标」，CVSS 允许扩展指标，
		// 解析器只取基础项，应当成功；其余都应失败。
		if v == "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H/TE:X" {
			if !ok {
				t.Errorf("%q 含扩展指标也应解析成功（只取基础项）", v)
			}
			continue
		}
		if ok {
			t.Errorf("%q 不应解析成功", v)
		}
	}
}

func TestCvssSeverity(t *testing.T) {
	cases := map[float64]string{
		0: "none", 1.0: "low", 3.9: "low", 4.0: "medium",
		6.9: "medium", 7.0: "high", 8.9: "high", 9.0: "critical", 10: "critical",
	}
	for score, want := range cases {
		if got := cvssSeverity(score); got != want {
			t.Errorf("cvssSeverity(%.1f) = %s, want %s", score, got, want)
		}
	}
}
