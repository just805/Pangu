package agent

// ═══ M-02：代码资产与静态扫描工具 ═══
//
// 四个工具按「只落脚本 + 组命令，不自己发请求」的既有取向分工：
//
//	code_asset_register  写  登记仓库/源码包，返回 code_asset_id
//	code_scan_plan       读  生成 semgrep / gitleaks / trivy 命令（只给命令，不给结论）
//	code_scan_ingest     写  把扫描器输出的 JSON 解析入库（不接受模型手写结果）
//	code_asset_context   读  按运行时 URL/域名反查代码资产与相关 sink 位置
//
// 为什么不允许模型直接提交扫描结果：工具输出是判据，模型自述不是。入库入口只认
// 扫描器写出的文件，指纹由本包按「扫描器|规则|文件|行|包」算——模型无法伪造去重键。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	actool "github.com/Autumn-27/norma/tool"

	"github.com/just805/pangu/db"
	"github.com/just805/pangu/intercept"
)

const (
	// codeScanFileLimit 是扫描结果 JSON 的读取上限。中型仓库的 semgrep 输出可以到几十 MB，
	// 再大就是跑错了目标（比如扫了整个 $HOME），直接拒绝比 OOM 好。
	codeScanFileLimit = 64 << 20
	// codeScanMaxRows 是单次入库的行数上限。SAST 误报多、条数大，全量入库会让表失去
	// 可查询性；超出部分按「先保高危、其余每规则留样本」折叠，并在返回里如实报丢弃数。
	codeScanMaxRows = 2000
	// codeSnippetCap 是单条代码片段的字符上限。
	codeSnippetCap = 500
)

// ── 指纹与折叠 ──────────────────────────────────────────────

// codeFingerprint 是去重键：同一资产同扫描器同规则同位置视为同一条结果。
//
// 每个字段带**长度前缀**再拼接，不是直接拿分隔符串起来：
// RuleID="r|1" + FilePath="a.go" 与 RuleID="r" + FilePath="1|a.go" 用 "|" 拼接会得到
// 同一个串，进而被判成同一条结果、把真实命中静默吞掉。sha256 只保证哈希不撞，
// 不解决拼接歧义。
func codeFingerprint(r db.CodeScanResult) string {
	var b strings.Builder
	for _, f := range []string{r.Scanner, r.RuleID, r.FilePath,
		itoaPtr(r.LineStart), itoaPtr(r.LineEnd), r.PackageName, r.PackageVersion} {
		fmt.Fprintf(&b, "%d:%s|", len(f), f)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:24]
}

func itoaPtr(p *int) string {
	if p == nil {
		return ""
	}
	return fmt.Sprint(*p)
}

// collapseScanResults 把结果折到上限内：critical/high 全留，其余每规则最多留 3 条样本。
//
// 不做这个折叠的话，一个中型仓库一轮 semgrep 就能写进数万条 medium/low，
// 之后任何按资产的查询都会退化成全表扫。
func collapseScanResults(rows []db.CodeScanResult) (kept []db.CodeScanResult, dropped int) {
	perRule := map[string]int{}
	for _, r := range rows {
		high := r.Severity == "critical" || r.Severity == "high"
		if !high {
			key := r.Scanner + "|" + r.RuleID
			if perRule[key] >= 3 {
				dropped++
				continue
			}
			perRule[key]++
		}
		if len(kept) >= codeScanMaxRows {
			dropped++
			continue
		}
		kept = append(kept, r)
	}
	return kept, dropped
}

// normalizeScanSeverity 把各扫描器的等级归一到五档。
func normalizeScanSeverity(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "CRITICAL", "CRIT":
		return "critical"
	case "HIGH", "ERROR", "MAJOR":
		return "high"
	case "MEDIUM", "WARNING", "MODERATE":
		return "medium"
	case "LOW", "INFO", "NOTE", "MINOR":
		return "low"
	case "UNKNOWN", "":
		return "info"
	}
	return "low"
}

