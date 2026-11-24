package traffic

import (
	"net/http"
	"testing"
)

func TestHiddenUpstreamPrecedence(t *testing.T) {
	tr := &Traffic{}
	if err := tr.SetHiddenServiceUpstream("socks5h://127.0.0.1:9050", "http://127.0.0.1:4444"); err != nil {
		t.Fatalf("SetHiddenServiceUpstream: %v", err)
	}
	gu, err := ValidateProxyURL("http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	tr.upstream.Store(gu)

	cases := []struct{ raw, wantHost string }{
		{"http://abc.onion/", "127.0.0.1:9050"},
		{"http://abc.i2p/", "127.0.0.1:4444"},
		{"http://example.com/", "127.0.0.1:8080"},
	}
	for _, c := range cases {
		req, _ := http.NewRequest(http.MethodGet, c.raw, nil)
		u, err := tr.pickUpstream(req)
		if err != nil || u == nil || u.Host != c.wantHost {
			t.Errorf("pickUpstream(%s) = %v, %v; want host %s", c.raw, u, err, c.wantHost)
		}
	}

	// 清空 Tor 后，.onion 回落到全局出口（用户自己承担走不通的风险）。
	if err := tr.SetHiddenServiceUpstream("", "http://127.0.0.1:4444"); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://abc.onion/", nil)
	u, _ := tr.pickUpstream(req)
	if u == nil || u.Host != "127.0.0.1:8080" {
		t.Fatalf("cleared tor should fall back to global, got %v", u)
	}
}

func TestHiddenUpstreamInvalid(t *testing.T) {
	tr := &Traffic{}
	if err := tr.SetHiddenServiceUpstream("ftp://127.0.0.1:9050", ""); err == nil {
		t.Fatal("unsupported tor scheme must error")
	}
}

func TestValidateProxyURLSocks5hNormalized(t *testing.T) {
	u, err := ValidateProxyURL("socks5h://127.0.0.1:9050")
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "socks5" {
		t.Fatalf("socks5h scheme = %q, want socks5", u.Scheme)
	}
}
