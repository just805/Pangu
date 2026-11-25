package db

import (
	"context"
	"time"
)

// ═══ M-09：合规基线核查 ═══
//
// 在「找漏洞」之外补「合不合规」。基线项定义在代码里（可随版本升级），
// 核查结果按任务沉淀；不符合项由 Agent 转成 finding，复用既有证据链与报告。
//
// 为什么不做成「一键合规扫描器」：等保/CIS 里真正有判定价值的项大多需要看配置、
// 看策略、看人工证据（如「是否定期审计日志」），机器能自动判的只是其中一部分。
// 所以本模块提供「清单 + 记录 + 统计」，自动核查交给 Agent 用既有工具完成。

// ComplianceItem 是一条基线检查项。
type ComplianceItem struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Method string `json:"method"` // 怎么核查（用什么工具、看什么）
	Expect string `json:"expect"` // 期望结果
}

// ComplianceResult 是一条核查结果。
type ComplianceResult struct {
	ItemKey  string    `json:"item_key"`
	Status   string    `json:"status"` // pass|fail|na|unknown
	Evidence string    `json:"evidence"`
	Note     string    `json:"note"`
	Updated  time.Time `json:"updated_at"`
}

// builtinBaselines 是内置基线项库。key 一旦发布不要再改（结果表按 key 关联）。
var builtinBaselines = map[string][]ComplianceItem{
	// 等保 2.0 二级：面向通用系统的六类基本要求，只列机器可核查/可看证据的项。
	"djbh2": {
		{Key: "ident-1", Title: "身份鉴别：登录失败处理", Method: "尝试弱口令并观察是否锁定/延迟（≤5 次即停）", Expect: "有失败次数限制或延迟，不无限重试"},
		{Key: "ident-2", Title: "身份鉴别：口令复杂度", Method: "检查注册/改密接口是否校验复杂度", Expect: "拒绝纯数字/短口令"},
		{Key: "access-1", Title: "访问控制：未授权访问", Method: "无凭据直接请求需鉴权的管理接口", Expect: "401/403，不返回业务数据"},
		{Key: "audit-1", Title: "安全审计：日志留存", Method: "检查是否存在登录/操作日志接口与留存说明", Expect: "有审计记录且可由管理员查询"},
		{Key: "intrusion-1", Title: "入侵防范：最小服务", Method: "端口扫描 + 服务识别", Expect: "只开放业务必需端口，无调试/管理口裸奔"},
		{Key: "malware-1", Title: "恶意代码防范", Method: "检查上传接口的文件类型校验与隔离", Expect: "拒绝可执行类型或落盘隔离"},
		{Key: "backup-1", Title: "数据备份", Method: "查看是否有备份/恢复说明或接口", Expect: "有明确的备份策略说明"},
	},
	// CIS Web 服务器基线（裁剪版）：只保留可从外部核验的项。
	"cis-web": {
		{Key: "tls-1", Title: "TLS 配置", Method: "testssl.sh / 手工握手", Expect: "禁用 SSLv3/TLS1.0/1.1，无弱套件"},
		{Key: "hdr-1", Title: "安全响应头", Method: "看响应头", Expect: "有 HSTS/X-Content-Type-Options/CSP 中至少适用的项"},
		{Key: "dir-1", Title: "目录列表", Method: "请求常见目录", Expect: "不返回目录索引"},
		{Key: "err-1", Title: "错误页信息泄露", Method: "构造错误请求", Expect: "不暴露堆栈/框架版本/绝对路径"},
		{Key: "method-1", Title: "HTTP 方法限制", Method: "OPTIONS/TRACE/PUT 探测", Expect: "关闭不必要的写方法"},
		{Key: "admin-1", Title: "管理口暴露", Method: "探测 /admin /manager /console", Expect: "管理口不对公网开放或有额外防护"},
		{Key: "cookie-1", Title: "Cookie 安全属性", Method: "看 Set-Cookie", Expect: "会话 Cookie 带 HttpOnly，HTTPS 下带 Secure"},
	},
}

// ComplianceBaselines 返回可用基线名（稳定顺序）。
func ComplianceBaselines() []string { return []string{"djbh2", "cis-web"} }

// ComplianceBaseline 返回某个基线的检查项。
func ComplianceBaseline(name string) ([]ComplianceItem, bool) {
	items, ok := builtinBaselines[name]
	return items, ok
}

// ComplianceStore 读写 compliance_results。
type ComplianceStore struct{ db *DB }

func (d *DB) Compliance() *ComplianceStore { return &ComplianceStore{db: d} }

// Compliance 返回与当前探索图同一数据库的合规结果存储（矩阵同思路，省一次注入）。
func (s *ExplorationStore) Compliance() *ComplianceStore { return &ComplianceStore{db: s.db} }

// UpsertResult 写入/覆盖一条核查结果。
func (s *ComplianceStore) UpsertResult(ctx context.Context, taskID int64, baseline string, r ComplianceResult) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO compliance_results(task_id, baseline, item_key, status, evidence, note)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (task_id, baseline, item_key) DO UPDATE SET
  status=EXCLUDED.status, evidence=EXCLUDED.evidence, note=EXCLUDED.note, updated_at=now()`,
		taskID, baseline, r.ItemKey, r.Status, r.Evidence, r.Note)
	return err
}

// ListResults 返回某任务某基线的已有结果。
func (s *ComplianceStore) ListResults(ctx context.Context, taskID int64, baseline string) ([]ComplianceResult, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT item_key, status, evidence, note, updated_at
FROM compliance_results WHERE task_id=$1 AND baseline=$2 ORDER BY item_key`, taskID, baseline)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ComplianceResult{}
	for rows.Next() {
		var r ComplianceResult
		if err := rows.Scan(&r.ItemKey, &r.Status, &r.Evidence, &r.Note, &r.Updated); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteByTask 清理某任务的合规结果（任务物理删除兜底）。
func (s *ComplianceStore) DeleteByTask(ctx context.Context, taskID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM compliance_results WHERE task_id=$1`, taskID)
	return err
}