func clipSnippet(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > codeSnippetCap {
		return string(r[:codeSnippetCap]) + "…"
	}
	return s
}

func intPtr(v int) *int {
	if v <= 0 {
		return nil
	}
	return &v
}

// ── 三种扫描器输出解析 ──────────────────────────────────────

// parseSemgrepResults 解析 `semgrep --json` 的输出。
func parseSemgrepResults(raw []byte) ([]db.CodeScanResult, error) {
	var doc struct {
		Results []struct {
			CheckID string `json:"check_id"`
			Path    string `json:"path"`
			Start   struct {
				Line int `json:"line"`
			} `json:"start"`
			End struct {
				Line int `json:"line"`
			} `json:"end"`
			Extra struct {
				Severity string `json:"severity"`
				Message  string `json:"message"`
				Lines    string `json:"lines"`
			} `json:"extra"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("不是合法的 semgrep JSON（用 --json 输出）：%w", err)
	}
	out := make([]db.CodeScanResult, 0, len(doc.Results))
	for _, r := range doc.Results {
		snippet := r.Extra.Lines
		if strings.TrimSpace(snippet) == "" {
			snippet = r.Extra.Message
		}
		res := db.CodeScanResult{
			Scanner: "semgrep", RuleID: r.CheckID,
			Severity: normalizeScanSeverity(r.Extra.Severity),
			FilePath: r.Path, LineStart: intPtr(r.Start.Line), LineEnd: intPtr(r.End.Line),
			Snippet: clipSnippet(snippet),
		}
		res.Fingerprint = codeFingerprint(res)
		out = append(out, res)
	}
	return out, nil
}

// parseGitleaksResults 解析 `gitleaks detect --report-format json` 的输出。
//
// 凭据卫生：gitleaks 的 Secret 字段是明文密钥，绝不能入库——库里存了密钥等于
// 把泄露面从「目标仓库」扩大到「本平台数据库 + 报告 + 备份」。这里只留长度与
// 前 4 位，用来让分析者确认是哪一条，足够复核而不构成可用凭据。
func parseGitleaksResults(raw []byte) ([]db.CodeScanResult, error) {
	var doc []struct {
		RuleID      string `json:"RuleID"`
		Description string `json:"Description"`
		File        string `json:"File"`
		StartLine   int    `json:"StartLine"`
		EndLine     int    `json:"EndLine"`
		Secret      string `json:"Secret"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("不是合法的 gitleaks JSON（--report-format json）：%w", err)
	}
	out := make([]db.CodeScanResult, 0, len(doc))
	for _, r := range doc {
		snippet := r.Description
		if s := strings.TrimSpace(r.Secret); s != "" {
			masked := "****"
			if r := []rune(s); len(r) > 4 {
				masked = string(r[:4]) + "****（长度 " + fmt.Sprint(len(r)) + "，已脱敏）"
			}
			snippet = strings.TrimSpace(snippet + " · 命中值 " + masked)
		}
		// 密钥默认按 high：泄露凭据通常直接可用，比多数 SAST 规则更该先看。
		res := db.CodeScanResult{
			Scanner: "gitleaks", RuleID: r.RuleID, Severity: "high",
			FilePath: r.File, LineStart: intPtr(r.StartLine), LineEnd: intPtr(r.EndLine),
			Snippet: clipSnippet(snippet),
		}
		res.Fingerprint = codeFingerprint(res)
		out = append(out, res)
	}
	return out, nil
}

// parseTrivyResults 解析 `trivy fs --format json` 的输出（成分分析）。
func parseTrivyResults(raw []byte) ([]db.CodeScanResult, error) {
	var doc struct {
		Results []struct {
			Target          string `json:"Target"`
			Vulnerabilities []struct {
				VulnerabilityID  string `json:"VulnerabilityID"`
				PkgName          string `json:"PkgName"`
				InstalledVersion string `json:"InstalledVersion"`
				Severity         string `json:"Severity"`
				Title            string `json:"Title"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("不是合法的 trivy JSON（--format json）：%w", err)
	}
	out := []db.CodeScanResult{}
	for _, res := range doc.Results {
		eco := ecosystemFromLockfile(res.Target)
		for _, v := range res.Vulnerabilities {
			row := db.CodeScanResult{
				Scanner: "trivy", RuleID: v.VulnerabilityID,
				Severity: normalizeScanSeverity(v.Severity),
				FilePath: res.Target,
				Snippet:  clipSnippet(v.Title),
				PackageName: v.PkgName, PackageVersion: v.InstalledVersion,
				PackageEcosystem: eco,
			}
			row.Fingerprint = codeFingerprint(row)
			out = append(out, row)
		}
	}
	return out, nil
}

// ecosystemFromLockfile 从锁文件名推断生态，供 P2 的 CVE 匹配用。
func ecosystemFromLockfile(target string) string {
	base := strings.ToLower(filepath.Base(target))
	switch {
	case base == "go.mod" || strings.HasSuffix(base, ".sum"):
		return "go"
	case base == "package-lock.json" || base == "yarn.lock" || base == "pnpm-lock.yaml":
		return "npm"
	case strings.HasPrefix(base, "requirements") || base == "poetry.lock" || base == "pipfile.lock":
		return "pypi"
	case base == "pom.xml" || strings.HasSuffix(base, ".gradle"):
		return "maven"
	case base == "gemfile.lock":
		return "rubygems"
	case base == "cargo.lock":
		return "cargo"
	}
	return ""
}

// parseScanFile 按扫描器分派解析；文件不存在/超限给可执行的提示。
func parseScanFile(scanner, path string) ([]db.CodeScanResult, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("读取扫描结果失败：%w（先把命令输出写到该路径）", err)
	}
	if st.Size() > codeScanFileLimit {
		return nil, fmt.Errorf("扫描结果 %d MB 超过上限 %d MB——多半扫错了范围，缩小到仓库目录再跑",
			st.Size()>>20, codeScanFileLimit>>20)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	switch scanner {
	case "semgrep":
		return parseSemgrepResults(raw)
	case "gitleaks":
		return parseGitleaksResults(raw)
	case "trivy":
		return parseTrivyResults(raw)
	}
	return nil, fmt.Errorf("未知扫描器 %q：可用 semgrep | gitleaks | trivy", scanner)
}

// ── 成分分析 → 公开情报库（P2 桥）──────────────────────────

const (
	// codePkgVulnMaxPackages 是单次 ingress 最多查多少个包。一次 trivy 结果可能有上千个
	// 组件，全查会变成上百次情报查询；先给最有代表性的，需要全量时再按资产复查。
	codePkgVulnMaxPackages = 10
	// codePkgVulnPerPackage 是单个包最多回几条 CVE。
	codePkgVulnPerPackage = 3
)

// matchPackageVulns 把成分扫描结果接到公开情报库。
//
// 为什么不需要「生态名 → CPE vendor/product」的映射层：GHSA 同步（vulnintel 的
// ghsaIndexRows）已经按包名往 vuln_cpe_products 写了多行索引——npm/pypi 的裸包名、
// composer 的 vendor/package 全名、vendor 段兜底都有。所以这里把包名当 product
// 传进去就能命中，多加一层映射只会引入新的不一致。
//
// 注意：**不要把 ecosystem 当 vendor 传**。vendor 参数是「包含匹配」过滤器，
// 而 npm 包的 vendor 常为空，传 "npm" 会把命中全过滤掉。
func matchPackageVulns(ctx context.Context, rows []db.CodeScanResult) []map[string]any {
	if vulnintelStore == nil {
		return nil
	}
	type pkgKey struct{ name, version string }
	eco := map[pkgKey]string{}
	for _, r := range rows {
		if r.Scanner != "trivy" || strings.TrimSpace(r.PackageName) == "" {
			continue
		}
		k := pkgKey{r.PackageName, r.PackageVersion}
		if _, ok := eco[k]; !ok {
			eco[k] = r.PackageEcosystem
		}
	}
	if len(eco) == 0 {
		return nil
	}
	keys := make([]pkgKey, 0, len(eco))
	for k := range eco {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name != keys[j].name {
			return keys[i].name < keys[j].name
		}
		return keys[i].version < keys[j].version
	})
	if len(keys) > codePkgVulnMaxPackages {
		keys = keys[:codePkgVulnMaxPackages]
	}

	out := []map[string]any{}
	for _, k := range keys {
		hits, err := vulnintelStore.SearchVulns(ctx, "", k.name, k.version, codePkgVulnPerPackage)
		if err != nil || len(hits) == 0 {
			continue
		}
		cves := make([]map[string]any, 0, len(hits))
		for _, h := range hits {
			item := map[string]any{"cve_id": h.CVEID, "severity": h.Severity, "kev": h.KEV}
			if h.VersionInRange != nil {
				item["version_in_range"] = *h.VersionInRange
			}
			cves = append(cves, item)
		}
		out = append(out, map[string]any{
			"package": k.name, "version": k.version, "ecosystem": eco[k], "cves": cves,
		})
	}
	return out
}

// ── 工具 ────────────────────────────────────────────────────

func (t *ToolSet) codeAssetRegister() actool.CoreTool {
	return t.writeExpTool("code_asset_register",
		"登记一个【代码资产】（仓库或源码包），返回 code_asset_id，供后续扫描与反查使用。\n"+
			"**只在授权范围内登记**：登记的仓库必须属于本次任务授权的企业；拿不准就先问人。\n"+
			"同一 (企业, 名称, 类型) 重复登记会更新而不是新建。",
		obj(map[string]any{
			"name":     str("仓库名或包名（唯一标识，如 order-service）"),
			"kind":     str("repo（git 仓库）| archive（本地源码包），默认 repo"),
			"location": str("git URL 或本地路径（扫描命令直接用它）"),
			"language": str("主语言（可选，如 go / java / php）"),
			"company_id": intp("归属企业 id（可选；不填则跟随本任务的企业归属）"),
			"file_count": intp("文件数（可选）"),
			"loc":        intp("代码行数（可选）"),
		}, "name"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Name      string `json:"name"`
				Kind      string `json:"kind"`
				Location  string `json:"location"`
				Language  string `json:"language"`
				CompanyID int64  `json:"company_id"`
				FileCount int    `json:"file_count"`
				LOC       int    `json:"loc"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			companyID := a.CompanyID
			if companyID <= 0 {
				companyID = t.taskCompanyID(ctx)
			}
			id, err := t.ts.CodeAssets().UpsertCodeAsset(ctx, db.CodeAsset{
				CompanyID: int64Ptr(companyID), TaskID: t.taskID,
				Name: a.Name, Kind: strings.TrimSpace(a.Kind), Location: a.Location,
				Language: a.Language, FileCount: a.FileCount, LOC: a.LOC,
			})
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{
				"code_asset_id": id,
				"note": "下一步用 code_scan_plan 生成扫描命令，跑完把 JSON 结果交给 code_scan_ingest 入库。",
			})
		})
}

func (t *ToolSet) codeScanPlan() actool.CoreTool {
	return t.readExpTool("code_scan_plan",
		"生成【静态扫描命令】（semgrep / gitleaks / trivy），只给命令与输出路径，不给结论。\n"+
			"扫的是本地源码，不产生网络流量；但仍是读目标代码，必须在授权范围内。\n"+
			"跑完把 JSON 结果用 code_scan_ingest 入库，规划者才能看到「这个仓库有多少处 sink」。",
		obj(map[string]any{
			"code_asset_id": intp("代码资产 id（code_asset_register 返回的）"),
			"scanners":      str("逗号分隔的扫描器：semgrep,gitleaks,trivy（默认三者都生成）"),
		}, "code_asset_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				CodeAssetID int64  `json:"code_asset_id"`
				Scanners    string `json:"scanners"`
			}
			_ = json.Unmarshal(in, &a)
			if a.CodeAssetID <= 0 {
				return actool.Errorf("code_asset_id 必填（先 code_asset_register）"), nil
			}
			asset, err := t.ts.CodeAssets().GetCodeAsset(ctx, a.CodeAssetID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if asset == nil {
				return actool.Errorf(fmt.Sprintf("代码资产 %d 不存在", a.CodeAssetID)), nil
			}
			target := strings.TrimSpace(asset.Location)
			if target == "" {
				return actool.Errorf("该代码资产没有 location（登记时没填路径）——无法生成扫描命令"), nil
			}
			workDir := intercept.ReviewWorkingDirectory(ctx)
			if workDir == "" {
				return actool.Errorf("本次 run 没有绑定工作目录，无法写出扫描结果文件"), nil
			}
			wanted := splitCommaList(a.Scanners)
			if len(wanted) == 0 {
				wanted = []string{"semgrep", "gitleaks", "trivy"}
			}
			cmds := map[string]string{}
			for _, sc := range wanted {
				out := filepath.Join(workDir, "code_scan_"+sc+".json")
				switch sc {
				case "semgrep":
					cmds[sc] = fmt.Sprintf("semgrep --config auto --json --quiet --output %s %s", quoteShell(out), quoteShell(target))
				case "gitleaks":
					cmds[sc] = fmt.Sprintf("gitleaks detect --no-banner --source %s --report-format json --report-path %s", quoteShell(target), quoteShell(out))
				case "trivy":
					cmds[sc] = fmt.Sprintf("trivy fs --quiet --format json --output %s %s", quoteShell(out), quoteShell(target))
				default:
					cmds[sc] = ""
				}
			}
			return jsonResult(map[string]any{
				"code_asset_id": asset.ID,
				"name":          asset.Name,
				"target":        target,
				"commands":      cmds,
				"note": "用 Bash 逐个执行；每条命令把 JSON 写到工作目录。跑完用 code_scan_ingest 传 file_path 入库。" +
					"工具缺失时「宿主环境」页可一键安装。",
			})
		})
}

func (t *ToolSet) codeScanIngest() actool.CoreTool {
	return t.writeExpTool("code_scan_ingest",
		"把【扫描器输出的 JSON 文件】解析入库（semgrep / gitleaks / trivy），返回入库条数与分布。\n"+
			"只认扫描器自己写出的文件——不接受手写/改写的结果。重复扫描按指纹去重，不会堆行。\n"+
			"入库后规划者能在态势里看到这些线索；高危规则命中不会自动变成漏洞，需动态验证后 report_finding。",
		obj(map[string]any{
			"code_asset_id": intp("代码资产 id"),
			"scanner":       str("semgrep | gitleaks | trivy"),
			"file_path":     str("扫描器输出的 JSON 文件路径（工作目录下）"),
			"node_id":       intp("可选：把本次入库的代码资产锚定到该探索图节点（通常是当前意图的产物）"),
		}, "code_asset_id", "scanner", "file_path"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				CodeAssetID int64  `json:"code_asset_id"`
				Scanner     string `json:"scanner"`
				FilePath    string `json:"file_path"`
				NodeID      int64  `json:"node_id"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if a.CodeAssetID <= 0 {
				return actool.Errorf("code_asset_id 必填"), nil
			}
			scanner := strings.ToLower(strings.TrimSpace(a.Scanner))
			path := strings.TrimSpace(a.FilePath)
			if path == "" {
				return actool.Errorf("file_path 必填（扫描器写出的 JSON）"), nil
			}
			rows, err := parseScanFile(scanner, path)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			kept, dropped := collapseScanResults(rows)
			inserted, updated, err := t.ts.CodeAssets().InsertCodeScanResults(ctx, a.CodeAssetID, kept)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if a.NodeID > 0 {
				_ = t.ts.CodeAssets().AnchorCodeAsset(ctx, a.CodeAssetID, t.ts.ID(), a.NodeID)
			}
			summary, _ := t.ts.CodeAssets().CodeScanSummary(ctx, a.CodeAssetID)
			out := map[string]any{
				"scanner":       scanner,
				"parsed":        len(rows),
				"inserted":      inserted,
				"updated":       updated,
				"collapsed":     dropped,
				"asset_summary": summary,
				"note":          "静态线索不是漏洞：要拿动态证据（请求响应/命令输出）才能 report_finding。",
			}
			if dropped > 0 {
				out["collapse_hint"] = fmt.Sprintf("规则重复的低危项折叠了 %d 条（每规则最多留 3 条样本），critical/high 全量保留。", dropped)
			}
			// 成分分析落地后立刻对一次情报库：trivy 给出了「组件 + 版本」，
			// 这正是公开 CVE 能直接回答的问题，不必等下一轮再让模型手工查。
			if scanner == "trivy" {
				if pv := matchPackageVulns(ctx, kept); len(pv) > 0 {
					out["package_vulns"] = pv
					out["package_vuln_hint"] = "这些是组件版本命中的公开 CVE：version_in_range=true 的优先验；" +
						"确认影响后用 add_hint 告诉规划者要打哪个组件（写清组件/版本/CVE），不要直接把静态命中当漏洞上报。"
				}
			}
			return jsonResult(out)
		})
}

func (t *ToolSet) codeAssetContext() actool.CoreTool {
	return t.readExpTool("code_asset_context",
		"按【运行时资产】反查源码：给一个 URL 或域名，返回该企业登记的代码资产 + 用路径关键词"+
			"命中的扫描结果（sink 位置、规则、等级）。\n"+
			"典型用法：动态侧发现 /api/order/create 可疑 → 反查后端有没有对应 handler、用了什么拼接方式。",
		obj(map[string]any{
			"url":     str("目标 URL 或域名（如 https://a.example.com/api/order/create 或 a.example.com）"),
			"keyword": str("可选：覆盖默认的路径关键词（默认取路径最后一个非 id 段）"),
			"limit":   intp("返回的 sink 条数上限，默认 20，上限 100"),
		}, "url"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				URL     string `json:"url"`
				Keyword string `json:"keyword"`
				Limit   int    `json:"limit"`
			}
			_ = json.Unmarshal(in, &a)
			limit := a.Limit
			if limit <= 0 || limit > 100 {
				limit = 20
			}
			cc, err := t.ts.CodeAssets().CodeContextFor(ctx, a.URL, limit)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if kw := strings.TrimSpace(a.Keyword); kw != "" && len(cc.Assets) > 0 {
				// 显式关键词覆盖：重新按该词搜。
				cc.Sinks = nil
				for _, as := range cc.Assets {
					sinks, err := t.ts.CodeAssets().ListCodeSinks(ctx, as.ID, kw, limit)
					if err != nil {
						return actool.Errorf(err.Error()), nil
					}
					cc.Sinks = append(cc.Sinks, sinks...)
					if len(cc.Sinks) >= limit {
						cc.Sinks = cc.Sinks[:limit]
						break
					}
				}
				cc.Keyword = kw
			}
			return jsonResult(cc)
		})
}

// taskCompanyID 取本任务的企业归属（用于代码资产归属）。查不到返回 0。
func (t *ToolSet) taskCompanyID(ctx context.Context) int64 {
	if t.ts == nil || t.taskID <= 0 {
		return 0
	}
	ids, err := t.ts.TaskCompanies(t.taskID)
	if err != nil || len(ids) == 0 {
		return 0
	}
	return ids[0]
}

func splitCommaList(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(strings.ToLower(part)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func int64Ptr(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	return &v
}
