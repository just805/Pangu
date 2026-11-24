package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/just805/pangu/db"
)

// TestTOTPCode 用 RFC 6238 SHA1 测试向量钉住实现（密钥 "12345678901234567890"
// 的 Base32 形式；T=59s 时 8 位码 94287082 → 6 位取后 6 位 287082）。
func TestTOTPCode(t *testing.T) {
	code, err := totpCode("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", time.Unix(59, 0))
	if err != nil {
		t.Fatal(err)
	}
	if code != "287082" {
		t.Fatalf("totp=%s want 287082", code)
	}
	if _, err := totpCode("not-base32!!!", time.Now()); err == nil {
		t.Fatal("invalid base32 must error")
	}
}

// TestRunAuthCheck 用本地 httptest 验证三种登录形态与成功判定，并确认结果
// 不回显密码（日志/详情都没有）。目标站点不需要真实网络。
func TestRunAuthCheck(t *testing.T) {
	var gotUser, gotPass, gotTOTP string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if stringsHasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			_ = r.ParseForm()
			gotUser, gotPass, gotTOTP = r.PostForm.Get("user"), r.PostForm.Get("pass"), r.PostForm.Get("totp")
		} else {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotUser, gotPass, gotTOTP = body["user"], body["pass"], body["totp"]
		}
		if gotUser == "alice" && gotPass == "s3cret" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"token":"ok-token"}`))
			return
		}
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"bad credentials"}`))
	}))
	defer srv.Close()

	base := &db.TaskAuthConfig{
		Enabled: true, LoginURL: srv.URL + "/login", Method: "POST",
		ContentType: "json", UsernameField: "user", PasswordField: "pass",
		Username: "alice", Password: "s3cret", SuccessContains: "token",
	}
	res := runAuthCheck(base, nil)
	if !res.OK || res.StatusCode != 200 {
		t.Fatalf("json login should pass: %+v", res)
	}
	if gotTOTP != "" {
		t.Fatal("totp must not be sent when unconfigured")
	}

	// 失败凭据：401 + 缺 token → 不通过，且详情不含密码。
	bad := *base
	bad.Password = "wrong-pass"
	res = runAuthCheck(&bad, nil)
	if res.OK {
		t.Fatalf("bad credentials must fail: %+v", res)
	}
	if stringsContains(res.Detail, "wrong-pass") {
		t.Fatal("detail leaked password")
	}

	// form 形态
	form := *base
	form.ContentType = "form"
	res = runAuthCheck(&form, nil)
	if !res.OK || gotUser != "alice" || gotPass != "s3cret" {
		t.Fatalf("form login should pass: %+v user=%s pass=%s", res, gotUser, gotPass)
	}

	// 未启用：明确说明而不是误判成功。
	off := db.TaskAuthConfig{}
	if res := runAuthCheck(&off, nil); res.OK {
		t.Fatal("disabled config must not report ok")
	}
}

func stringsHasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
func stringsContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// 自定义 http 工具与登录自检的出口链路：
// - 代理地址同时接受 host:port 与 scheme://host:port（此前勾选记录代理时
//   无条件二次拼 scheme，已带 scheme 的地址变成 http://http://… 而失效）；
// - 未配置 proxy 时默认走平台出口（Manager.ProxyAddr），不再裸连。

func TestNormalizeProxyAddr(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"127.0.0.1:8788", "http://127.0.0.1:8788"},
		{"  127.0.0.1:8788  ", "http://127.0.0.1:8788"},
		{"http://127.0.0.1:8788", "http://127.0.0.1:8788"},
		{"socks5://10.0.0.1:1080", "socks5://10.0.0.1:1080"},
		{"", ""},
	} {
		if got := normalizeProxyAddr(tc.in); got != tc.want {
			t.Errorf("normalizeProxyAddr(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

// TestHTTPToolProxyTransport：未配置 proxy / 勾选记录代理 → 走平台出口；
// 显式 proxy（两种写法）→ 覆盖。
func TestHTTPToolProxyTransport(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	// SetGlobalProxy 有 scheme 校验（裸 host:port 会被拒），平台配置用带 scheme 的。
	if err := m.SetGlobalProxy("http://127.0.0.1:8788"); err != nil {
		t.Fatalf("SetGlobalProxy: %v", err)
	}
	s := &Server{m: m}
	getProxy := func(tr *http.Transport) string {
		if tr == nil || tr.Proxy == nil {
			return ""
		}
		u, err := tr.Proxy(nil)
		if err != nil {
			return "ERR"
		}
		return u.String()
	}
	// 未配置 proxy → 平台出口（自动补 scheme，无双拼）。
	tr := s.httpProxyTransport(httpExec{})
	if got := getProxy(tr); got != "http://127.0.0.1:8788" {
		t.Fatalf("default egress proxy = %q", got)
	}
	// use_recording_proxy → 同样是平台出口（捕获关时 ProxyAddr=全局代理）。
	tr = s.httpProxyTransport(httpExec{UseRecordingProxy: true, Proxy: "http://should-be-ignored:1"})
	if got := getProxy(tr); got != "http://127.0.0.1:8788" {
		t.Fatalf("recording-proxy branch = %q", got)
	}
	// 显式 proxy 覆盖（裸 host:port 与 scheme:// 都接受）。
	tr = s.httpProxyTransport(httpExec{Proxy: "10.0.0.9:3128"})
	if got := getProxy(tr); got != "http://10.0.0.9:3128" {
		t.Fatalf("explicit bare proxy = %q", got)
	}
	tr = s.httpProxyTransport(httpExec{Proxy: "socks5://10.0.0.9:1080"})
	if got := getProxy(tr); got != "socks5://10.0.0.9:1080" {
		t.Fatalf("explicit scheme proxy = %q", got)
	}
	// 平台未配置代理：普通目标必须直连。隐藏服务默认启用，transport 可能非 nil
	//（.onion/.i2p 需要专用上游），但普通 host 的 Proxy 必须返回 nil。
	if err := m.SetGlobalProxy(""); err != nil {
		t.Fatalf("SetGlobalProxy clear: %v", err)
	}
	if tr := s.httpProxyTransport(httpExec{}); tr != nil {
		req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
		if u, err := tr.Proxy(req); err != nil || u != nil {
			t.Fatalf("no platform proxy: normal host must be direct, got %v (%v)", u, err)
		}
	}
}
