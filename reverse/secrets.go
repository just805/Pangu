package reverse

// 敏感信息提取：在二进制里做确定性扫描（不执行样本）。先抽取 ASCII / UTF-16LE
// 字符串，再用规则库匹配常见凭据形态；输出带偏移与上下文的候选，供 UI/报告
// 展示，最终是否算漏洞由人或 worker 验证后决定。

import (
	"regexp"
	"sort"
	"strings"
)

// Secret 是一条敏感信息候选。
type Secret struct {
	Rule       string  `json:"rule"`
	Match      string  `json:"match"`
	Context    string  `json:"context"`
	Offset     int64   `json:"offset"`
	Section    string  `json:"section,omitempty"`
	Confidence float64 `json:"confidence"`
}

type secretRule struct {
	name       string
	re         *regexp.Regexp
	group      int
	confidence float64
}

// 规则顺序即报告展示顺序；group 指定取哪个捕获组（0=整段）。
var secretRules = []secretRule{
	{"private_key", regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----`), 0, 0.99},
	{"aws_access_key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), 0, 0.95},
	{"github_token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,255}\b`), 0, 0.95},
	{"google_api_key", regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`), 0, 0.90},
	{"slack_token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`), 0, 0.90},
	{"stripe_key", regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[0-9A-Za-z]{16,}\b`), 0, 0.90},
	{"aliyun_access_key", regexp.MustCompile(`\bLTAI[0-9A-Za-z]{12,20}\b`), 0, 0.85},
	{"tencent_secret_id", regexp.MustCompile(`\bAKID[0-9A-Za-z]{32,36}\b`), 0, 0.85},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), 0, 0.85},
	{"bearer_token", regexp.MustCompile(`(?i)authorization\s*:\s*bearer\s+([A-Za-z0-9\-\._~+/]{10,})`), 1, 0.80},
	{"basic_auth", regexp.MustCompile(`(?i)authorization\s*:\s*basic\s+([A-Za-z0-9+/=]{8,})`), 1, 0.75},
	{"database_url", regexp.MustCompile(`\b(?:mysql|postgres(?:ql)?|mongodb(?:\+srv)?|redis|mssql|oracle)://[^\s"'<>]{6,}`), 0, 0.80},
	{"jdbc_url", regexp.MustCompile(`\bjdbc:[a-z]+://[^\s"'<>]{6,}`), 0, 0.80},
	{"aws_secret_key", regexp.MustCompile(`(?i)aws.{0,24}["']([A-Za-z0-9/+=]{40})["']`), 1, 0.70},
	{"internal_url", regexp.MustCompile(`https?://(?:10\.|192\.168\.|172\.(?:1[6-9]|2\d|3[01])\.|localhost|127\.0\.0\.1)[^\s"'<>]{0,200}`), 0, 0.60},
	{"generic_secret", regexp.MustCompile(`(?i)(?:api[_-]?key|apikey|secret[_-]?key|access[_-]?key|auth[_-]?token|token|password|passwd|pwd)\s*[:=]\s*["']?([A-Za-z0-9_\-\./+=]{8,80})["']?`), 1, 0.50},
}

const (
	maxSecrets         = 500
	maxInterestingText = 300
	maxURLs            = 200
	maxDomains         = 120
	minStringLen       = 6
)

type stringHit struct {
	value  string
	offset int64
	wide   bool
}

