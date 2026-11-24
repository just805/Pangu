package agent

// 微信小程序渗透能力：wxapkg 解包 + 解包产物静态审计（原生 Go 工具）。
//
// 解包只读本地 .wxapkg、把产物写到任务目录；审计纯读。二者都不发网络请求——
// 云开发/开放能力测试仍走既有 http_request/session_login，流量留痕口径不变。

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	actool "github.com/Autumn-27/norma/tool"
)

const (
	mpMaxFiles      = 20000
	mpMaxFileSize   = 64 << 20
	mpMaxTotalBytes = 512 << 20
	mpMaxNameLen    = 4096

	mpAuditMaxFiles = 5000
	mpAuditMaxBytes = 2 << 20
	mpEndpointCap   = 200
	mpSecretCap     = 100
	mpPreviewKeep   = 40
	mpUnpackListCap = 200
)

type mpRawEntry struct {
	rawName []byte
	offset  uint32
	size    uint32
}

type mpUnpackResult struct {
	OutputDir     string   `json:"output_dir"`
	FileCount     int      `json:"file_count"`
	Files         []string `json:"files"`
	TotalBytes    int64    `json:"total_bytes"`
	AppConfig     bool     `json:"app_config"`
	ProjectConfig bool     `json:"project_config"`
	SourceMaps    []string `json:"sourcemaps,omitempty"`
	Truncated     bool     `json:"truncated,omitempty"`
}

// miniprogram_unpack：解包标准 wxapkg（v1，明文 / 单字节 XOR）。写盘工具，非并发。
func (t *ToolSet) miniprogramUnpack() actool.CoreTool {
	return writeTool("miniprogram_unpack",
		"解包微信小程序 .wxapkg 到目录（支持标准 v1 明文与单字节 XOR 包；返回文件清单、"+
			"app-config/project.config 是否存在、sourcemap 列表）。不支持的变体会明确报错，"+
			"请改用 wxappUnpacker/unveilr。解包产物可用 miniprogram_audit 继续做接口/密钥审计。",
		obj(map[string]any{
			"input_path": str(".wxapkg 路径（绝对路径，或相对任务工作目录的路径）"),
			"output_dir": str("解包输出目录（可选；默认 <任务目录>/miniprogram-unpack）"),
		}, "input_path"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				InputPath string `json:"input_path"`
				OutputDir string `json:"output_dir"`
			}
			_ = json.Unmarshal(in, &a)
			inPath, err := t.resolveMPPath(a.InputPath)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			outDir, err := t.resolveMPOutputDir(a.OutputDir)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			res, err := unpackWxapkgFile(inPath, outDir)
			if err != nil {
				return actool.Errorf("解包失败: " + err.Error()), nil
			}
			return jsonResult(res)
		})
}

// miniprogram_audit：扫描解包目录/单文件，抽取接口、硬编码密钥、云开发信息与
// 敏感身份参数。纯读工具，并发安全。
func (t *ToolSet) miniprogramAudit() actool.CoreTool {
	return readTool("miniprogram_audit",
		"扫描微信小程序解包产物（目录或单文件），抽取接口 URL、wx.cloud 云环境/云函数、"+
			"硬编码密钥（AppSecret/AK/Bearer/私钥，片段脱敏）、appid、sourcemap 与 openid 等"+
			"敏感身份参数，并给出对应的 43 类漏洞排查提示。不发网络请求。",
		obj(map[string]any{
			"path":           str("解包目录或单个文件路径（绝对路径，或相对任务工作目录的路径）"),
			"max_files":      intp("最多扫描文件数（默认 5000）"),
			"max_file_bytes": intp("单文件大小上限（默认 2MB，超出跳过）"),
		}, "path"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Path         string `json:"path"`
				MaxFiles     int    `json:"max_files"`
				MaxFileBytes int    `json:"max_file_bytes"`
			}
			_ = json.Unmarshal(in, &a)
			p, err := t.resolveMPPath(a.Path)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			res, err := auditMiniProgram(p, a.MaxFiles, int64(a.MaxFileBytes))
			if err != nil {
				return actool.Errorf("审计失败: " + err.Error()), nil
			}
			return jsonResult(res)
		})
}

// ── 路径解析 ─────────────────────────────────────────────────────────────

func (t *ToolSet) resolveMPPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("路径必填")
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	if t.runDir == "" {
		return "", fmt.Errorf("相对路径需要任务工作目录；请传绝对路径")
	}
	return filepath.Join(t.runDir, p), nil
}

