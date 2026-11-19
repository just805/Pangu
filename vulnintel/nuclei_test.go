package vulnintel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CountTemplates 是模板树自愈的判定基准：clone/pull 失败后「目录里一个 yaml
// 都没有」= 损坏残留，清理重 clone；有模板 = 只网络抖动，保留目录下次 pull。
func TestCountTemplates(t *testing.T) {
	dir := t.TempDir()
	if n := CountTemplates(dir); n != 0 {
		t.Fatalf("empty dir: got %d, want 0", n)
	}
	// 造一个 nuclei-templates 形状的树：子目录 + .yaml，外加一个不该数的 .yml/.txt
	y := filepath.Join(dir, "http", "cves", "2026")
	if err := os.MkdirAll(y, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(y, "a.yaml"), []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(y, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.yaml"), []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nope.yml"), []byte("id: y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := CountTemplates(dir); n != 2 {
		t.Fatalf("got %d, want 2（.yml 与非 yaml 不计）", n)
	}
}

// TestNormalizeNucleiProxy：空/裸 host:port/带 scheme/缺端口/含逗号的全部口径。
func TestNormalizeNucleiProxy(t *testing.T) {
	ok := map[string]string{
		"":                       "", // 空 = 不发射 -proxy
		"127.0.0.1:8788":         "http://127.0.0.1:8788",
		"  127.0.0.1:8788 ":      "http://127.0.0.1:8788",
		"http://127.0.0.1:8788":  "http://127.0.0.1:8788",
		"socks5://10.0.0.1:1080": "socks5://10.0.0.1:1080",
	}
	for in, want := range ok {
		got, err := NormalizeNucleiProxy(in)
		if err != nil || got != want {
			t.Errorf("NormalizeNucleiProxy(%q)=%q,%v want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"10.0.0.1", "http://10.0.0.1", "http://a:1,http://b:2", "://"} {
		if _, err := NormalizeNucleiProxy(bad); err == nil {
			t.Errorf("NormalizeNucleiProxy(%q) 应报错", bad)
		}
	}
}

// TestBuildNucleiCmdProxyAndTimeout：-proxy 以两个独立 argv 元素追加；timeout
// 钳制与 EffectiveTimeout 一致。
func TestBuildNucleiCmdProxyAndTimeout(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	build := func(proxy string) []string {
		cmd, cancel, err := BuildNucleiCmd(context.Background(), dir, ScanArgs{
			Target: "https://t.example.com", Proxy: proxy, TimeoutTotal: 45 * time.Minute,
		})
		if err != nil {
			t.Fatalf("BuildNucleiCmd(proxy=%q): %v", proxy, err)
		}
		cancel()
		return cmd.Args
	}
	args := build("127.0.0.1:8788")
	if got := strings.Join(args, " "); !strings.Contains(got, "-proxy http://127.0.0.1:8788") {
		t.Fatalf("missing -proxy argv pair: %v", args)
	}
	// scheme 原样透传。
	args = build("socks5://10.0.0.1:1080")
	if got := strings.Join(args, " "); !strings.Contains(got, "-proxy socks5://10.0.0.1:1080") {
		t.Fatalf("scheme passthrough failed: %v", args)
	}
	// 空代理不发射。
	for _, a := range build("") {
		if a == "-proxy" {
			t.Fatalf("empty proxy must not emit -proxy: %v", args)
		}
	}
	// 含逗号直接报错。
	if _, _, err := BuildNucleiCmd(context.Background(), dir, ScanArgs{
		Target: "https://t.example.com", Proxy: "http://a:1,http://b:2",
	}); err == nil {
		t.Fatal("comma proxy must fail")
	}
	// EffectiveTimeout 口径：0→10m，>30m→30m。
	if EffectiveTimeout(0) != 10*time.Minute || EffectiveTimeout(45*time.Minute) != 30*time.Minute || EffectiveTimeout(5*time.Minute) != 5*time.Minute {
		t.Fatal("EffectiveTimeout clamp wrong")
	}
}
