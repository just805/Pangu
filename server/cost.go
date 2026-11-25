package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/just805/pangu/db"
)

// modelPrice 是内置参考价（USD / 1M tokens）。定价表只能近似：各家价格随
// 版本/渠道变动，这里给的是公开标价的数量级，用于让用户看清"哪个任务、
// 哪个 profile 在烧钱"的相对分布，不承诺账单精度。前缀匹配、按声明顺序
// 命中（specific 在前，free 在最前，避免 ":free" 模型被当成付费模型）。
type modelPrice struct {
	prefix string
	in     float64
	out    float64
}

var modelPrices = []modelPrice{
	{"free", 0, 0},
	{"claude-opus", 15, 75},
	{"claude-sonnet", 3, 15},
	{"claude-haiku", 0.8, 4},
	{"gpt-4o", 2.5, 10},
	{"gpt-4.1", 2, 8},
	{"gpt-5", 1.25, 10},
	{"o3", 2, 8},
	{"gemini-2.5-pro", 1.25, 10},
	{"gemini-2.5-flash", 0.3, 2.5},
	{"deepseek", 0.27, 1.1},
	{"glm", 0.6, 2},
	{"qwen", 0.4, 1.2},
	{"kimi", 0.6, 2.5},
	{"nemotron", 0, 0},
	{"llama", 0.2, 0.6},
}

// estimateUSDCost 按模型名估算一次用量成本；known=false 表示模型不在定价表，
// 成本按 0 计且调用方需要把模型名上报给用户（不静默算错）。
func estimateUSDCost(model string, inTok, outTok int) (usd float64, known bool) {
	m := strings.ToLower(model)
	for _, p := range modelPrices {
		if strings.Contains(m, p.prefix) {
			return (float64(inTok)*p.in + float64(outTok)*p.out) / 1e6, true
		}
	}
	return 0, false
}

// loadProfilePrices 读取各通道的自定义单价（M-15）：profile_name → {in, out}。
// 只收录声明了价格的通道——未声明的回退内置参考价。查询失败返回空表，
// 成本核算退化为旧的「纯内置价」，不会因为一次读失败就没有成本视图。
func (s *Server) loadProfilePrices() map[string][2]float64 {
	out := map[string][2]float64{}
	pg := s.m.PG()
	if pg == nil {
		return out
	}
	profiles, err := pg.ListProfiles()
	if err != nil {
		return out
	}
	for _, p := range profiles {
		if p.InputPricePerMTok > 0 || p.OutputPricePerMTok > 0 {
			out[p.Name] = [2]float64{p.InputPricePerMTok, p.OutputPricePerMTok}
		}
	}
	return out
}

// priceUsage 决定一行用量用哪个价：通道自定义价优先（自有网关/中转站的实际折扣
// 只有用户自己填的才准），否则回退内置参考价。两者都没有时 known=false。
func priceUsage(prices map[string][2]float64, profileName, model string, inTok, outTok int) (usd float64, known bool) {
	if p, ok := prices[profileName]; ok {
		return (float64(inTok)*p[0] + float64(outTok)*p[1]) / 1e6, true
	}
	return estimateUSDCost(model, inTok, outTok)
}

// profileCost 是按 profile 聚合的成本视图。
type profileCost struct {
	ProfileName  string  `json:"profile_name"`
	Calls        int     `json:"calls"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// pgUsageCost 返回成本估算。?task=<id> 时返回该任务（按模型分别定价）；
// 否则返回全站按 profile 聚合 + 总计。所有金额都是"内置参考价估算"，响应里
// 原样带 unpriced_models 让前端提示哪些模型无法定价（而不是把它们算成免费）。
func (s *Server) pgUsageCost(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeJSON(w, 200, map[string]any{"total_cost_usd": 0})
		return
	}
	if task := strings.TrimSpace(r.URL.Query().Get("task")); task != "" {
		rows, err := pg.UsageModelsByTask(task)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		prices := s.loadProfilePrices()
		var total db.TaskUsage
		total.TaskID = task
		var cost float64
		unpriced := map[string]bool{}
		for _, row := range rows {
			c, known := priceUsage(prices, row.ProfileName, row.Model, row.InputTokens, row.OutputTokens)
			cost += c
			if !known {
				unpriced[row.Model] = true
			}
			total.Calls += row.Calls
			total.InputTokens += row.InputTokens
			total.OutputTokens += row.OutputTokens
		}
		writeJSON(w, 200, map[string]any{
			"task":            total,
			"cost_usd":        round4(cost),
			"unpriced_models": keysOf(unpriced),
			"pricing_note":    "优先按各通道自定义单价（LLM 配置页填写，USD/百万 token）；未填的回退内置参考价。仅用于相对对比，不承诺账单精度；不含已归档任务。",
		})
		return
	}

	rows, err := pg.UsageByModel()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	prices := s.loadProfilePrices()
	byProfile := map[string]*profileCost{}
	var totalIn, totalOut int
	var totalCost float64
	unpriced := map[string]bool{}
	for _, row := range rows {
		c, known := priceUsage(prices, row.ProfileName, row.Model, row.InputTokens, row.OutputTokens)
		if !known {
			unpriced[row.Model] = true
		}
		p := byProfile[row.ProfileName]
		if p == nil {
			p = &profileCost{ProfileName: row.ProfileName}
			byProfile[row.ProfileName] = p
		}
		p.Calls += row.Calls
		p.InputTokens += row.InputTokens
		p.OutputTokens += row.OutputTokens
		p.CostUSD += c
		totalIn += row.InputTokens
		totalOut += row.OutputTokens
		totalCost += c
	}
	out := make([]profileCost, 0, len(byProfile))
	for _, p := range byProfile {
		p.CostUSD = round4(p.CostUSD)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CostUSD > out[j].CostUSD })
	writeJSON(w, 200, map[string]any{
		"total": map[string]any{
			"input_tokens":  totalIn,
			"output_tokens": totalOut,
			"cost_usd":      round4(totalCost),
		},
		"by_profile":      out,
		"unpriced_models": keysOf(unpriced),
		"pricing_note":    "优先按各通道自定义单价（LLM 配置页填写，USD/百万 token）；未填的回退内置参考价。仅用于相对对比，不承诺账单精度；不含已归档任务。",
	})
}

func round4(v float64) float64 {
	return float64(int64(v*10000+0.5)) / 10000
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
