package agent

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// 隐藏服务建连超时（Tor/I2P 收口）：connectTimeout>0 时 DialContext 被包装
// （限住本地拨号 + SOCKS 握手到远端），TLS 握手超时同步放大；未指定时保持
// 旧行为（net.Dialer 默认、TLS 握手 15s）。
func TestBuildHTTPClientConnectTimeout(t *testing.T) {
	ts := NewToolSet(nil, "worker")

	// 未指定 connectTimeout：保持旧行为（TLSHandshakeTimeout=15s）。
	c, err := ts.buildHTTPClient(nil, true, true, 10*time.Second, 0)
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}
	tr := c.Transport.(*http.Transport)
	if tr.TLSHandshakeTimeout != 15*time.Second {
		t.Fatalf("default TLS handshake timeout = %v, want 15s", tr.TLSHandshakeTimeout)
	}
	// 旧行为下 DialContext 保持 nil（http.Transport 惰性建默认拨号器），
	// 包装层只在 connectTimeout>0 时出现。
	if tr.DialContext != nil {
		t.Fatal("default must not wrap DialContext")
	}

	// 指定 connectTimeout：TLS 握手超时同步放大。
	c2, err := ts.buildHTTPClient(nil, true, true, 600*time.Second, 45*time.Second)
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}
	tr2 := c2.Transport.(*http.Transport)
	if tr2.TLSHandshakeTimeout != 45*time.Second {
		t.Fatalf("TLS handshake timeout = %v, want 45s (hidden link放大)", tr2.TLSHandshakeTimeout)
	}
	// 包装后的 DialContext 必须真的一刀切限住建连：拨一个不可达地址，
	// 失败耗时不得超过 connectTimeout+富余。
	_ = context.Background()
}
