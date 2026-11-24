package agent

// 模型可控字符串进入 shell-out 工具（curl -H / -A）前的校验。
//
// 背景（上游 issue #51/#52 同类问题）：`auth_request` 的 header、`diff_responses`
// 的 header_a/header_b 由模型生成，最终以 `curl -H '<值>'` 形式执行。POSIX 单引号
// 只能防 shell 注入，防不了两种真正的问题：
//
//  1. 值里带 CR/LF → curl 把它拆成多个请求头（CWE-113 头注入），可以偷偷加
//     `X-Forwarded-For`、`Cookie` 之类；
//  2. 值以 `@` 开头 → curl 会把 `@` 后面的路径当文件读，把本地文件内容当请求头
//     发给目标（本地文件外泄）。
//
// 因此所有交给 curl 的 header/UA 值都必须满足：可打印 ASCII（0x20–0x7E）、
// 不以 `@` 开头、长度有上限。校验在工具入参阶段做，报错回给模型而不是发出请求。

import (
	"fmt"
	"strings"
)

const (
	maxHeaderValueBytes = 8 << 10 // 单个请求头 8KB，远大于正常值
	maxUserAgentBytes   = 1024    // UA 上限与浏览器/curl 实际可用值对齐
)

// validateCurlHeaderValue 校验一个将进入 `curl -H` 的完整头值（如
// "Authorization: Bearer xxx"）。空值合法（调用方跳过）。
func validateCurlHeaderValue(s string) error {
	if s == "" {
		return nil
	}
	return validatePrintableASCII(s, maxHeaderValueBytes, "请求头")
}

// normalizeToolUserAgent 校验并归一 User-Agent。首尾空白会被 trim；空串表示
// 使用平台默认 UA。拒绝 CR/LF/TAB/@ 前缀与超长值。
func normalizeToolUserAgent(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if err := validatePrintableASCII(s, maxUserAgentBytes, "User-Agent"); err != nil {
		return "", err
	}
	return s, nil
}

// validatePrintableASCII 逐字节检查：仅允许 0x20–0x7E。
// 报错带 0-based 偏移和 0xXX，方便模型定位是哪个字符写错了。
func validatePrintableASCII(s string, max int, what string) error {
	if len(s) > max {
		return fmt.Errorf("%s 过长：%d 字节（上限 %d）", what, len(s), max)
	}
	if strings.HasPrefix(s, "@") {
		return fmt.Errorf("%s 不能以 @ 开头：curl 会把 @ 后面的内容当本地文件读取并发送", what)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7e {
			hint := ""
			switch c {
			case '\r', '\n':
				hint = "（换行会把一个请求头拆成多个，属头注入）"
			case '\t':
				hint = "（制表符不可用于请求头）"
			}
			return fmt.Errorf("%s 第 %d 个字节 0x%02X 不是可打印 ASCII%s", what, i, c, hint)
		}
	}
	return nil
}
