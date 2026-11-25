package agent

import (
	"fmt"
	"strings"

	"github.com/just805/pangu/db"
)

// ═══ M-05：派单计划编译器 ═══
//
// 派单纪律此前主要靠 planner 提示词的约定（「利用类意图要基于已确认的观测」）。
// 约定会被忽略：实测里既有对范围外资产派单的情况，也有拿「已排除」的结论继续
// 往下派的情况。本文件把其中**可确定性判定**的两条搬到服务端校验：
//
//	1. 锚点资产必须在任务授权范围内（完全越界的直接拒绝）；
//	2. verdict=ruled_out（已排除）的事实不能作为新方向的依据。
//
// 两条都只在解析成本很低、误伤接近零的范围内生效，且查询失败一律 fail-open——
// 闸门故障不能让正常规划停摆（与 scopeGuardError 同一取向）。

// dispatchScopeError 是派单侧的授权范围校验。
//
// 为什么要再拦一次：写回闸门 scopeGuardError 只在 fact/finding 落库时生效，那时
// worker 已经跑完一整轮、烧掉一轮 token 才发现越界。派单前拦，代价小得多。
//
// 只拒绝 foreign（完全越界）。source（继承/关联任务的资产）放行——继承场景下
// planner 常需要给来源资产派只读侦察意图，写回侧另有闸门兜底。
func (t *ToolSet) dispatchScopeError(anchors []int64) string {
	if t.as == nil || t.taskID <= 0 || len(anchors) == 0 {
		return ""
	}
	if !t.ScopeWriteGuard {
		return "" // 用户显式关掉范围守卫时，派单侧保持一致，不做单边收紧
	}
	// 任务从未声明过测试范围时保守放行：此时资产归属判不出 own，会把正常派单
	// 全判成越界（与 N2 scope 守卫「未配范围放行」的既定取向一致）。
	if !t.as.TaskScopeDeclared(t.taskID) {
		return ""
	}
	_, _, foreign, err := t.as.ClassifyAssetsForTask(t.taskID, anchors)
	if err != nil || len(foreign) == 0 {
		return "" // fail-open
	}
	return fmt.Sprintf("意图绑定了 %d 个不在本任务授权范围内的资产（%s）：请只对范围为内的资产派单；"+
		"若这些资产确属本次授权，请先用 add_task_scope 把它们纳入测试范围，或改用 list_assets 里本任务可见的资产 id",
		len(foreign), t.assetLabel(foreign))
}

// dispatchParentVerdictError 拒绝把「已排除」的结论当新方向的依据。
//
// 「已排除」意味着某条路径被具体控制点挡住了；在此基础上继续派利用/验证意图，
// 要么是无效重复，要么说明排除判断本身有问题——两种情况都应该让 planner 先看
// 清那条结论，而不是径直往下派。
func dispatchParentVerdictError(parents []*db.Node) string {
	for _, p := range parents {
		if p == nil || verdictOfNode(p) != FactRuledOut {
			continue
		}
		return fmt.Sprintf("parent_id %d「%s」是 verdict=ruled_out（已排除）的结论，不能作为新方向的依据："+
			"这条路径已被具体控制点挡住。若要继续，请先针对该控制点派一条【绕过验证】意图（以新的观测为起点），"+
			"或确认排除判断有误后重写该事实；不要直接在「已排除」上派利用意图",
			p.ID, clampRunes(nodeSummary(p), 60))
	}
	return ""
}

// clampRunes 按字符截断，避免中文被按字节切断（只用于提示文本）。
func clampRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// assetLabel 渲染资产 id 的简短标签（域名 > URL > IP > 应用名 > 根域名）。
// 与 scopeGuardError 的提示同一口径，便于模型对上自己看到的资产。
func (t *ToolSet) assetLabel(ids []int64) string {
	if t.as == nil {
		return fmt.Sprint(ids)
	}
	assets, err := t.as.GetByIDs(ids)
	if err != nil {
		return fmt.Sprint(ids)
	}
	parts := make([]string, 0, len(assets))
	for i, a := range assets {
		if a == nil {
			continue
		}
		if i >= 6 {
			parts = append(parts, fmt.Sprintf("…等 %d 个", len(assets)))
			break
		}
		name := a.Domain
		if name == "" {
			name = a.URL
		}
		if name == "" {
			name = a.IP
		}
		if name == "" {
			name = a.AppName
		}
		if name == "" {
			name = a.RootDomain
		}
		if name == "" {
			name = fmt.Sprintf("#%d", a.ID)
		}
		parts = append(parts, fmt.Sprintf("#%d %s", a.ID, name))
	}
	return strings.Join(parts, "、")
}
