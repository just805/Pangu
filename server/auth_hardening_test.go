package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestLimiter 构造字段齐全的独立实例——共享单例 loginLimit 会被其它测试污染。
func newTestLimiter() *loginLimiter {
	return &loginLimiter{
		fails:       map[string][]time.Time{},
		lockedUntil: map[string]time.Time{},
	}
}

// 全局登录锁必须要求失败来自多个独立来源（loginGlobalMinIPs）：单源攻击者
// （哪怕伪造 XFF——clientIP 修复后伪造已无效）只会把自己锁进 per-IP 闸门，
// 不能把全体管理员的登录拖进全局锁。否则攻击者每 5 分钟发 30 个坏登录就能
// 让管理员无限期 429（公网 DoS）。
func TestLoginGlobalLockRequiresDistinctIPs(t *testing.T) {
	l := newTestLimiter()
	// 同一 IP 失败 30 次：只 per-IP 锁，不触发全局锁。
	for i := 0; i < loginGlobalMaxFails; i++ {
		l.recordFail("203.0.113.7")
	}
	ok, _ := l.allow("198.51.100.1")
	if !ok {
		t.Fatal("单 IP 失败 30 次不应触发全局锁（其他来源的登录被无辜拒之门外）")
	}
	okSelf, _ := l.allow("203.0.113.7")
	if okSelf {
		t.Fatal("攻击 IP 自身应被 per-IP 闸门锁住")
	}
}

func TestLoginGlobalLockTriggersOnDistinctIPs(t *testing.T) {
	l := newTestLimiter()
	// 5 个不同 IP 各失败 6 次 = 30 次 ≥ 阈值且独立来源达标 → 全局锁。
	for i := 0; i < loginGlobalMinIPs; i++ {
		for j := 0; j < loginGlobalMaxFails/loginGlobalMinIPs; j++ {
			l.recordFail("198.51.100." + string(rune('1'+i)))
		}
	}
	ok, wait := l.allow("203.0.113.99")
	if ok {
		t.Fatal("多 IP 分布式失败应触发全局锁")
	}
	if wait <= 0 {
		t.Fatalf("全局锁应带剩余锁定时长, got %v", wait)
	}
}

// ?token= 通道整体移除：token 进访问日志/Referer 是会话劫持面，SSE 认证收敛到
// HttpOnly cookie。任何路径下 query 里的 token 都不再被采信。
func TestExtractTokenRejectsQueryToken(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/logs/stream?token=leaked-jwt", nil)
	if got := extractToken(r); got != "" {
		t.Fatalf("query token 应被拒绝（历史通道已移除）, got %q", got)
	}
	// Authorization 头与 cookie 依旧有效。
	r = httptest.NewRequest("GET", "/api/logs/stream", nil)
	r.Header.Set("Authorization", "Bearer header-jwt")
	if got := extractToken(r); got != "header-jwt" {
		t.Fatalf("Authorization 头应优先, got %q", got)
	}
	r = httptest.NewRequest("GET", "/api/logs/stream", nil)
	r.AddCookie(&http.Cookie{Name: "pangu_token", Value: "cookie-jwt"})
	if got := extractToken(r); got != "cookie-jwt" {
		t.Fatalf("cookie 认证应生效, got %q", got)
	}
}

// 登录/初始化响应下发的会话 cookie 必须是 HttpOnly：XSS 偷不走，token 不再落
// localStorage/可读 cookie。
func TestSetAuthCookieIsHttpOnly(t *testing.T) {
	t.Setenv("PANGU_COOKIE_SECURE", "")
	w := httptest.NewRecorder()
	setAuthCookie(w, "jwt-value")
	var c *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "pangu_token" {
			c = ck
		}
	}
	if c == nil {
		t.Fatal("未下发 pangu_token cookie")
	}
	if !c.HttpOnly {
		t.Fatal("会话 cookie 必须 HttpOnly（XSS 防护）")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite 应为 Lax, got %v", c.SameSite)
	}
	if c.MaxAge <= 0 {
		t.Fatalf("MaxAge 应为正（7 天）, got %d", c.MaxAge)
	}

	// PANGU_COOKIE_SECURE=1 时加 Secure（TLS 部署开关）。
	t.Setenv("PANGU_COOKIE_SECURE", "1")
	w2 := httptest.NewRecorder()
	setAuthCookie(w2, "jwt-value")
	var c2 *http.Cookie
	for _, ck := range w2.Result().Cookies() {
		if ck.Name == "pangu_token" {
			c2 = ck
		}
	}
	if c2 == nil || !c2.Secure {
		t.Fatal("PANGU_COOKIE_SECURE=1 时 cookie 应带 Secure")
	}
}

// 导出脱敏：命中敏感头名的行值替换为 [REDACTED]，头名与请求形状保留。
func TestRedactHead(t *testing.T) {
	head := "POST /api/v1/login HTTP/1.1\r\nHost: target.example\r\nAuthorization: Bearer target-session-token\r\nCookie: session=abc; other=1\r\nX-Custom: keep-me\r\n"
	got := redactHead(head)
	if strings.Contains(got, "target-session-token") || strings.Contains(got, "session=abc") {
		t.Fatalf("敏感值未脱敏: %q", got)
	}
	for _, want := range []string{"Authorization: [REDACTED]", "Cookie: [REDACTED]", "X-Custom: keep-me", "Host: target.example"} {
		if !strings.Contains(got, want) {
			t.Fatalf("脱敏结果缺 %q: %q", want, got)
		}
	}
	// 空头块原样返回；非敏感头不误伤。
	if redactHead("") != "" {
		t.Fatal("空头块应原样返回")
	}
	if got := redactHead("X-Forwarded-For: 1.2.3.4"); got != "X-Forwarded-For: 1.2.3.4" {
		t.Fatalf("非敏感头不应被改: %q", got)
	}
}
