package db

// ═══ M-02：代码资产与静态扫描 ═══
//
// 源码是与域名/IP 同等重要的一类资产，此前它不是资产：semgrep / gitleaks / trivy
// 的结果只是 Bash 回显，worker 看过就丢，规划者看不到「这个仓库有多少处可控输入
// 进 sink」，也就派不出「去动态验证这个 sink」的意图。
//
// 边界（与设计稿一致）：
//   - 不并进 assets 表（assets.type 是六类型枚举，全平台共享；动它要改去重、覆盖统计、
//     前端分组三处核心逻辑）。代码资产独立建表，用 code_asset_anchors 挂到探索图。
//   - 不执行样本代码。扫描是本地读文件，由 worker 用宿主工具跑，结果 JSON 交本模块解析。
//   - 不接受模型手写的结果：入库入口只认扫描器输出的文件路径。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CodeAsset 是一个仓库或源码包。
type CodeAsset struct {
	ID            int64     `json:"id"`
	CompanyID     *int64    `json:"company_id,omitempty"`
	TaskID        int64     `json:"task_id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"` // repo | archive
	Location      string    `json:"location"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	Language      string    `json:"language,omitempty"`
	FileCount     int       `json:"file_count"`
	LOC           int       `json:"loc"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// CodeScanResult 是一条归一后的静态扫描结果。三种扫描器的差异由 Scanner 字段区分，
// 成分分析才有 package_* 三列。
type CodeScanResult struct {
	ID               int64  `json:"id,omitempty"`
	CodeAssetID      int64  `json:"code_asset_id"`
	Scanner          string `json:"scanner"`  // semgrep | gitleaks | trivy
	RuleID           string `json:"rule_id"`  // 规则 / CVE / 密钥类型
	Severity         string `json:"severity"` // critical|high|medium|low|info
	FilePath         string `json:"file_path"`
	LineStart        *int   `json:"line_start,omitempty"`
	LineEnd          *int   `json:"line_end,omitempty"`
	Snippet          string `json:"snippet,omitempty"`
	PackageName      string `json:"package_name,omitempty"`
	PackageVersion   string `json:"package_version,omitempty"`
	PackageEcosystem string `json:"package_ecosystem,omitempty"`
	Fingerprint      string `json:"fingerprint"`
}

// CodeAssetStore 读写代码资产与扫描结果。
type CodeAssetStore struct{ db *DB }

// CodeAssets 返回与主库同一连接的代码资产存储。
func (d *DB) CodeAssets() *CodeAssetStore { return &CodeAssetStore{db: d} }

// CodeAssets 返回与当前探索图同一数据库的代码资产存储（省一次注入，与 Compliance 同思路）。
func (s *ExplorationStore) CodeAssets() *CodeAssetStore { return &CodeAssetStore{db: s.db} }

// TaskCompanies 暴露本任务的企业归属，供 agent 侧决定代码资产挂给谁
// （工具包里拿不到 *DB，只能经已有的 store 转发）。
func (s *ExplorationStore) TaskCompanies(taskID int64) ([]int64, error) {
	return s.db.TaskCompanyIDs(taskID)
}

// UpsertCodeAsset 登记/更新一个代码资产，返回 id。
//
// 唯一键是 (COALESCE(company_id,0), name, kind)——用表达式索引而不是表内 UNIQUE，
// 因为 company_id 可为 NULL 而 NULL 之间互不相等。
// 冲突时的合取语义与资产图一致：只补空、不覆盖已有值；计数取较大者（重复扫描不该缩水）。
func (s *CodeAssetStore) UpsertCodeAsset(ctx context.Context, a CodeAsset) (int64, error) {
	if strings.TrimSpace(a.Name) == "" {
		return 0, fmt.Errorf("name 不能为空")
	}
	if a.Kind == "" {
		a.Kind = "repo"
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `
INSERT INTO code_assets(company_id, task_id, name, kind, location, default_branch, language, file_count, loc)
VALUES (NULLIF($1,0), $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (COALESCE(company_id, 0), name, kind) DO UPDATE SET
    location       = COALESCE(NULLIF(EXCLUDED.location, ''), code_assets.location),
    default_branch = COALESCE(NULLIF(EXCLUDED.default_branch, ''), code_assets.default_branch),
    language       = COALESCE(NULLIF(EXCLUDED.language, ''), code_assets.language),
    file_count     = GREATEST(code_assets.file_count, EXCLUDED.file_count),
    loc            = GREATEST(code_assets.loc, EXCLUDED.loc),
    updated_at     = now()
RETURNING id`,
		a.CompanyID, a.TaskID, strings.TrimSpace(a.Name), a.Kind, strings.TrimSpace(a.Location),
		strings.TrimSpace(a.DefaultBranch), strings.TrimSpace(a.Language), a.FileCount, a.LOC).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("登记代码资产: %w", err)
	}
	return id, nil
}

