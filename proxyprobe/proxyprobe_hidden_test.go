package proxyprobe

import (
	"net/http"
	"testing"
)

func TestHiddenKind(t *testing.T) {
	cases := map[string]string{
		"abc.onion":         "tor",
		"x.y.onion:8080":    "tor",
		"ABC.ONION":         "tor",
		"abc.i2p":           "i2p",
		"sub.abc.i2p":       "i2p",
		"example.com":       "",
		"onion.example.com": "",
		"i2p":               "",
		"127.0.0.1:9050":    "",
	}
	for host, want := range cases {
		if got := HiddenKind(host); got != want {
			t.Errorf("HiddenKind(%q) = %q, want %q", host, got, want)
		}
	}
}

func mustReq(t *testing.T, raw string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestConfigureTransportRoutesByHost(t *testing.T) {
	tr := &http.Transport{}
	err := ConfigureTransport(tr, "http://127.0.0.1:8080", HiddenProxies{
		Tor: "socks5h://127.0.0.1:9050",
		I2P: "http://127.0.0.1:4444",
	})
	if err != nil {
		t.Fatalf("ConfigureTransport: %v", err)
	}
	if tr.Proxy == nil || tr.DialContext == nil {
		t.Fatal("transport must configure both Proxy and DialContext")
	}

	// .onion 走 Tor SOCKS：Proxy 返回 nil（由 DialContext 处理）。
	if u, err := tr.Proxy(mustReq(t, "http://abc.onion/")); err != nil || u != nil {
		t.Fatalf(".onion Proxy = %v, %v; want nil,nil", u, err)
	}
	// .i2p 走 I2P HTTP 代理。
	u, err := tr.Proxy(mustReq(t, "http://abc.i2p/"))
	if err != nil || u == nil || u.Host != "127.0.0.1:4444" {
		t.Fatalf(".i2p Proxy = %v, %v; want 127.0.0.1:4444", u, err)
	}
	// 普通域名走平台出口。
	u, err = tr.Proxy(mustReq(t, "http://example.com/"))
	if err != nil || u == nil || u.Host != "127.0.0.1:8080" {
		t.Fatalf("normal Proxy = %v, %v; want 127.0.0.1:8080", u, err)
	}
}

func TestConfigureTransportSocksPlatform(t *testing.T) {
	tr := &http.Transport{}
	if err := ConfigureTransport(tr, "socks5h://127.0.0.1:1080", HiddenProxies{}); err != nil {
		t.Fatalf("ConfigureTransport: %v", err)
	}
	// 平台出口是 SOCKS：普通域名也不能交给 Proxy（net/http 不认 socks）。
	if u, err := tr.Proxy(mustReq(t, "http://example.com/")); err != nil || u != nil {
		t.Fatalf("socks platform Proxy = %v, %v; want nil,nil", u, err)
	}
	if tr.DialContext == nil {
		t.Fatal("socks platform must set DialContext")
	}
}

func TestConfigureTransportRejectsBadProxy(t *testing.T) {
	tr := &http.Transport{}
	if err := ConfigureTransport(tr, "", HiddenProxies{Tor: "ftp://127.0.0.1:9050"}); err == nil {
		t.Fatal("unsupported tor scheme must error")
	}
}

func TestSocksDialContext(t *testing.T) {
	d, err := SocksDialContext("socks5h://127.0.0.1:9050")
	if err != nil || d == nil {
		t.Fatalf("SocksDialContext(socks5h) returned nil dialer or error: %v", err)
	}
	if _, err := SocksDialContext("http://127.0.0.1:8080"); err == nil {
		t.Fatal("non-socks proxy must error")
	}
}
