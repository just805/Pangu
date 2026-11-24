package report

import (
	"strings"
	"unicode/utf8"
)

// cleanText 让任意来源的文本可以安全写进 UTF-8 产物（Markdown / OOXML）：
//   - 非法 UTF-8 字节序列替换为 U+FFFD，避免下游按编码猜测时整段乱码；
//   - 去掉 XML 1.0 不允许的控制字符（\x00-\x08、\x0B、\x0C、\x0E-\x1F、\x7F），
//     否则 Word 会以「内容有问题」告警或静默丢字。
//
// 制表/换行/回车保留。幂等。
func cleanText(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\uFFFD")
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return r
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, s)
}

// truncateUTF8 把 s 截到不超过 maxBytes 字节，且不切断多字节字符。
// 按字节截断会把一个汉字的 UTF-8 序列劈成两半，得到非法 UTF-8——zip 因此
// 不置 UTF-8 标志位，Windows/WinRAR 按 CP437/GBK 解码就成乱码（实测 86/192 条命中）。
func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// normalizeLiteralNewlines 还原被模型写成字面转义（两个字符 "\" + "n"）的换行。
//
// agent 有时把 summary/evidence 的换行写成字面 "\n"，导出的 Word 里整段证据
// 就挤成一个超长段落、页面上还露着 "\n"（用户实测「格式问题严重」的主因之一）。
// 仅当字面 \n 的数量多于真实换行时才还原，避免误伤 JSON 字符串里的合法转义
// （与前端 components/evidence-view.tsx 的 normalizeEvidenceText 同口径）。
func normalizeLiteralNewlines(s string) string {
	if !strings.Contains(s, `\n`) {
		return s
	}
	literal := strings.Count(s, `\n`)
	if literal <= strings.Count(s, "\n") {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(s, `\r\n`, "\n"), `\n`, "\n"), `\t`, "\t")
}
