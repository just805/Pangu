package server

import (
	"net/http"
	"testing"

	"github.com/just805/pangu/db"
)

// TestRequiredAPIKeyScope 锁住「路径 → 所需权限档」的映射。这是开放 API 的
// 授权边界，规则改动必须先在这里显式改断言，不能靠人工 review 记。
func TestRequiredAPIKeyScope(t *testing.T) {
	cases := []struct {
		method, path, want string
		why                string
	}{
		// 只读查询
		{http.MethodGet, "/api/tasks", db.APIKeyScopeRead, "任务列表只读"},
		{http.MethodHead, "/api/stats", db.APIKeyScopeRead, "HEAD 同 GET"},
		{http.MethodGet, "/api/findings/12", db.APIKeyScopeRead, "发现详情只读"},

		// 业务写
		{http.MethodPost, "/api/tasks", db.APIKeyScopeWrite, "建任务属业务写"},
		{http.MethodDelete, "/api/findings/12", db.APIKeyScopeWrite, "删发现属业务写"},
		{http.MethodPost, "/api/intercept/pending/1/decide", db.APIKeyScopeWrite, "审批决定属业务写"},

		// 首段匹配不能误伤：/api/llm 是配置，/api/llm-records 是只读记录
		{http.MethodGet, "/api/llm-records", db.APIKeyScopeRead, "LLM 请求记录只读"},
		{http.MethodGet, "/api/llm/profiles", db.APIKeyScopeAdmin, "LLM 通道配置是全权"},
		{http.MethodPost, "/api/llm/profiles", db.APIKeyScopeAdmin, "改 LLM 配置是全权"},

		// 平台能力边界
		{http.MethodPost, "/api/tools", db.APIKeyScopeAdmin, "自定义工具=任意代码"},
		{http.MethodPost, "/api/hostenv/install", db.APIKeyScopeAdmin, "装宿主工具=任意包"},
		{http.MethodPost, "/api/update/apply", db.APIKeyScopeAdmin, "一键更新"},
		{http.MethodPost, "/api/settings", db.APIKeyScopeAdmin, "改全局设置"},
		{http.MethodGet, "/api/api-keys", db.APIKeyScopeAdmin, "能读别人的 Key 列表也不行"},

		// 认证面：能改密码/踢会话就必须全权
		{http.MethodPost, "/api/auth/change-password", db.APIKeyScopeAdmin, "改密码"},
		{http.MethodPost, "/api/auth/revoke-all-sessions", db.APIKeyScopeAdmin, "吊销全部会话"},

		// 精确例外：对外 MCP 出口目前只暴露只读工具
		{http.MethodPost, "/api/mcp/server", db.APIKeyScopeRead, "MCP 只读出口"},
		{http.MethodGet, "/api/mcp", db.APIKeyScopeAdmin, "MCP 客户端配置仍是全权"},

		// 未知路径：非 GET 至少 write，GET 只读
		{http.MethodPost, "/api/brand-new-endpoint", db.APIKeyScopeWrite, "未知写端点"},
		{http.MethodGet, "/api/brand-new-endpoint", db.APIKeyScopeRead, "未知读端点"},
	}
	for _, c := range cases {
		if got := requiredAPIKeyScope(c.method, c.path); got != c.want {
			t.Errorf("%s %s (%s) => %q, want %q", c.method, c.path, c.why, got, c.want)
		}
	}
}

// TestAuthExemptPath 锁住免鉴权白名单：多放一个端点就是多一个未授权入口。
func TestAuthExemptPath(t *testing.T) {
	exempt := []string{"/api/health", "/api/auth/status", "/api/auth/init", "/api/auth/login", "/api/auth/logout"}
	for _, p := range exempt {
		if !authExemptPath(p) {
			t.Errorf("%s 应当免鉴权", p)
		}
	}
	// 这三条一旦被前缀豁免，一把 read 档 Key 就能接管或踢掉管理员。
	for _, p := range []string{
		"/api/auth/change-password",
		"/api/auth/revoke-all-sessions",
		"/api/auth/revoke-current",
		"/api/tasks",
	} {
		if authExemptPath(p) {
			t.Errorf("%s 不应免鉴权", p)
		}
	}
}
