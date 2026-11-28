package server

import "net/http"

// registerOpsRoutes 注册运维与配置域路由：审计/GC、流量与命令回放、LLM 录制、
// 系统监测、全局设置、通知渠道、漏洞情报同步、代理池、报告与模板、MCP 服务端出口、
// 回归评测靶场与工具清单。
//
// 纯搬移自 Handler()，注册顺序与原实现一致。
func (s *Server) registerOpsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/audit", s.getAudit)
	mux.HandleFunc("POST /api/gc", s.gc)
	mux.HandleFunc("GET /api/traffic", s.getTraffic)
	mux.HandleFunc("GET /api/traffic/hosts", s.getTrafficHosts)
	mux.HandleFunc("DELETE /api/traffic", s.deleteTraffic)
	mux.HandleFunc("DELETE /api/traffic/hosts", s.deleteTrafficHosts)
	mux.HandleFunc("DELETE /api/traffic/all", s.deleteAllTraffic)
	mux.HandleFunc("GET /api/traffic/exchange", s.getTrafficExchange)
	mux.HandleFunc("GET /api/traffic/blob", s.getTrafficBlob)
	mux.HandleFunc("GET /api/commands", s.pgListCommands)
	mux.HandleFunc("GET /api/commands/stats", s.pgToolStats) // 按工具聚合调用次数
	mux.HandleFunc("GET /api/llm/records", s.pgListLLMRecords)
	mux.HandleFunc("DELETE /api/llm/records", s.pgDeleteLLMRecords)
	mux.HandleFunc("GET /api/llm/records/tasks", s.pgLLMTasks)
	mux.HandleFunc("GET /api/llm/records/by-model", s.pgTokenByModel) // 按模型聚合本任务 token 用量
	mux.HandleFunc("GET /api/llm/records/{id}", s.pgGetLLMRecord)
	// 系统监测：只读运行时快照（goroutine/连接池/引擎实时/依赖健康）。
	// 与业务仪表盘分工——这里回答「进程现在健康吗」，不回答「测出了什么」。
	mux.HandleFunc("GET /api/system/monitor", s.systemMonitor)

	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	// 漏洞 IM 推送。渠道是「多实例 + 各自过滤规则」的资源，因此独立成一组
	// REST 接口，而不是塞进扁平的 /api/settings 键值里。
	mux.HandleFunc("GET /api/notify/meta", s.notifyMeta)
	mux.HandleFunc("GET /api/notify/channels", s.notifyListChannels)
	mux.HandleFunc("POST /api/notify/channels", s.notifyCreateChannel)
	mux.HandleFunc("PATCH /api/notify/channels/{id}", s.notifyUpdateChannel)
	mux.HandleFunc("DELETE /api/notify/channels/{id}", s.notifyDeleteChannel)
	mux.HandleFunc("POST /api/notify/channels/{id}/test", s.notifyTestChannel)
	mux.HandleFunc("GET /api/notify/deliveries", s.notifyListDeliveries)
	mux.HandleFunc("POST /api/notify/deliveries/{id}/retry", s.notifyRetryDelivery)
	mux.HandleFunc("GET /api/vulnintel/status", s.vulnintelStatus)
	mux.HandleFunc("PUT /api/vulnintel/config", s.vulnintelSaveConfig)
	mux.HandleFunc("POST /api/vulnintel/sync", s.vulnintelSyncNow)
	mux.HandleFunc("POST /api/vulnintel/sync/cancel", s.vulnintelSyncCancel)
	mux.HandleFunc("POST /api/vulnintel/probe", s.vulnintelProbe)
	mux.HandleFunc("GET /api/proxypool", s.proxyPoolGet)
	mux.HandleFunc("PUT /api/proxypool", s.proxyPoolPut)
	mux.HandleFunc("POST /api/proxypool/bulk", s.proxyPoolBulk)
	mux.HandleFunc("POST /api/proxypool/check", s.proxyPoolCheck)
	mux.HandleFunc("POST /api/settings/web-search/test", s.testWebSearch)
	mux.HandleFunc("GET /api/report", s.getReport)
	mux.HandleFunc("GET /api/report/summary", s.reportSummaryGet)
	mux.HandleFunc("PUT /api/report/summary", s.reportSummaryPut)
	mux.HandleFunc("POST /api/report/summary", s.reportSummaryGenerate)
	mux.HandleFunc("GET /api/report/export", s.exportTaskDocx)
	// 报告多格式（M-10）：Excel 漏洞清单 + PDF（依赖宿主 LibreOffice）。
	mux.HandleFunc("GET /api/report/export.xlsx", s.exportTaskFindingsXLSX)
	mux.HandleFunc("GET /api/report/export.pdf", s.exportTaskPDF)
	// 报告模板（M-10）：页眉/密级/委托方覆盖 + 章节开关。
	mux.HandleFunc("GET /api/report-templates", s.listReportTemplates)
	mux.HandleFunc("POST /api/report-templates", s.createReportTemplate)
	mux.HandleFunc("PATCH /api/report-templates/{id}", s.updateReportTemplate)
	mux.HandleFunc("DELETE /api/report-templates/{id}", s.deleteReportTemplate)
	mux.HandleFunc("POST /api/report-templates/{id}/default", s.setDefaultReportTemplate)
	// MCP 服务端出口（M-13）：把平台只读能力暴露给外部 Agent（JWT / API Key 均可）。
	// 注意与 /api/mcp（MCP 客户端配置 CRUD）区分：这里是对外的服务端端点。
	mux.HandleFunc("POST /api/mcp/server", s.mcpEndpoint)
	// 回归评测靶场（M-14）：标准答案 CRUD + 按答案判卷。
	mux.HandleFunc("GET /api/eval/benchmarks", s.listEvalBenchmarks)
	mux.HandleFunc("POST /api/eval/benchmarks", s.createEvalBenchmark)
	mux.HandleFunc("DELETE /api/eval/benchmarks/{id}", s.deleteEvalBenchmark)
	mux.HandleFunc("POST /api/eval/benchmarks/{id}/score", s.scoreEvalBenchmark)
	mux.HandleFunc("GET /api/findings/{id}/export", s.exportFindingDocx)
	mux.HandleFunc("GET /api/tools/inventory", s.toolsInventory)
}
