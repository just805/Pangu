package server

import (
	"strings"
	"testing"
)

// monitorWarnings 是监测页的灵魂：把数字翻译成「该查什么」。
// 这里锁住两个方向——该报的必须报，不该报的不能制造噪音。
func TestMonitorWarningsNormal(t *testing.T) {
	got := monitorWarnings(
		map[string]any{"goroutines": 42},
		map[string]any{"wait_count": int64(0)},
		map[string]any{"activity_drops": int64(0), "tasks_queued": 0, "llm_ready": true},
		map[string]any{"enabled": true},
	)
	if len(got) != 0 {
		t.Errorf("健康状态下不该有提示，实际 %d 条：%+v", len(got), got)
	}
}

func TestMonitorWarningsGoroutines(t *testing.T) {
	got := monitorWarnings(
		map[string]any{"goroutines": 1500},
		map[string]any{"wait_count": int64(0)},
		map[string]any{"activity_drops": int64(0), "tasks_queued": 0, "llm_ready": true},
		map[string]any{"enabled": true},
	)
	if len(got) != 1 || !strings.Contains(got[0].Title, "goroutine") {
		t.Fatalf("应报 goroutine 偏高：%+v", got)
	}
	if !strings.Contains(got[0].Detail, "1500") {
		t.Errorf("提示应带上实际数字：%s", got[0].Detail)
	}
}

func TestMonitorWarningsPoolWait(t *testing.T) {
	got := monitorWarnings(
		map[string]any{"goroutines": 10},
		map[string]any{"wait_count": int64(7), "in_use": 10, "max_open": 10},
		map[string]any{"activity_drops": int64(0), "tasks_queued": 0, "llm_ready": true},
		map[string]any{"enabled": true},
	)
	if len(got) != 1 || !strings.Contains(got[0].Title, "连接池") {
		t.Fatalf("应报连接池等待：%+v", got)
	}
}

func TestMonitorWarningsActivityDrops(t *testing.T) {
	got := monitorWarnings(
		map[string]any{"goroutines": 10},
		map[string]any{"wait_count": int64(0)},
		map[string]any{"activity_drops": int64(3), "tasks_queued": 0, "llm_ready": true},
		map[string]any{"enabled": true},
	)
	if len(got) != 1 || !strings.Contains(got[0].Title, "activity") {
		t.Fatalf("应报 activity 丢弃：%+v", got)
	}
}

// 排队 + LLM 未就绪才是问题；LLM 就绪时排队是正常的并发上限行为，不该报警。
func TestMonitorWarningsQueuedOnlyWhenLLMNotReady(t *testing.T) {
	base := func(ready bool) []monitorWarning {
		return monitorWarnings(
			map[string]any{"goroutines": 10},
			map[string]any{"wait_count": int64(0)},
			map[string]any{"activity_drops": int64(0), "tasks_queued": 2, "tasks_running": 0, "llm_ready": ready},
			map[string]any{"enabled": true},
		)
	}
	if got := base(true); len(got) != 0 {
		t.Errorf("LLM 就绪时的排队是正常行为，不该报警：%+v", got)
	}
	if got := base(false); len(got) != 1 || !strings.Contains(got[0].Title, "排队") {
		t.Errorf("LLM 未就绪且有排队时应报警：%+v", got)
	}
}

// 实测发现的形态：任务 status=running、inflight=0、planner_round=0。
// 表面上在跑，实际一轮都没执行——监测页必须点出来，否则「任务卡住」无从发现。
func TestMonitorWarningsRunningWithoutLLM(t *testing.T) {
	got := monitorWarnings(
		map[string]any{"goroutines": 10},
		map[string]any{"wait_count": int64(0)},
		map[string]any{"activity_drops": int64(0), "tasks_queued": 0, "tasks_running": 2, "llm_ready": false},
		map[string]any{"enabled": true},
	)
	if len(got) != 1 || !strings.Contains(got[0].Title, "运行") {
		t.Fatalf("任务在运行但 LLM 未就绪时应报警：%+v", got)
	}
	// LLM 就绪时 running 且 inflight=0 也可能是收尾期，不该由这条规则报。
	ok := monitorWarnings(
		map[string]any{"goroutines": 10},
		map[string]any{"wait_count": int64(0)},
		map[string]any{"activity_drops": int64(0), "tasks_queued": 0, "tasks_running": 2, "llm_ready": true},
		map[string]any{"enabled": true},
	)
	if len(ok) != 0 {
		t.Errorf("LLM 就绪时不该由该规则报警：%+v", ok)
	}
}

// 流量录制未启用是 info 级（不是故障），但必须提示——否则取证缺口没人知道。
func TestMonitorWarningsTrafficOff(t *testing.T) {
	got := monitorWarnings(
		map[string]any{"goroutines": 10},
		map[string]any{"wait_count": int64(0)},
		map[string]any{"activity_drops": int64(0), "tasks_queued": 0, "llm_ready": true},
		map[string]any{"enabled": false},
	)
	if len(got) != 1 || got[0].Level != "info" {
		t.Fatalf("流量关闭应是 info 级提示：%+v", got)
	}
}

func TestRound1(t *testing.T) {
	cases := map[float64]float64{0: 0, 1.26: 1.3, 1.24: 1.2, 42.05: 42.1}
	for in, want := range cases {
		if got := round1(in); got != want {
			t.Errorf("round1(%v)=%v, want %v", in, got, want)
		}
	}
}