func (t *ToolSet) resolveMPOutputDir(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		if t.runDir != "" {
			return filepath.Join(t.runDir, "miniprogram-unpack"), nil
		}
		return filepath.Join(os.TempDir(), "pangu-miniprogram"), nil
	}
	if !filepath.IsAbs(p) {
		if t.runDir == "" {
			return "", fmt.Errorf("相对 output_dir 需要任务工作目录；请传绝对路径")
		}
		p = filepath.Join(t.runDir, p)
	}
	p = filepath.Clean(p)
	if t.runDir != "" {
		root := filepath.Clean(t.runDir)
		if p != root && !strings.HasPrefix(p, root+string(os.PathSeparator)) {
			return "", fmt.Errorf("output_dir 必须位于任务工作目录内")
		}
	}
	return p, nil
}

// ── wxapkg 解包 ──────────────────────────────────────────────────────────

func unpackWxapkgFile(inputPath, outputDir string) (*mpUnpackResult, error) {
	buf, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, err
	}
	if len(buf) < 18 || buf[0] != 0xBE || buf[13] != 0xED {
		return nil, fmt.Errorf("不是标准 wxapkg（头标记不符），可能是不支持的变体；请尝试 wxappUnpacker/unveilr")
	}
	fileCount := int(binary.BigEndian.Uint32(buf[14:18]))
	if fileCount <= 0 || fileCount > mpMaxFiles {
		return nil, fmt.Errorf("文件数异常(%d)，可能是加密/变体包", fileCount)
	}
	entries, err := parseMPIndex(buf, fileCount)
	if err != nil {
		return nil, err
	}
	indexInfoLength := int(binary.BigEndian.Uint32(buf[5:9]))
	dataStart := 14 + indexInfoLength
	key, names, err := inferMPKey(entries, len(buf), dataStart)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, err
	}
	res := &mpUnpackResult{OutputDir: outputDir}
	for i, e := range entries {
		name := names[i]
		rel := strings.TrimPrefix(filepath.FromSlash(name), string(os.PathSeparator))
		rel = strings.TrimPrefix(rel, "/")
		clean := filepath.Clean(rel)
		if strings.Contains(name, "..") || clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return nil, fmt.Errorf("拒绝非法路径: %s", name)
		}
		start, ok := mpDataRange(int(e.offset), int(e.size), len(buf), dataStart)
		if !ok {
			return nil, fmt.Errorf("文件 %s 数据越界", name)
		}
		data := make([]byte, e.size)
		copy(data, buf[start:start+int(e.size)])
		if key != 0 {
			for j := range data {
				data[j] ^= key
			}
		}
		dest := filepath.Join(outputDir, clean)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return nil, err
		}
		res.TotalBytes += int64(len(data))
		res.Files = append(res.Files, name)
		switch clean {
		case "app-config.json":
			res.AppConfig = true
		case "project.config.json":
			res.ProjectConfig = true
		}
		if strings.HasSuffix(strings.ToLower(clean), ".map") {
			res.SourceMaps = append(res.SourceMaps, name)
		}
	}
	res.FileCount = len(res.Files)
	if len(res.Files) > mpUnpackListCap {
		res.Files = res.Files[:mpUnpackListCap]
		res.Truncated = true
	}
	return res, nil
}

func parseMPIndex(buf []byte, fileCount int) ([]mpRawEntry, error) {
	p := 18
	out := make([]mpRawEntry, 0, fileCount)
	for i := 0; i < fileCount; i++ {
		if p+4 > len(buf) {
			return nil, fmt.Errorf("索引越界")
		}
		nameLen := int(binary.BigEndian.Uint32(buf[p:]))
		p += 4
		if nameLen <= 0 || nameLen > mpMaxNameLen || p+nameLen+8 > len(buf) {
			return nil, fmt.Errorf("文件名长度异常(%d)", nameLen)
		}
		raw := append([]byte(nil), buf[p:p+nameLen]...)
		p += nameLen
		off := binary.BigEndian.Uint32(buf[p:])
		p += 4
		size := binary.BigEndian.Uint32(buf[p:])
		p += 4
		if size > mpMaxFileSize {
			return nil, fmt.Errorf("单文件过大(%d)", size)
		}
		out = append(out, mpRawEntry{rawName: raw, offset: off, size: size})
	}
	return out, nil
}

// inferMPKey 用「全部文件名以 / 开头 + 数据范围合法 + 无重复」做自校验，逐 key
// 试解，命中即采用；0 即明文包。
func inferMPKey(entries []mpRawEntry, fileLen, dataStart int) (byte, []string, error) {
	for k := 0; k < 256; k++ {
		key := byte(k)
		names := make([]string, len(entries))
		seen := make(map[string]bool, len(entries))
		ok := true
		var total int64
		for i, e := range entries {
			name := decodeMPName(e.rawName, key)
			if !strings.HasPrefix(name, "/") {
				ok = false
				break
			}
			clean := path.Clean(name)
			// 拒绝任何含 ".." 的名字（path.Clean 会把 "/../x" 规整成 "/x"，
			// 单看 clean 拦不住，必须在原始名字上判断）。
			if strings.Contains(name, "..") || seen[clean] {
				ok = false
				break
			}
			seen[clean] = true
			total += int64(e.size)
			if total > mpMaxTotalBytes {
				ok = false
				break
			}
			if _, valid := mpDataRange(int(e.offset), int(e.size), fileLen, dataStart); !valid {
				ok = false
				break
			}
			names[i] = name
		}
		if ok {
			return key, names, nil
		}
	}
	return 0, nil, fmt.Errorf("无法识别包加密方式（0–255 XOR 均未通过校验）；请尝试 wxappUnpacker/unveilr")
}

