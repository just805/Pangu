package server

import "net/http"

// registerAssetRoutes 注册资产/探索图域路由：资产台账与快照、企业归属、
// 探索图（意图/事实/发现/活动）、token 统计与证据附件。
//
// 纯搬移自 Handler()，注册顺序与原实现一致；findings 的流量绑定子路由仍由
// registerFindingTraffic 注册，避免与 finding_traffic.go 的归属分裂。
func (s *Server) registerAssetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/assets", s.listAssets)
	mux.HandleFunc("GET /api/assets/counts", s.assetCounts)
	mux.HandleFunc("POST /api/assets", s.insertAssets)
	mux.HandleFunc("DELETE /api/assets", s.deleteAssets)
	// 资产快照与变更检测（M-07）：采快照 / 列快照 / 比较两次快照。
	mux.HandleFunc("POST /api/assets/snapshots", s.takeAssetSnapshot)
	mux.HandleFunc("GET /api/assets/snapshots", s.listAssetSnapshots)
	mux.HandleFunc("GET /api/assets/snapshots/{id}/diff", s.assetSnapshotDiff)

	// company system
	mux.HandleFunc("GET /api/companies", s.listCompanies)
	mux.HandleFunc("POST /api/companies", s.createCompany)
	mux.HandleFunc("GET /api/companies/{id}", s.getCompany)
	mux.HandleFunc("DELETE /api/companies/{id}", s.deleteCompany)
	mux.HandleFunc("POST /api/companies/{id}/scope", s.addCompanyScope)
	mux.HandleFunc("POST /api/companies/reattribute", s.reattribute)

	mux.HandleFunc("GET /api/exploration/frontier", s.frontier)
	mux.HandleFunc("GET /api/exploration/findings", s.findings)
	mux.HandleFunc("GET /api/exploration/findings/groups", s.findingGroups)
	mux.HandleFunc("GET /api/exploration/findings/asset-tree", s.findingAssetTree)
	mux.HandleFunc("GET /api/exploration/findings/stats", s.findingStats)
	mux.HandleFunc("GET /api/exploration/findings/export", s.findingsExport)
	s.registerFindingTraffic(mux)
	mux.HandleFunc("GET /api/exploration/findings/{id}", s.getFinding)
	mux.HandleFunc("GET /api/exploration/findings/{id}/lineage", s.findingLineage)
	mux.HandleFunc("POST /api/exploration/findings/{id}/deepen", s.deepenFinding)
	mux.HandleFunc("GET /api/exploration/findings/{id}/retests", s.listFindingRetests)
	mux.HandleFunc("GET /api/exploration/findings/retests/active", s.listActiveFindingRetests)
	mux.HandleFunc("POST /api/exploration/findings/{id}/retests", s.startFindingRetest)
	mux.HandleFunc("PATCH /api/exploration/findings/{id}", s.patchFinding)
	mux.HandleFunc("DELETE /api/exploration/findings/{id}", s.deleteFinding)
	mux.HandleFunc("GET /api/exploration/intents", s.intents)
	mux.HandleFunc("GET /api/exploration/graph", s.explorationGraph)
	mux.HandleFunc("GET /api/exploration/nodes", s.explorationNodes)
	mux.HandleFunc("GET /api/exploration/activity", s.activity)
	mux.HandleFunc("GET /api/exploration/activity/history", s.activityHistory)
	mux.HandleFunc("GET /api/exploration/main-sessions", s.mainSessions)
	mux.HandleFunc("POST /api/exploration/main-session/new", s.newMainSession)
	mux.HandleFunc("GET /api/exploration/activity/stream", s.streamActivity)
	mux.HandleFunc("GET /api/exploration/activity/{seq}", s.activityDetail)
	mux.HandleFunc("GET /api/exploration/tokens", s.tokenStats)
	mux.HandleFunc("GET /api/tokens/daily", s.tokenDailyStats)
	mux.HandleFunc("GET /api/tokens/conversations", s.conversationTokens)
	mux.HandleFunc("GET /api/tokens/usage", s.pgUsageStats)            // 全局 llm_usage 聚合（仪表盘新版视图）
	mux.HandleFunc("GET /api/tokens/cost", s.pgUsageCost)              // 成本估算（内置参考价，?task= 看单任务）
	mux.HandleFunc("POST /api/tasks/{id}/auth-check", s.taskAuthCheck) // 登录凭据自检（真实发一次登录请求）
	// 证据附件（v0.5.0）：列表 / 单文件下载 / 证据包 zip。
	mux.HandleFunc("GET /api/tasks/{id}/artifacts", s.listTaskArtifacts)
	mux.HandleFunc("PATCH /api/tasks/{id}/artifacts/{aid}", s.patchTaskArtifact)    // 改归属（任务级 ↔ 漏洞）
	mux.HandleFunc("POST /api/tasks/{id}/artifacts/scan", s.scanTaskArtifacts)      // 历史目录补登记
	mux.HandleFunc("POST /api/tasks/{id}/artifacts/auto-bind", s.autoBindArtifacts) // 智能关联（v0.4.42）
	mux.HandleFunc("GET /api/tasks/{id}/artifacts/export", s.exportTaskArtifacts)
	mux.HandleFunc("GET /api/tasks/{id}/artifacts/{aid}", s.serveTaskArtifact)
	mux.HandleFunc("GET /api/tasks/{id}/artifacts/{aid}/preview/{page}", s.serveArtifactPreview)
	mux.HandleFunc("GET /api/tasks/{id}/artifacts/{aid}/data-preview", s.serveArtifactDataPreview) // 数据文件在线查看（v0.4.41）
	mux.HandleFunc("POST /api/tasks/{id}/artifacts/{aid}/preview", s.regenerateArtifactPreview)
	mux.HandleFunc("DELETE /api/tasks/{id}/artifacts/{aid}", s.deleteTaskArtifact) // 删除附件（配额触顶后的补救路径）
}
