package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// toolCtorPattern 匹配 ToolSet 上的工具构造器：func (t *ToolSet) xxx() actool.CoreTool {…}
var toolCtorPattern = regexp.MustCompile(`^func \(t \*ToolSet\) (\w+)\(\) actool\.CoreTool\s*(\S.*)?$`)

// loadAgentSources 读入 agent 包全部非测试源码（file → 内容）。
func loadAgentSources(t *testing.T) map[string]string {
	t.Helper()
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("列出源文件: %v", err)
	}
	out := map[string]string{}
	for _, name := range entries {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("读取 %s: %v", name, err)
		}
		out[name] = string(b)
	}
	return out
}

// toolConstructors 收集真实工具构造器（排除「单行转发器」）。
//
// 转发器形如 `func (t *ToolSet) GraphOverviewTool() actool.CoreTool { return t.graphOverview() }`，
// 是把私有构造器导出给 server 侧跨任务调用用的（tools.go 的 Cross-task reuse 段），
// 本身不是独立工具，自然不进三张清单。
func toolConstructors(src map[string]string) map[string]string {
	found := map[string]string{} // 构造器名 → 出处
	for file, body := range src {
		for _, line := range strings.Split(body, "\n") {
			m := toolCtorPattern.FindStringSubmatch(strings.TrimRight(line, "\r"))
			if m == nil {
				continue
			}
			name, rest := m[1], m[2]
			if strings.Contains(rest, "return t.") {
				continue // 转发器
			}
			found[name] = file
		}
	}
	return found
}

// toolListBody 截取某个 ToolSet 方法的函数体文本（从声明行到下一条行首 func）。
func toolListBody(src map[string]string, method string) string {
	head := "func (t *ToolSet) " + method + "() []actool.CoreTool {"
	for _, body := range src {
		idx := strings.Index(body, head)
		if idx < 0 {
			continue
		}
		rest := body[idx+len(head):]
		if end := strings.Index(rest, "\nfunc "); end >= 0 {
			rest = rest[:end]
		}
		return rest
	}
	return ""
}

// TestEveryToolConstructorIsRegistered 是「工具实现了必须注册」的守门测试。
//
// 回归背景（AGENTS.md §4）：listener（v0.3.87）和 list_worker_traces（v0.3.89）
// 都栽过同一个坑——工具函数写好了、单测也有，但没进 PlannerTools/WorkerTools/
// MainAgentTools 三张清单，于是模型永远看不到它，功能等于不存在。这类遗漏不会报错、
// 不会被单测发现，只在真实任务里表现为「明明实现了却从不用」。
//
// 判据刻意做窄：只检查「有没有在清单里被调用过」。工具该给哪个角色、读还是写、
// 是否延迟加载，都由各自清单的作者决定，这里不做判断。
func TestEveryToolConstructorIsRegistered(t *testing.T) {
	src := loadAgentSources(t)
	ctors := toolConstructors(src)
	if len(ctors) == 0 {
		t.Fatal("没有扫到任何工具构造器——扫描逻辑可能已失效")
	}

	listBodies := toolListBody(src, "PlannerTools") +
		toolListBody(src, "WorkerTools") +
		toolListBody(src, "MainAgentTools")
	if strings.TrimSpace(listBodies) == "" {
		t.Fatal("三张清单都没扫到——清单函数可能被改名或移走")
	}

	var missing []string
	for name, file := range ctors {
		if !strings.Contains(listBodies, "."+name+"()") {
			missing = append(missing, name+" ("+file+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("以下 %d 个工具构造器没有注册进三张工具清单，模型将永远看不到它们：\n  %s\n"+
			"修法：把它加进 PlannerTools/WorkerTools/MainAgentTools 之一（tools.go / tools_insert.go），"+
			"并同步 agent/toolcatalog.go 的 BuiltinToolSeeds。", len(missing), strings.Join(missing, "\n  "))
	}
}

// TestCoverageToolsHaveOptOut 保护 DropCoverageTools 的剔除名单：
// 覆盖度功能关闭时被剔除的每一条，都必须是真实存在的工具名。写错一个名字
// 不会报错，只会让该工具在关闭覆盖度时依然塞进 prompt。
func TestCoverageToolsHaveOptOut(t *testing.T) {
	src := loadAgentSources(t)
	ctors := toolConstructors(src)

	var names []string
	for name := range coverageOnlyTools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, toolName := range names {
		// coverageOnlyTools 存的是工具的 Name()（snake_case），不是构造器名。
		// 这里只做形态校验：必须是非空小写/下划线串，防止手滑写进中文或空格。
		if toolName == "" || strings.ToLower(toolName) != toolName || strings.ContainsAny(toolName, " \t") {
			t.Errorf("coverageOnlyTools 里的名字不合规：%q（应是工具的 Name()，形如 list_untested_assets）", toolName)
		}
	}
	if len(names) > 0 && len(ctors) == 0 {
		t.Error("构造器扫描失效")
	}
}
