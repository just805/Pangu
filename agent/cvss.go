package agent

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ═══ M-11：CVSS v3.1 基础评分 ═══
//
// 为什么不引第三方库：平台「手写零依赖」的既有取向（报告 OOXML 同理），且 CVSS
// 基础分是一段确定性的公开公式，自己实现可以逐项单测。
//
// 为什么重要：定级此前完全由模型自评——同一个漏洞，模型这次说 high 下次说 medium。
// 让模型给 vector、平台算分，severity 才有确定性锚点（与 M-04 的定级上下限互补）。

// cvssSeverity 把分数映射到严重度档位（CVSS v3.1 规范 §5）。
func cvssSeverity(score float64) string {
	switch {
	case score == 0:
		return "none"
	case score < 4.0:
		return "low"
	case score < 7.0:
		return "medium"
	case score < 9.0:
		return "high"
	default:
		return "critical"
	}
}

// roundupCVSS 是 CVSS v3.1 规范的 Roundup：向上取整到一位小数。
func roundupCVSS(input float64) float64 {
	intInput := int64(math.Round(input * 100000))
	if intInput%10000 == 0 {
		return float64(intInput) / 100000.0
	}
	return (float64(intInput/10000) + 1) / 10.0
}

// ParseCVSS31 解析 CVSS v3.1（兼容 3.0 向量串）基础指标并计算 base score。
// 返回分数、严重度档位（none/low/medium/high/critical）与是否解析成功。
// 任何指标缺失或取值非法都返回 ok=false——宁可明确失败，也不给一个错的分。
func ParseCVSS31(vector string) (float64, string, bool) {
	v := strings.TrimSpace(vector)
	if v == "" {
		return 0, "", false
	}
	parts := strings.Split(v, "/")
	if len(parts) < 9 {
		return 0, "", false
	}
	if !strings.HasPrefix(parts[0], "CVSS:3.") {
		return 0, "", false
	}
	m := make(map[string]string, len(parts)-1)
	for _, p := range parts[1:] {
		k, val, ok := strings.Cut(p, ":")
		if !ok || k == "" || val == "" {
			return 0, "", false
		}
		m[strings.ToUpper(strings.TrimSpace(k))] = strings.ToUpper(strings.TrimSpace(val))
	}

	// 基础指标权重（CVSS v3.1 §7.4）。
	avWeights := map[string]float64{"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2}
	acWeights := map[string]float64{"L": 0.77, "H": 0.44}
	uiWeights := map[string]float64{"N": 0.85, "R": 0.62}
	ciaWeights := map[string]float64{"H": 0.56, "L": 0.22, "N": 0.0}

	scope := m["S"]
	if scope != "U" && scope != "C" {
		return 0, "", false
	}
	av, ok1 := avWeights[m["AV"]]
	ac, ok2 := acWeights[m["AC"]]
	ui, ok3 := uiWeights[m["UI"]]
	c, ok4 := ciaWeights[m["C"]]
	i, ok5 := ciaWeights[m["I"]]
	a, ok6 := ciaWeights[m["A"]]
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 {
		return 0, "", false
	}
	// PR 依赖 S（作用域变化与否权重不同）。
	var pr float64
	switch m["PR"] {
	case "N":
		pr = 0.85
	case "L":
		if scope == "U" {
			pr = 0.62
		} else {
			pr = 0.68
		}
	case "H":
		if scope == "U" {
			pr = 0.27
		} else {
			pr = 0.5
		}
	default:
		return 0, "", false
	}

	iss := 1 - (1-c)*(1-i)*(1-a)
	var impact float64
	if scope == "U" {
		impact = 6.42 * iss
	} else {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	}
	exploitability := 8.22 * av * ac * pr * ui

	if impact <= 0 {
		return 0, "none", true
	}
	var base float64
	if scope == "U" {
		base = roundupCVSS(math.Min(impact+exploitability, 10))
	} else {
		base = roundupCVSS(math.Min(1.08*(impact+exploitability), 10))
	}
	return base, cvssSeverity(base), true
}

// cvssVectorSupportHint 是解析失败时回灌给模型的纠正提示。
func cvssVectorSupportHint(vector string) string {
	return fmt.Sprintf("cvss_vector 无法解析（收到 %q）：需要完整的 CVSS v3.1 基础指标串，"+
		"八项一个不能少，例如 CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H。"+
		"不确定时省略该字段——平台会按你给的 severity 走既有证据闸门，而不是给一个算错的分。",
		strconv.Quote(strings.TrimSpace(vector)))
}