// GetCodeAsset 按 id 取一条。
func (s *CodeAssetStore) GetCodeAsset(ctx context.Context, id int64) (*CodeAsset, error) {
	rows, err := s.queryCodeAssets(ctx, `WHERE id = $1`, id)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

// ListCodeAssets 列出代码资产；companyID > 0 时只看该企业。
func (s *CodeAssetStore) ListCodeAssets(ctx context.Context, companyID int64, limit int) ([]*CodeAsset, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if companyID > 0 {
		return s.queryCodeAssets(ctx, `WHERE company_id = $1 ORDER BY updated_at DESC LIMIT $2`, companyID, limit)
	}
	return s.queryCodeAssets(ctx, `ORDER BY updated_at DESC LIMIT $1`, limit)
}

// listUnattributedCodeAssets 只列「没有企业归属」的仓库（全局登记）。
//
// 反查时用它而不是 ListCodeAssets(0)：后者返回所有企业的仓库，一条查不到归属的
// URL 就能把别的公司的源码清单带出来。单管理员部署下影响有限，但语义必须对。
func (s *CodeAssetStore) listUnattributedCodeAssets(ctx context.Context, limit int) ([]*CodeAsset, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.queryCodeAssets(ctx, `WHERE company_id IS NULL ORDER BY updated_at DESC LIMIT $1`, limit)
}

// queryCodeAssets 是共用的读取骨架（避免三处扫描列名漂移）。
func (s *CodeAssetStore) queryCodeAssets(ctx context.Context, tail string, args ...any) ([]*CodeAsset, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, company_id, task_id, name, kind, location, default_branch, language,
       file_count, loc, created_at, updated_at
FROM code_assets `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*CodeAsset{}
	for rows.Next() {
		var a CodeAsset
		if err := rows.Scan(&a.ID, &a.CompanyID, &a.TaskID, &a.Name, &a.Kind, &a.Location,
			&a.DefaultBranch, &a.Language, &a.FileCount, &a.LOC, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// InsertCodeScanResults 批量入库，返回 (新增, 已存在被更新)。
//
// 去重靠 (code_asset_id, fingerprint)：同一仓库重复扫描只更新时间，不堆行——
// SAST 一轮跑出上万条是常态，堆行会让这张表迅速变成不可查询。
func (s *CodeAssetStore) InsertCodeScanResults(ctx context.Context, assetID int64, rows []CodeScanResult) (inserted, updated int, err error) {
	if assetID <= 0 {
		return 0, 0, fmt.Errorf("code_asset_id 无效")
	}
	if len(rows) == 0 {
		return 0, 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO code_scan_results(code_asset_id, scanner, rule_id, severity, file_path,
    line_start, line_end, snippet, package_name, package_version, package_ecosystem, fingerprint)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (code_asset_id, fingerprint) DO UPDATE SET
    severity = EXCLUDED.severity,
    snippet  = COALESCE(NULLIF(EXCLUDED.snippet, ''), code_scan_results.snippet),
    scanned_at = now()
RETURNING (xmax = 0) AS is_insert`)
	if err != nil {
		return 0, 0, err
	}
	defer stmt.Close()
	for _, r := range rows {
		if strings.TrimSpace(r.Fingerprint) == "" {
			continue // 没指纹就没法去重，宁可不入也不要堆重复行
		}
		var isInsert bool
		if err := stmt.QueryRowContext(ctx, assetID, r.Scanner, r.RuleID, r.Severity, r.FilePath,
			r.LineStart, r.LineEnd, r.Snippet, r.PackageName, r.PackageVersion, r.PackageEcosystem,
			r.Fingerprint).Scan(&isInsert); err != nil {
			return inserted, updated, err
		}
		if isInsert {
			inserted++
		} else {
			updated++
		}
	}
	return inserted, updated, tx.Commit()
}

// CodeScanSummary 返回该资产的扫描结果分布：总数、按扫描器、按严重度。
func (s *CodeAssetStore) CodeScanSummary(ctx context.Context, assetID int64) (map[string]any, error) {
	out := map[string]any{"total": 0}
	byScanner := map[string]int{}
	bySeverity := map[string]int{}
	rows, err := s.db.QueryContext(ctx, `
SELECT scanner, severity, count(*) FROM code_scan_results WHERE code_asset_id = $1
GROUP BY scanner, severity`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var scanner, sev string
		var n int
		if err := rows.Scan(&scanner, &sev, &n); err != nil {
			return nil, err
		}
		byScanner[scanner] += n
		if sev != "" {
			bySeverity[sev] += n
		}
		total += n
	}
	out["total"] = total
	out["by_scanner"] = byScanner
	out["by_severity"] = bySeverity
	return out, rows.Err()
}