// scanStrings 流式抽取可打印字符串，避免把整个文件的所有字符串都驻留内存。
func scanStrings(data []byte, fn func(stringHit)) int {
	count := 0
	// ASCII
	start := -1
	for i := 0; i < len(data); i++ {
		b := data[i]
		if b >= 0x20 && b <= 0x7e || b == '\t' {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 && i-start >= minStringLen {
			fn(stringHit{value: string(data[start:i]), offset: int64(start)})
			count++
		}
		start = -1
	}
	if start >= 0 && len(data)-start >= minStringLen {
		fn(stringHit{value: string(data[start:]), offset: int64(start)})
		count++
	}
	// UTF-16LE（Windows 程序里大量字符串是宽字符）。按字节推进，兼容奇偶对齐。
	start = -1
	for i := 0; i+1 < len(data); {
		if data[i] >= 0x20 && data[i] <= 0x7e && data[i+1] == 0 {
			if start < 0 {
				start = i
			}
			i += 2
			continue
		}
		if start >= 0 && (i-start)/2 >= minStringLen {
			fn(stringHit{value: utf16leASCII(data[start:i]), offset: int64(start), wide: true})
			count++
		}
		start = -1
		i++
	}
	if start >= 0 && (len(data)-start)/2 >= minStringLen {
		fn(stringHit{value: utf16leASCII(data[start:]), offset: int64(start), wide: true})
		count++
	}
	return count
}

func utf16leASCII(b []byte) string {
	out := make([]byte, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		out = append(out, b[i])
	}
	return string(out)
}

// extractSecrets 扫描字符串并匹配规则；返回候选与按字符串统计的有趣片段/URL。
func extractSecrets(data []byte) (secrets []Secret, interesting, urls, domains []string, stringCount int) {
	seenSecret := map[string]bool{}
	seenInteresting := map[string]bool{}
	seenURL := map[string]bool{}
	seenDomain := map[string]bool{}
	urlRe := regexp.MustCompile(`https?://[^\s"'<>]{4,}`)
	domainRe := regexp.MustCompile(`\b(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}\b`)

	addSecret := func(h stringHit, r secretRule, m []int) {
		if len(secrets) >= maxSecrets || r.group*2+1 >= len(m) {
			return
		}
		s, e := m[r.group*2], m[r.group*2+1]
		if s < 0 || e <= s || e > len(h.value) {
			return
		}
		val := strings.TrimSpace(h.value[s:e])
		if len(val) < minStringLen || isPlaceholder(val) {
			return
		}
		key := r.name + "\x00" + val
		if seenSecret[key] {
			return
		}
		seenSecret[key] = true
		off := h.offset + int64(s)
		if h.wide {
			off = h.offset + int64(s*2)
		}
		secrets = append(secrets, Secret{
			Rule:       r.name,
			Match:      truncate(val, 200),
			Context:    stringContext(h.value, s, e),
			Offset:     off,
			Confidence: r.confidence,
		})
	}

	scanStrings(data, func(h stringHit) {
		stringCount++
		for _, r := range secretRules {
			for _, m := range r.re.FindAllStringSubmatchIndex(h.value, 20) {
				addSecret(h, r, m)
			}
		}
		if len(interesting) < maxInterestingText && isInterestingString(h.value) && !seenInteresting[h.value] {
			seenInteresting[h.value] = true
			interesting = append(interesting, truncate(h.value, 300))
		}
		for _, u := range urlRe.FindAllString(h.value, 5) {
			if len(urls) >= maxURLs || seenURL[u] {
				continue
			}
			seenURL[u] = true
			urls = append(urls, truncate(u, 300))
		}
		for _, d := range domainRe.FindAllString(h.value, 5) {
			dl := strings.ToLower(d)
			if len(domains) >= maxDomains || seenDomain[dl] || isFileExtension(dl) {
				continue
			}
			seenDomain[dl] = true
			domains = append(domains, dl)
		}
	})
	sort.SliceStable(secrets, func(i, j int) bool {
		if secrets[i].Confidence != secrets[j].Confidence {
			return secrets[i].Confidence > secrets[j].Confidence
		}
		return secrets[i].Offset < secrets[j].Offset
	})
	return secrets, interesting, urls, domains, stringCount
}

func stringContext(s string, start, end int) string {
	lo := start - 40
	if lo < 0 {
		lo = 0
	}
	hi := end + 80
	if hi > len(s) {
		hi = len(s)
	}
	ctx := strings.TrimSpace(s[lo:hi])
	ctx = strings.Join(strings.Fields(ctx), " ")
	return truncate(ctx, 240)
}

func isPlaceholder(v string) bool {
	l := strings.ToLower(strings.TrimSpace(v))
	if len(l) < minStringLen {
		return true
	}
	for _, p := range []string{"your_", "your-", "your ", "example", "sample", "placeholder", "changeme", "change_me", "xxxx", "****", "....", "<", ">", "${", "{{", "%s", "none", "null", "todo"} {
		if strings.Contains(l, p) {
			return true
		}
	}
	same := true
	for i := 1; i < len(l); i++ {
		if l[i] != l[0] {
			same = false
			break
		}
	}
	return same
}

func isInterestingString(s string) bool {
	l := strings.ToLower(s)
	for _, kw := range []string{
		"http://", "https://", ".dll", ".exe", ".pdb", ".config", ".json", ".xml",
		"password", "passwd", "secret", "token", "api_key", "apikey", "access_key",
		`c:\`, `\\`, `software\`, "powershell", "cmd.exe", "schtasks", "reg add",
		"mysql", "postgres", "mongodb", "redis", "aws", "azure", "aliyun", "tencent",
		"license", "update", "install", "upload", "download",
	} {
		if strings.Contains(l, kw) {
			return true
		}
	}
	return false
}

func isFileExtension(s string) bool {
	i := strings.LastIndexByte(s, '.')
	if i < 0 || i == len(s)-1 {
		return false
	}
	switch s[i+1:] {
	case "dll", "exe", "sys", "ocx", "cpl", "scr", "config", "json", "xml", "txt", "log", "pdb", "manifest":
		return true
	}
	return false
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// ScanText 对纯文本（如反编译产物）做规则扫描，供反编译后二次提取使用。
// 与二进制扫描共用同一套规则；Offset 是文本内字节偏移，Section 由调用方填。
func ScanText(text string) []Secret {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []Secret
	for _, r := range secretRules {
		for _, m := range r.re.FindAllStringSubmatchIndex(text, 50) {
			if r.group*2+1 >= len(m) {
				continue
			}
			s, e := m[r.group*2], m[r.group*2+1]
			if s < 0 || e <= s || e > len(text) {
				continue
			}
			val := strings.TrimSpace(text[s:e])
			if len(val) < minStringLen || isPlaceholder(val) {
				continue
			}
			key := r.name + "\x00" + val
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Secret{
				Rule:       r.name,
				Match:      truncate(val, 200),
				Context:    stringContext(text, s, e),
				Offset:     int64(s),
				Confidence: r.confidence,
			})
			if len(out) >= 200 {
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence != out[j].Confidence {
			return out[i].Confidence > out[j].Confidence
		}
		return out[i].Offset < out[j].Offset
	})
	return out
}
