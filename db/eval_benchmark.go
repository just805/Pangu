package db

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// ═══ M-14：回归评测靶场（判卷器）═══
//
// 评测基线（eval.go）看的是「真实任务跑得怎样」，但它没有标准答案：
// 改了提示词、换了模型、调了预算之后，无法回答「是变好还是变坏」。
// 本模块登记一组靶场任务的标准答案，跑完按答案判卷——这是 Agent 侧改动的验收工具。
//
// 判卷口径（刻意保守）：
//   - matched   ：标准答案里的项被找到（类别匹配 + 资产关键字匹配）；
//   - missed    ：标准答案里的项没被找到 → 漏报；
//   - unexpected：任务报了但不在标准答案里。**不直接算误报**——靶场也可能被挖出
//     预期之外的真实漏洞。是否算误报由人看明细判断。
//
// 只有「missed」是可以机器判定为坏信号的一侧；unexpected 必须人工过一眼。

// ExpectedFinding 是标准答案里的一项。
type ExpectedFinding struct {
	VulnClass string `json:"vulnclass"`
	// AssetKeyword 是资产标签里应出现的关键字（如 /api/login、api.example.com）。
	// 为空表示只按类别匹配（适用于「任意资产上都算」的漏洞类型）。
	AssetKeyword string `json:"asset_keyword,omitempty"`
	Note         string `json:"note,omitempty"`
}

// ActualFinding 是被评测任务里的一条发现（由调用方从 findings + 资产名组装）。
type ActualFinding struct {
	FindingID   int64    `json:"finding_id"`
	VulnClass   string   `json:"vulnclass"`
	Name        string   `json:"name,omitempty"`
	AssetLabels []string `json:"asset_labels,omitempty"`
}

// UnexpectedFinding 是标准答案之外的发现。
type UnexpectedFinding struct {
	FindingID int64  `json:"finding_id"`
	VulnClass string `json:"vulnclass"`
	Name      string `json:"name,omitempty"`
}

// BenchmarkScore 是一次判卷结果。
type BenchmarkScore struct {
	Expected   int                 `json:"expected"`
	Matched    int                 `json:"matched"`
	Missed     []ExpectedFinding   `json:"missed"`
	Actual     int                 `json:"actual"`
	Unexpected []UnexpectedFinding `json:"unexpected"`
	// Recall = matched / expected（标准答案覆盖率）；expected=0 时为 nil。
	Recall *float64 `json:"recall"`
	// Precision = matched / actual（发现里的「标准答案命中率」）；actual=0 时为 nil。
	// 注意：unexpected 不等于误报，所以这个指标只作参考。
	Precision *float64 `json:"precision"`
	Note      string   `json:"note"`
}

// EvalBenchmark 是一套靶场标准答案。
type EvalBenchmark struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Expected    []ExpectedFinding `json:"expected"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// EvalBenchmarkStore 读写 eval_benchmarks。
type EvalBenchmarkStore struct{ db *DB }

func (d *DB) EvalBenchmarks() *EvalBenchmarkStore { return &EvalBenchmarkStore{db: d} }

// Create 新建标准答案。
func (s *EvalBenchmarkStore) Create(ctx context.Context, name, description string, expected []ExpectedFinding) (int64, error) {
	raw, _ := json.Marshal(expected)
	var id int64
	err := s.db.QueryRowContext(ctx, `
INSERT INTO eval_benchmarks(name, description, expected) VALUES ($1,$2,$3) RETURNING id`,
		strings.TrimSpace(name), description, raw).Scan(&id)
	return id, err
}

// List 返回全部标准答案（最新在前）。
func (s *EvalBenchmarkStore) List(ctx context.Context) ([]EvalBenchmark, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, description, expected, created_at, updated_at
FROM eval_benchmarks ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EvalBenchmark{}
	for rows.Next() {
		var b EvalBenchmark
		var raw []byte
		if err := rows.Scan(&b.ID, &b.Name, &b.Description, &raw, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &b.Expected)
		out = append(out, b)
	}
	return out, rows.Err()
}

// Get 按 id 取标准答案。
func (s *EvalBenchmarkStore) Get(ctx context.Context, id int64) (*EvalBenchmark, error) {
	var b EvalBenchmark
	var raw []byte
	err := s.db.QueryRowContext(ctx, `
SELECT id, name, description, expected, created_at, updated_at
FROM eval_benchmarks WHERE id=$1`, id).
		Scan(&b.ID, &b.Name, &b.Description, &raw, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(raw, &b.Expected)
	return &b, nil
}

// Delete 删除标准答案。
func (s *EvalBenchmarkStore) Delete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM eval_benchmarks WHERE id=$1`, id)
	return err
}

// ScoreBenchmark 是纯函数判卷：不碰数据库，便于单测与复用。
//
// 匹配规则：类别归一后相等，且（未给 asset_keyword，或任一资产标签包含该关键字）。
// 一条实际发现只能命中一条标准答案（避免重复计数）。
func ScoreBenchmark(expected []ExpectedFinding, actual []ActualFinding) BenchmarkScore {
	score := BenchmarkScore{Expected: len(expected), Actual: len(actual),
		Missed: []ExpectedFinding{}, Unexpected: []UnexpectedFinding{}}
	used := make([]bool, len(actual))

	for _, exp := range expected {
		wantClass := NormalizeVulnClass(exp.VulnClass)
		kw := strings.ToLower(strings.TrimSpace(exp.AssetKeyword))
		found := false
		for i, act := range actual {
			if used[i] {
				continue
			}
			if NormalizeVulnClass(act.VulnClass) != wantClass {
				continue
			}
			if kw != "" && !assetLabelsContain(act.AssetLabels, kw) {
				continue
			}
			used[i] = true
			found = true
			break
		}
		if found {
			score.Matched++
		} else {
			score.Missed = append(score.Missed, exp)
		}
	}
	for i, act := range actual {
		if used[i] {
			continue
		}
		score.Unexpected = append(score.Unexpected, UnexpectedFinding{
			FindingID: act.FindingID, VulnClass: act.VulnClass, Name: act.Name,
		})
	}
	if score.Expected > 0 {
		r := float64(score.Matched) / float64(score.Expected)
		score.Recall = &r
	}
	if score.Actual > 0 {
		p := float64(score.Matched) / float64(score.Actual)
		score.Precision = &p
	}
	score.Note = "missed 是漏报（机器可判定的坏信号）；unexpected 需人工看明细——靶场里也可能挖出预期外的真实漏洞，不等于误报。"
	return score
}

func assetLabelsContain(labels []string, lowerKeyword string) bool {
	for _, l := range labels {
		if strings.Contains(strings.ToLower(l), lowerKeyword) {
			return true
		}
	}
	return false
}