// ListCodeSinks 从扫描结果里按关键词找位置（file_path / snippet / rule_id 模糊匹配）。
// 这是「动态入口 → 源码位置」这条桥的探针：给接口名或参数名，返回哪几处代码碰过它。
func (s *CodeAssetStore) ListCodeSinks(ctx context.Context, assetID int64, keyword string, limit int) ([]CodeScanResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	kw := strings.TrimSpace(keyword)
	q := `SELECT id, code_asset_id, scanner, rule_id, severity, file_path, line_start, line_end,
       snippet, package_name, package_version, package_ecosystem, fingerprint
FROM code_scan_results WHERE code_asset_id = $1`
	args := []any{assetID}
	if kw != "" {
		q += ` AND (file_path ILIKE $2 OR snippet ILIKE $2 OR rule_id ILIKE $2)`
		args = append(args, "%"+kw+"%")
	}
	q += fmt.Sprintf(` ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1
       WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END, id LIMIT $%d`, len(args)+1)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodeScanResult{}
	for rows.Next() {
		var r CodeScanResult
		if err := rows.Scan(&r.ID, &r.CodeAssetID, &r.Scanner, &r.RuleID, &r.Severity, &r.FilePath,
			&r.LineStart, &r.LineEnd, &r.Snippet, &r.PackageName, &r.PackageVersion,
			&r.PackageEcosystem, &r.Fingerprint); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AnchorCodeAsset 把代码资产挂到某个探索图节点（fact/intent/finding）。
// 幂等：同一 (代码资产, 节点) 重复锚定不报错。
func (s *CodeAssetStore) AnchorCodeAsset(ctx context.Context, codeAssetID, explorationID, nodeID int64) error {
	if codeAssetID <= 0 || nodeID <= 0 {
		return nil // 无锚点是合法状态（模型没给关联节点），不当作错误
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO code_asset_anchors(code_asset_id, exploration_id, node_id)
VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, codeAssetID, explorationID, nodeID)
	return err
}

// CodeScanBrief 是报告用的按资产扫描概要。
type CodeScanBrief struct {
	AssetID    int64            `json:"asset_id"`
	AssetName  string           `json:"asset_name"`
	Location   string           `json:"location,omitempty"`
	Total      int              `json:"total"`
	ByScanner  map[string]int   `json:"by_scanner"`
	BySeverity map[string]int   `json:"by_severity"`
	TopSinks   []CodeScanResult `json:"top_sinks"`
}

// TaskCodeScanBriefs 汇总某任务登记的代码资产及扫描结果，供报告章节使用。
//
// 只按 task_id 关联（首次登记该仓库的任务）——报告是任务维度的交付件，
// 不该把企业下其他任务的仓库混进来。没登记过源码的任务返回空，报告因此不出该章。
func (s *CodeAssetStore) TaskCodeScanBriefs(ctx context.Context, taskID int64, topPerAsset int) ([]CodeScanBrief, error) {
	if taskID <= 0 {
		return nil, nil
	}
	if topPerAsset <= 0 || topPerAsset > 20 {
		topPerAsset = 3
	}
	assets, err := s.queryCodeAssets(ctx, `WHERE task_id = $1 ORDER BY id LIMIT 50`, taskID)
	if err != nil || len(assets) == 0 {
		return nil, err
	}
	out := make([]CodeScanBrief, 0, len(assets))
	for _, a := range assets {
		sum, err := s.CodeScanSummary(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		total, _ := sum["total"].(int)
		if total == 0 {
			continue // 登记了但没扫：报告不列空仓库
		}
		brief := CodeScanBrief{
			AssetID: a.ID, AssetName: a.Name, Location: a.Location, Total: total,
			ByScanner:  toIntMap(sum["by_scanner"]),
			BySeverity: toIntMap(sum["by_severity"]),
		}
		if sinks, err := s.ListCodeSinks(ctx, a.ID, "", topPerAsset); err == nil {
			brief.TopSinks = sinks
		}
		out = append(out, brief)
	}
	return out, nil
}

// toIntMap 把 map[string]any 收窄成 map[string]int（Summary 返回的是通用 map）。
func toIntMap(v any) map[string]int {
	out := map[string]int{}
	if m, ok := v.(map[string]int); ok {
		for k, n := range m {
			out[k] = n
		}
	}
	return out
}

// CodeContext 是「运行时资产 → 源码」反查的结果。
type CodeContext struct {
	Query       string           `json:"query"`
	Keyword     string           `json:"keyword,omitempty"`      // 用来搜 sink 的关键词
	Companies   []int64          `json:"companies,omitempty"`    // 命中的企业
	Assets      []*CodeAsset     `json:"code_assets"`            // 可查的代码资产
	Sinks       []CodeScanResult `json:"sinks"`                  // 关键词命中的扫描结果
	Note        string           `json:"note"`
}

// CodeContextFor 按运行时资产（URL 或域名）反查可用的代码资产与相关 sink。
//
// 关联逻辑（这里没有代码托管平台的权限，只能靠归属推）：
//  1. URL/域名 → 资产表 → company_id；
//  2. 该 company 下的 code_assets；
//  3. 用 URL 路径末端（如 /api/order/create → create | order）在扫描结果里搜位置。
//
// 找不到归属时返回空集而不是报错——「这个目标没有登记源码」是常见且合法的状态。
func (s *CodeAssetStore) CodeContextFor(ctx context.Context, rawURL string, limit int) (*CodeContext, error) {
	raw := strings.TrimSpace(rawURL)
	out := &CodeContext{Query: raw, Assets: []*CodeAsset{}, Sinks: []CodeScanResult{}}
	if raw == "" {
		return nil, fmt.Errorf("url 不能为空")
	}
	host, keyword := splitHostAndKeyword(raw)
	if host == "" {
		return nil, fmt.Errorf("无法从 %q 解析出主机（给完整 URL 或域名）", raw)
	}
	out.Keyword = keyword

	// 1. 主机 → 企业（服务资产的 url 以 host 开头，子域资产看 domain）。
	var companyID int64
	err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(company_id, 0) FROM assets
WHERE company_id IS NOT NULL AND (domain = $1 OR url ILIKE $2)
ORDER BY id LIMIT 1`, host, host+"%").Scan(&companyID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if companyID > 0 {
		out.Companies = []int64{companyID}
	}

	// 2. 该企业的代码资产；查不到归属时只看「全局登记」（company_id IS NULL）的仓库。
	var assets []*CodeAsset
	var err2 error
	if companyID > 0 {
		assets, err2 = s.ListCodeAssets(ctx, companyID, 20)
	} else {
		assets, err2 = s.listUnattributedCodeAssets(ctx, 20)
	}
	if err2 != nil {
		return nil, err2
	}
	out.Assets = assets

	// 3. 关键词搜 sink。
	if keyword != "" {
		for _, a := range assets {
			sinks, err := s.ListCodeSinks(ctx, a.ID, keyword, limit)
			if err != nil {
				return nil, err
			}
			out.Sinks = append(out.Sinks, sinks...)
			if len(out.Sinks) >= limit {
				out.Sinks = out.Sinks[:limit]
				break
			}
		}
	}
	switch {
	case len(out.Assets) == 0:
		out.Note = "未找到关联的代码资产。若目标有源码，先用 code_asset_register 登记（需在授权范围内）。"
	case len(out.Sinks) == 0:
		out.Note = "有代码资产但没有匹配该路径关键词的扫描结果。可先用 code_scan_plan + Bash 跑一轮扫描再 ingest。"
	default:
		out.Note = "这些是静态扫描线索，不是已确认漏洞：要拿动态证据才 report_finding。"
	}
	return out, nil
}

// splitHostAndKeyword 从 URL 或域名里取主机与「路径末端关键词」。
//
// 关键词取路径最后一个有意义的分段（去掉纯数字/id 形态），因为接口名通常在那一段：
//   https://x.com/api/order/create?id=1 → host=x.com, keyword=create
//   x.com                                → host=x.com, keyword=""
func splitHostAndKeyword(raw string) (host, keyword string) {
	s := raw
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		path := s[i:]
		s = s[:i]
		if j := strings.IndexAny(path, "?#"); j >= 0 {
			path = path[:j]
		}
		for _, seg := range reverseSegments(path) {
			if !isIDLikeSegment(seg) {
				keyword = seg
				break
			}
		}
	}
	host = s
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i] // 去端口
	}
	return host, keyword
}

func reverseSegments(path string) []string {
	parts := strings.Split(path, "/")
	out := make([]string, 0, len(parts))
	for i := len(parts) - 1; i >= 0; i-- {
		if p := strings.TrimSpace(parts[i]); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// isIDLikeSegment 判断路径段是否只是 id（纯数字、uuid、哈希）——
// 用它当关键词搜源码等于全表扫，没有信息量。
func isIDLikeSegment(s string) bool {
	if s == "" {
		return true
	}
	digits := true
	for _, r := range s {
		if r < '0' || r > '9' {
			digits = false
			break
		}
	}
	if digits {
		return true
	}
	if len(s) >= 16 && strings.Count(s, "-") >= 2 { // uuid 形态
		return true
	}
	return len(s) >= 24 && isHexLike(s)
}

func isHexLike(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
