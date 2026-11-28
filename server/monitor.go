package server

// ═══ 系统监测（只读运行时快照）═══
//
// 平台原有观测都是业务视角：总览（任务/发现/资产/Token）、洞察（趋势）、
// 日志（文本流）、情报同步进度、宿主工具体检。缺的是**进程与运行时视角**——
// goroutine 有没有在漏、连接池是否打满、某任务此刻有几个 planner/worker 在跑、
// activity 写库失败丢了多少条。这几项恰是长任务跑飞、池打满、内存上涨时唯一
// 能定位的东西，此前只能靠 grep 日志。
//
// 设计边界：
//   - **只读**：不新增表、不写库、不起后台协程，每次请求现算。
//   - **不引监控栈**：不用 Prometheus/OTel（与项目「不抄重型编排栈」一致），
//     前端 5 秒轮询足够——监测页是排障用，不是仪表盘级实时。
//   - 单个聚合端点，避免前端并发打十几个接口。

import (
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// processStart 是进程启动时刻，用于算 uptime。包级变量，进程内唯一。
var processStart = time.Now()

// monitorTask 是单个任务的运行时快照（只给监测页看，不含业务字段）。
type monitorTask struct {
	TaskID        string `json:"task_id"`
	Name          string `json:"name,omitempty"`
	Status        string `json:"status"`
	Queued        bool   `json:"queued,omitempty"`
	Paused        bool   `json:"paused,omitempty"`
	Inflight      int64  `json:"inflight"`
	LLMCalls      int64  `json:"llm_calls"`
	PlannerRound  int    `json:"planner_round"`
	ActivityDrops int64  `json:"activity_drops"`
}

// monitorWarning 是一条带处置建议的异常提示。给出建议而非只报数字——
// 监测页的价值在于「看到就知道下一步查什么」。
type monitorWarning struct {
	Level  string `json:"level"` // warn | info
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// systemMonitor 返回平台运行时快照（GET /api/system/monitor）。
func (s *Server) systemMonitor(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	now := time.Now()

	// ── 进程与运行时 ──
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	// 最近一次 GC 的暂停时长（PauseNs 是环形缓冲，用 NumGC-1 取最近一次）。
	var lastPauseMs float64
	if ms.NumGC > 0 {
		lastPauseMs = float64(ms.PauseNs[(ms.NumGC+255)%256]) / 1e6
	}
	process := map[string]any{
		"version":          BuildVersion,
		"go_version":       runtime.Version(),
		"os":               runtime.GOOS,
		"arch":             runtime.GOARCH,
		"num_cpu":          runtime.NumCPU(),
		"uptime_seconds":   int64(now.Sub(processStart).Seconds()),
		"goroutines":       runtime.NumGoroutine(),
		"heap_alloc_mb":    round1(float64(ms.HeapAlloc) / (1 << 20)),
		"heap_sys_mb":      round1(float64(ms.HeapSys) / (1 << 20)),
		"gc_count":         ms.NumGC,
		"gc_last_pause_ms": round1(lastPauseMs),
	}

	// ── 存储与连接池 ──
	st := pg.Stats()
	database := map[string]any{
		"open":                st.OpenConnections,
		"in_use":              st.InUse,
		"idle":                st.Idle,
		"max_open":            st.MaxOpenConnections,
		"wait_count":          st.WaitCount,
		"wait_ms":             st.WaitDuration.Milliseconds(),
		"max_idle_closed":     st.MaxIdleClosed,
		"max_lifetime_closed": st.MaxLifetimeClosed,
	}

	// ── 引擎实时 ──
	tasks := s.engine.MonitorTasks()
	running, queued, paused, terminal := 0, 0, 0, 0
	var drops int64
	var llmCalls int64
	for _, t := range tasks {
		drops += t.ActivityDrops
		llmCalls += t.LLMCalls
		switch {
		case t.Queued:
			queued++
		case t.Paused:
			paused++
		case isTerminalStatus(t.Status):
			terminal++
		default:
			running++
		}
	}
	engine := map[string]any{
		"tasks_total":      len(tasks),
		"tasks_running":    running,
		"tasks_queued":     queued,
		"tasks_paused":     paused,
		"tasks_terminal":   terminal,
		"llm_calls_active": llmCalls,
		"activity_drops":   drops,
		"sse_subscribers":  s.engine.Broadcaster().SubscriberCount(),
		"llm_ready":        s.engine.Ready(),
		"tasks":            tasks,
	}

	// ── 存储结构（schema 应用情况）──
	// schema.sql 每次启动全文 Exec，靠幂等兜底；这里暴露「本次建了多少表、花了多久」，
	// 否则「启动慢」和「迁移没生效」都只能靠猜。
	schemaInfo := map[string]any{}
	if st := pg.SchemaStatus(); st.Tables > 0 {
		tables := st.Tables
		// 实时数覆盖启动快照：Manager 初始化还会补建 4 张表（见 db.TableCount 注释）。
		if live, err := pg.TableCount(); err == nil && live > 0 {
			tables = live
		}
		schemaInfo = map[string]any{
			"tables":     tables,
			"created":    st.Created,
			"elapsed_ms": st.ElapsedMS,
			"lines":      st.Lines,
		}
	}

	// ── 依赖 ──
	trafficInfo := map[string]any{"enabled": false}
	if tr := s.m.Traffic(); tr != nil {
		n, _ := tr.Count()
		u := tr.Usage()
		days, maxMB := s.m.TrafficRetention()
		trafficInfo = map[string]any{
			"enabled": true, "exchanges": n,
			// 监听地址必须可见：这个代理无认证，绑 0.0.0.0 时整个局域网都能拿它
			// 当代理用（污染流量库、消耗带宽）。实测见过「1 分钟录 4332 条」这种
			// 量级，排查时第一眼就要能看出到底是谁在连。
			"listen": tr.ListenAddr(),
			// 目录占用：只显示条数看不见磁盘压力，而流量是全量明文落盘的。
			"usage_bytes":      u.Bytes,
			"usage_files":      u.Files,
			"index_bytes":      u.IndexBytes,
			"blob_bytes":       u.BlobBytes,
			"retention_days":   days,
			"retention_max_mb": maxMB,
		}
	}
	schedulerInfo := map[string]any{}
	if due, err := pg.TaskSchedules().Due(r.Context(), 100); err == nil {
		schedulerInfo["due_plans"] = len(due)
	}

	warnings := monitorWarnings(process, database, engine, trafficInfo)

	writeJSON(w, 200, map[string]any{
		"now":       now.Unix(),
		"process":   process,
		"database":  database,
		"schema":    schemaInfo,
		"engine":    engine,
		"traffic":   trafficInfo,
		"scheduler": schedulerInfo,
		"warnings":  warnings,
	})
}

// monitorWarnings 把「数字」翻译成「该查什么」。规则刻意保守：
// 只报有明确处置动作的异常，不制造噪音，否则监测页会被无视。
func monitorWarnings(process, database, engine, trafficInfo map[string]any) []monitorWarning {
	// 必须是非 nil 空切片：nil 会被序列化成 null，前端拿到 null 而不是 []，
	// 契约上「没有告警」应该是空数组。
	out := make([]monitorWarning, 0, 4)
	warn := func(title, detail string) {
		out = append(out, monitorWarning{Level: "warn", Title: title, Detail: detail})
	}

	if g, ok := process["goroutines"].(int); ok && g > 800 {
		warn("goroutine 数偏高",
			"当前 "+strconv.Itoa(g)+" 个。正常空载在几十量级；持续上涨通常意味着任务/连接/SQL 行未释放——"+
				"对照下方任务表的 inflight 看是否有任务卡在 running 但不干活。")
	}
	if wc, ok := database["wait_count"].(int64); ok && wc > 0 {
		warn("连接池发生过等待",
			"wait_count="+strconv.FormatInt(wc, 10)+"（累计）。持续增长说明连接不够用，会表现为接口变慢/超时；"+
				"先看 in_use 与 max_open，再考虑调大池或排查慢查询。")
	}
	if d, ok := engine["activity_drops"].(int64); ok && d > 0 {
		warn("有 activity 记录写库失败被丢弃",
			"累计 "+strconv.FormatInt(d, 10)+" 条。这些是 worker 的执行轨迹，丢了会导致会话页/trace 检索缺步——"+
				"在日志里搜「丢弃」看具体错误（常见是外键或字段超长）。")
	}
	if ready, _ := engine["llm_ready"].(bool); !ready {
		if q, ok := engine["tasks_queued"].(int); ok && q > 0 {
			warn("有任务在排队但 LLM 未就绪",
				"排队的 "+strconv.Itoa(q)+" 个任务不会自己开始。先在「LLM」页确认至少一个通道可用。")
		}
		if r, ok := engine["tasks_running"].(int); ok && r > 0 {
			// 实测发现的形态：任务 status=running，但 inflight=0、planner_round=0——
			// 表面上在跑，实际一轮都没执行。没有 LLM 时任务会一直空转到配置可用。
			warn("有任务在运行但 LLM 未就绪",
				strconv.Itoa(r)+" 个任务状态为运行中，但没有可用 LLM 通道，不会推进任何规划轮"+
					"（下方任务表的 inflight/规划轮应长期为 0）。先配置 LLM，任务会自动恢复。")
		}
	}
	if en, _ := trafficInfo["enabled"].(bool); !en {
		out = append(out, monitorWarning{
			Level:  "info",
			Title:  "流量录制未启用",
			Detail: "未启用时区间流量取证不可用，finding 无法绑定请求响应证据。需要时在「系统配置」打开。",
		})
	} else {
		if addr, _ := trafficInfo["listen"].(string); isWildcardListen(addr) {
			// 实测背景：容器沙箱要连这个代理时必须绑 0.0.0.0，代价是它成了一个
			// 无认证的开放代理——局域网内任何人都能用（污染流量库、白烧带宽）。
			// 曾见过 1 分钟 4332 条的量级，第一嫌疑就是这里。
			warn("流量录制代理对所有网卡开放",
				"监听 "+addr+"：该代理无认证，局域网内任何人都能把它当代理用——会往你的流量库里灌入"+
					"无关请求，也会占满带宽和磁盘。只有本机使用时改成 127.0.0.1:端口；"+
					"确需容器/远程访问时，用防火墙只放行可信网段。")
		}
		// 流量全量明文落盘，且默认没有清理策略；占用越大，升级/备份/排障越容易
		// 被磁盘拖死。5GB 是一个保守提示阈值，不自动删任何东西。
		if b, ok := trafficInfo["usage_bytes"].(int64); ok && b > 5<<30 {
			days, _ := trafficInfo["retention_days"].(int)
			maxMB, _ := trafficInfo["retention_max_mb"].(int)
			if days == 0 && maxMB == 0 {
				warn("流量目录已超过 5GB 且未设置保留策略",
					"当前占用 "+strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64)+"GB。"+
						"录制代理会持续落盘，长期运行会把磁盘占满；到「系统配置 → 流量保留」设置按天数或容量自动清理。")
			}
		}
	}
	return out
}

// isWildcardListen 判断监听地址是否对所有网卡开放。
func isWildcardListen(addr string) bool {
	addr = strings.TrimSpace(addr)
	return strings.HasPrefix(addr, "0.0.0.0") ||
		strings.HasPrefix(addr, ":") ||
		strings.HasPrefix(addr, "[::]")
}

func round1(f float64) float64 {
	return float64(int64(f*10+0.5)) / 10
}

// MonitorTasks 返回每个任务的运行时快照：在跑数、活跃 LLM 调用、规划轮次、
// activity 丢弃计数。只读内存句柄，不查库。
func (e *Engine) MonitorTasks() []monitorTask {
	tasks := e.m.List()
	out := make([]monitorTask, 0, len(tasks))
	for _, t := range tasks {
		snap := t.lifecycleSnapshot()
		var inflight, drops int64
		if v, ok := e.inflight.Load(t.ID); ok {
			if p, ok := v.(*int64); ok {
				inflight = atomic.LoadInt64(p)
			}
		}
		if v, ok := e.dropCnt.Load(t.ID); ok {
			if p, ok := v.(*int64); ok {
				drops = atomic.LoadInt64(p)
			}
		}
		var rounds int
		if v, ok := e.plannerRound.Load(t.ID); ok {
			rounds, _ = v.(int)
		}
		out = append(out, monitorTask{
			TaskID:        t.ID,
			Name:          snap.Name,
			Status:        snap.Status,
			Queued:        snap.Queued,
			Paused:        snap.Paused,
			Inflight:      inflight,
			LLMCalls:      e.ActiveLLMCalls(t.ID),
			PlannerRound:  rounds,
			ActivityDrops: drops,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out
}