func decodeMPName(raw []byte, key byte) string {
	b := make([]byte, len(raw))
	for i, c := range raw {
		b[i] = c ^ key
	}
	s := string(b)
	if strings.HasPrefix(s, "%2F") || strings.HasPrefix(s, "%2f") {
		if u, err := url.PathUnescape(s); err == nil {
			s = u
		}
	}
	return s
}

// mpDataRange 返回文件数据的实际起点。offset 可能是绝对偏移（主流实现），也可能
// 是相对 dataStart 的偏移（部分变体），两种都尝试。
func mpDataRange(offset, size, fileLen, dataStart int) (int, bool) {
	if size < 0 || offset < 0 {
		return 0, false
	}
	if offset >= 14 && offset+size <= fileLen {
		return offset, true
	}
	alt := dataStart + offset
	if alt >= dataStart && alt+size <= fileLen {
		return alt, true
	}
	return 0, false
}

// ── 静态审计 ─────────────────────────────────────────────────────────────

type mpEndpointHit struct {
	URL  string `json:"url"`
	File string `json:"file"`
	Line int    `json:"line"`
}

type mpSecretHit struct {
	Type    string `json:"type"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Preview string `json:"preview"`
}

type mpCloudInfo struct {
	Envs      []string `json:"envs"`
	Functions []string `json:"functions"`
}

type mpAuditResult struct {
	Root            string          `json:"root"`
	FilesScanned    int             `json:"files_scanned"`
	Skipped         int             `json:"skipped,omitempty"`
	Endpoints       []mpEndpointHit `json:"endpoints"`
	Secrets         []mpSecretHit   `json:"secrets"`
	AppIDs          []string        `json:"appids"`
	Cloud           mpCloudInfo     `json:"cloud"`
	SourceMaps      []string        `json:"sourcemaps"`
	SensitiveParams []string        `json:"sensitive_params"`
	Hints           []string        `json:"hints"`
}

var (
	mpURLRe       = regexp.MustCompile(`https?://[^\s"'<>()\\]+`)
	mpRequestRe   = regexp.MustCompile(`(?:wx\.(?:request|uploadFile|downloadFile)|url)\s*[:=]\s*["']([^"']+)["']`)
	mpCallFuncRe  = regexp.MustCompile(`callFunction\s*\(\s*\{[^}]*name\s*:\s*["']([^"']+)["']`)
	mpCloudEnvRe  = regexp.MustCompile(`cloud\.init\s*\(\s*\{[^}]*env\s*:\s*["']([^"']+)["']`)
	mpAppIDRe     = regexp.MustCompile(`\bwx[a-z0-9]{16}\b`)
	mpSecretRe    = regexp.MustCompile(`(?i)(appsecret|app_secret|secretkey|secret_key|accesskey|access_key|akid|api[_-]?key|bearer\s+[a-z0-9._-]{12,}|-----BEGIN [A-Z ]+PRIVATE KEY-----)`)
	mpSourceMapRe = regexp.MustCompile(`sourceMappingURL=([^\s"'*]+)`)
	mpSensitiveRe = regexp.MustCompile(`(?i)\b(openid|unionid|session_key|encryptedData|iv)\b`)
)

