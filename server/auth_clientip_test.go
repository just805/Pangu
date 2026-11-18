package server

import (
	"net/http/httptest"
	"testing"
)

// clientIP 只在直连对端属于 PANGU_TRUSTED_PROXIES 网段时才采信 XFF/X-Real-IP。
// 公网直连部署下无条件信任代理头 = 攻击者伪造无限 IP 绕过 per-IP 防爆破，并把
// 伪造失败 IP 放大成全局登录锁（管理员被锁死）。
func TestClientIPIgnoresForwardedHeadersWithoutTrustedProxy(t *testing.T) {
	t.Setenv("PANGU_TRUSTED_PROXIES", "")

	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "203.0.113.7:55555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	r.Header.Set("X-Real-IP", "9.9.9.9")
	if got := clientIP(r); got != "203.0.113.7" {
		t.Fatalf("无可信代理时 clientIP = %q, want RemoteAddr 203.0.113.7（伪造 XFF 不得采信）", got)
	}

	// XFF 首段是合法 IP 也不能采信——判定依据是直连对端，不是头内容。
	r = httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "203.0.113.7:55555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := clientIP(r); got != "203.0.113.7" {
		t.Fatalf("无可信代理时单段 XFF 也不得采信，got %q", got)
	}
}

func TestClientIPHonorForwardedHeadersBehindTrustedProxy(t *testing.T) {
	t.Setenv("PANGU_TRUSTED_PROXIES", "127.0.0.1,10.0.0.0/8,::1")

	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "10.1.2.3:44444" // 反代在可信网段内
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.2")
	if got := clientIP(r); got != "203.0.113.7" {
		t.Fatalf("可信反代下 clientIP = %q, want XFF 首段 203.0.113.7", got)
	}

	// XFF 缺失时退到 X-Real-IP。
	r = httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "10.1.2.3:44444"
	r.Header.Set("X-Real-IP", "198.51.100.4")
	if got := clientIP(r); got != "198.51.100.4" {
		t.Fatalf("可信反代下无 XFF 时应取 X-Real-IP, got %q", got)
	}

	// 反代不在可信网段：代理头不采信。
	r = httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "203.0.113.9:44444"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("非可信对端带 XFF 应取 RemoteAddr, got %q", got)
	}

	// IPv6 可信代理 + IPv6 客户端。
	r = httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "[::1]:44444"
	r.Header.Set("X-Forwarded-For", "2001:db8::1")
	if got := clientIP(r); got != "2001:db8::1" {
		t.Fatalf("IPv6 可信代理下应取 XFF, got %q", got)
	}
}
