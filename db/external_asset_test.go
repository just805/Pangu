package db

import "testing"

func TestNormalizeExternalAssetFOFA(t *testing.T) {
	// fofa 的 host 常带端口，另给 protocol。
	a, err := NormalizeExternalAsset("fofa", map[string]any{
		"host": "api.example.com:8443", "ip": "203.0.113.10", "port": "8443",
		"protocol": "https", "title": "登录", "server": "nginx",
	})
	if err != nil {
		t.Fatalf("归一失败: %v", err)
	}
	if a.Domain != "api.example.com" || a.RootDomain != "example.com" {
		t.Errorf("域名/根域不符: %+v", a)
	}
	if a.Port != 8443 || a.IP != "203.0.113.10" {
		t.Errorf("端口/IP 不符: %+v", a)
	}
	if a.URL != "https://api.example.com:8443" {
		t.Errorf("URL 不符: %q", a.URL)
	}
	if a.Type != "service" {
		t.Errorf("带端口的域名应为 service，实际 %q", a.Type)
	}
	if len(a.Technologies) == 0 {
		t.Error("应提取技术栈线索")
	}
}

func TestNormalizeExternalAssetShodanNested(t *testing.T) {
	// shodan 的标题与 server 在嵌套对象里，hostnames 是数组。
	a, err := NormalizeExternalAsset("shodan", map[string]any{
		"ip_str": "198.51.100.7", "port": float64(443), "transport": "https",
		"hostnames": []any{"www.example.org"},
		"http":      map[string]any{"title": "Example", "server": "Apache"},
	})
	if err != nil {
		t.Fatalf("归一失败: %v", err)
	}
	if a.Domain != "www.example.org" || a.RootDomain != "example.org" {
		t.Errorf("域名不符: %+v", a)
	}
	if a.URL != "https://www.example.org" {
		t.Errorf("443 端口不应拼端口号: %q", a.URL)
	}
	if a.Type != "service" {
		t.Errorf("应为 service，实际 %q", a.Type)
	}
}

func TestNormalizeExternalAssetRootDomain(t *testing.T) {
	a, err := NormalizeExternalAsset("hunter", map[string]any{
		"domain": "example.com", "ip": "203.0.113.20", "web_title": "首页",
	})
	if err != nil {
		t.Fatalf("归一失败: %v", err)
	}
	if a.Type != "root_domain" {
		t.Errorf("根域应为 root_domain，实际 %q", a.Type)
	}
	if a.URL != "" {
		t.Errorf("无 protocol 时不应拼 URL: %q", a.URL)
	}
}

func TestNormalizeExternalAssetIPOnly(t *testing.T) {
	a, err := NormalizeExternalAsset("quake", map[string]any{"ip": "10.0.0.9", "port": float64(6379)})
	if err != nil {
		t.Fatalf("归一失败: %v", err)
	}
	if a.Type != "service" || a.Port != 6379 || a.Domain != "" {
		t.Errorf("纯 IP + 端口应为 service: %+v", a)
	}
	// 无端口 → ip
	b, err := NormalizeExternalAsset("quake", map[string]any{"ip": "10.0.0.9"})
	if err != nil {
		t.Fatalf("归一失败: %v", err)
	}
	if b.Type != "ip" {
		t.Errorf("纯 IP 应为 ip，实际 %q", b.Type)
	}
}

func TestNormalizeExternalAssetErrors(t *testing.T) {
	if _, err := NormalizeExternalAsset("fofa", map[string]any{"title": "只有标题"}); err == nil {
		t.Fatal("没有 host/ip 应报错")
	}
	// 未知源走通用别名，仍能归一。
	a, err := NormalizeExternalAsset("unknown-src", map[string]any{"hostname": "x.example.net"})
	if err != nil {
		t.Fatalf("通用别名应可用: %v", err)
	}
	if a.Domain != "x.example.net" || a.Source != "unknown-src" {
		t.Errorf("通用归一不符: %+v", a)
	}
}