func auditMiniProgram(root string, maxFiles int, maxFileBytes int64) (*mpAuditResult, error) {
	if maxFiles <= 0 {
		maxFiles = mpAuditMaxFiles
	}
	if maxFileBytes <= 0 {
		maxFileBytes = mpAuditMaxBytes
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	res := &mpAuditResult{
		Root:            root,
		Endpoints:       []mpEndpointHit{},
		Secrets:         []mpSecretHit{},
		AppIDs:          []string{},
		Cloud:           mpCloudInfo{Envs: []string{}, Functions: []string{}},
		SourceMaps:      []string{},
		SensitiveParams: []string{},
		Hints:           []string{},
	}
	seenURL := map[string]bool{}
	seenSecret := map[string]bool{}
	seenApp := map[string]bool{}
	seenEnv := map[string]bool{}
	seenFn := map[string]bool{}
	seenSM := map[string]bool{}
	seenParam := map[string]bool{}

	addEndpoint := func(u, file string, line int) {
		u = strings.TrimSpace(u)
		if u == "" || seenURL[u] || len(res.Endpoints) >= mpEndpointCap {
			return
		}
		seenURL[u] = true
		res.Endpoints = append(res.Endpoints, mpEndpointHit{URL: u, File: file, Line: line})
	}
	addSecret := func(kind, preview, file string, line int) {
		if len(res.Secrets) >= mpSecretCap {
			return
		}
		key := kind + "|" + preview
		if seenSecret[key] {
			return
		}
		seenSecret[key] = true
		res.Secrets = append(res.Secrets, mpSecretHit{Type: kind, File: file, Line: line, Preview: preview})
	}
	addString := func(dst *[]string, seen map[string]bool, v string, cap int) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] || len(*dst) >= cap {
			return
		}
		seen[v] = true
		*dst = append(*dst, v)
	}

	scanFile := func(p string) {
		fi, err := os.Stat(p)
		if err != nil || fi.Size() > maxFileBytes {
			res.Skipped++
			return
		}
		f, err := os.Open(p)
		if err != nil {
			res.Skipped++
			return
		}
		defer f.Close()
		res.FilesScanned++
		rel := p
		if r, err := filepath.Rel(root, p); err == nil {
			rel = r
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		line := 0
		for sc.Scan() {
			line++
			text := sc.Text()
			for _, m := range mpURLRe.FindAllString(text, -1) {
				addEndpoint(m, rel, line)
			}
			for _, m := range mpRequestRe.FindAllStringSubmatch(text, -1) {
				addEndpoint(m[1], rel, line)
			}
			for _, m := range mpCallFuncRe.FindAllStringSubmatch(text, -1) {
				addString(&res.Cloud.Functions, seenFn, m[1], 100)
			}
			for _, m := range mpCloudEnvRe.FindAllStringSubmatch(text, -1) {
				addString(&res.Cloud.Envs, seenEnv, m[1], 50)
			}
			for _, m := range mpAppIDRe.FindAllString(text, -1) {
				addString(&res.AppIDs, seenApp, m, 50)
			}
			if loc := mpSecretRe.FindStringIndex(text); loc != nil {
				kind := strings.ToLower(mpSecretRe.FindString(text))
				tail := text[loc[1]:]
				if len(tail) > mpPreviewKeep {
					tail = tail[:mpPreviewKeep]
				}
				addSecret(kind, redactMPSecret(tail), rel, line)
			}
			for _, m := range mpSourceMapRe.FindAllStringSubmatch(text, -1) {
				addString(&res.SourceMaps, seenSM, m[1], 50)
			}
			for _, m := range mpSensitiveRe.FindAllStringSubmatch(text, -1) {
				addString(&res.SensitiveParams, seenParam, strings.ToLower(m[1]), 20)
			}
		}
	}

	if info.IsDir() {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if res.FilesScanned >= maxFiles {
				res.Skipped++
				return nil
			}
			if mpSkipExt(d.Name()) {
				return nil
			}
			scanFile(p)
			return nil
		})
	} else {
		scanFile(root)
	}

	if len(res.Secrets) > 0 {
		res.Hints = append(res.Hints, "发现硬编码密钥/凭据：对照 43 类第 3 条，确认 AppSecret 能否换 openid、AK 能否访问云资源（高危）")
	}
	if len(res.Cloud.Envs) > 0 || len(res.Cloud.Functions) > 0 {
		res.Hints = append(res.Hints, "云开发面：优先测云函数未授权/越权（34–35）、云数据库/存储未授权（36–37）、环境隔离（38）")
	}
	if len(res.Endpoints) > 0 {
		res.Hints = append(res.Hints, "接口清单：优先测未授权（6）、水平/纵向越权（7–8）、IDOR（9）；用 http_request 复现并绑定流量")
	}
	if len(res.SensitiveParams) > 0 {
		res.Hints = append(res.Hints, "出现 openid/unionid/session_key：优先验证身份参数越权（8）与手机号解密接口滥用（40）")
	}
	if len(res.SourceMaps) > 0 {
		res.Hints = append(res.Hints, "存在 sourcemap：可还原源码，扩大接口与密钥发现（2）")
	}
	return res, nil
}

func redactMPSecret(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "***"
	}
	r := []rune(s)
	if len(r) <= 6 {
		return "***"
	}
	return string(r[:6]) + "…"
}

func mpSkipExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".bmp",
		".mp3", ".mp4", ".wav", ".ogg", ".woff", ".woff2", ".ttf", ".otf", ".eot",
		".so", ".dll", ".exe", ".zip", ".gz", ".tar", ".wxapkg", ".apk", ".dex", ".jar":
		return true
	}
	return false
}
