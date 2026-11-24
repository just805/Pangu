package agent

// 工具可见性上限（上游 0.4.117 同款思路，复用 SDK 既有 deferred 机制）。
//
// 工具总数已到 90+，每个工具 schema 都进 prompt 是固定的 token 开销，弱模型还会
// 在长清单里选错。这里默认只让 30 个工具「schema 可见」：写回/核心执行面永远
// 可见，其余按原顺序补位，多出来的只保留名字（走 SDK 的 DeferredTools：
// schema 不下发，模型可用 SearchExtraTools/ExecuteExtraTool 发现并调用）。
//
// 注意：被 deferred 的工具仍然留在 Options.Tools 里——SDK 的 registry 必须先持有
// 它们，ExecuteExtraTool 才调得到（norma agentcore.go:315-325）。
// 关闭方式：PANGU_TOOL_VISIBILITY_LIMIT=0（或负数）。

import (
	"os"
	"strconv"
	"strings"

	actool "github.com/Autumn-27/norma/tool"
)

const defaultToolVisibilityLimit = 30

// essentialToolNames 是必须保持 schema 可见的核心工具：写回、执行、图谱读取与
// 任务控制。即使超过上限也不藏（藏了模型就没法干活）。
var essentialToolNames = map[string]bool{
	// 本地执行与文件
	"Bash": true, "Read": true, "Write": true, "Edit": true,
	// 写回
	"record_fact": true, "report_finding": true, "insert_assets": true,
	"save_evidence": true, "add_intent": true, "add_goal": true, "prove_goal": true,
	// 网络执行（出漏洞的主要手段）
	"http_request": true, "session_login": true, "scan_with_nuclei": true,
	// 态势与资产
	"graph_overview": true, "list_assets": true, "node_detail": true,
	"list_untested_assets": true, "add_task_scope": true,
	// 任务控制与收尾
	"kill_work": true, "spawn_task": true, "wrapup": true, "Skill": true,
}

// toolVisibilityLimit 读取上限配置：0/负数 = 关闭（全部可见）。
func toolVisibilityLimit() int {
	v := strings.TrimSpace(os.Getenv("PANGU_TOOL_VISIBILITY_LIMIT"))
	if v == "" {
		return defaultToolVisibilityLimit
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultToolVisibilityLimit
	}
	if n < 0 {
		return 0
	}
	return n
}

// limitToolVisibility 把超出上限的工具名补进 DeferredInfo（schema 不下发）。
// 工具本身仍在 Options.Tools 中，保证 ExecuteExtraTool 可调用。
func limitToolVisibility(tools []actool.CoreTool, def DeferredInfo) DeferredInfo {
	limit := toolVisibilityLimit()
	if limit <= 0 || len(tools) <= limit {
		return def
	}
	visible := map[string]bool{}
	for _, t := range tools {
		if t != nil && essentialToolNames[t.Name()] {
			visible[t.Name()] = true
		}
	}
	// 按原顺序补到上限：先来的先可见，避免整类工具被藏光。
	for _, t := range tools {
		if t == nil || len(visible) >= limit {
			break
		}
		visible[t.Name()] = true
	}
	seen := map[string]bool{}
	for _, n := range def.Deferred {
		seen[n] = true
	}
	for _, t := range tools {
		if t == nil {
			continue
		}
		n := t.Name()
		if visible[n] || seen[n] {
			continue
		}
		seen[n] = true
		def.Deferred = append(def.Deferred, n)
		def.GlobalNames = append(def.GlobalNames, n)
	}
	return def
}
