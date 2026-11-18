package netguard

import "testing"

func TestBlockLinkLocalDial(t *testing.T) {
	blocked := []string{
		"169.254.169.254:80",          // AWS/GCP/Azure 云元数据
		"169.254.170.2:80",            // ECS task metadata
		"[::ffff:169.254.169.254]:80", // IPv4-mapped 绕过
		"[fe80::1]:8080",              // IPv6 链路本地
		"224.0.0.1:80",                // 组播
		"0.0.0.0:80",                  // 未指定
	}
	allowed := []string{
		"127.0.0.1:11434",       // 本机 ollama
		"10.0.0.5:8000",         // 内网推理网关（RFC1918 放行是刻意取舍）
		"192.168.1.10:8787",
		"[::1]:8000",            // IPv6 环回
		"93.184.216.34:443",     // 公网 IP（hostname 在真实链路中由 Transport 先解析成 IP 再进 Control）
	}

	for _, addr := range blocked {
		if err := BlockLinkLocalDial("tcp", addr, nil); err == nil {
			t.Errorf("BlockLinkLocalDial(%q) = nil, want error", addr)
		}
	}
	for _, addr := range allowed {
		if err := BlockLinkLocalDial("tcp", addr, nil); err != nil {
			t.Errorf("BlockLinkLocalDial(%q) = %v, want nil", addr, err)
		}
	}
	// host 形态（未解析成 IP）应报错而不是放行——Transport 传入 Control 前一定
	// 已解析，此分支是纵深防御。
	if err := BlockLinkLocalDial("tcp", "example.metadata:80", nil); err == nil {
		t.Error("unparseable host should error, got nil")
	}
}
