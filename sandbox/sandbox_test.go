package sandbox

import (
	"context"
	"strings"
	"testing"
)

// ── 镜像映射 ────────────────────────────────────────────────

func TestImageFor(t *testing.T) {
	cases := []struct{ tech, version, want string }{
		{"nginx", "1.25.3", "nginx:1.25-alpine"},
		{"nginx", "1", "nginx:1-alpine"},
		{"NGINX", " 1.24 ", "nginx:1.24-alpine"},
		{"apache", "2.4.58", "httpd:2.4"},
		{"httpd", "2.4", "httpd:2.4"},
		{"php", "8.1.2", "php:8.1-apache"},
		{"redis", "7.2.4", "redis:7-alpine"},
	}
	for _, c := range cases {
		got, err := ImageFor(c.tech, c.version)
		if err != nil {
			t.Errorf("ImageFor(%q,%q) 报错: %v", c.tech, c.version, err)
			continue
		}
		if got != c.want {
			t.Errorf("ImageFor(%q,%q)=%q，期望 %q", c.tech, c.version, got, c.want)
		}
	}
}

// 映射不到必须明确失败——随便起个近似镜像跑，会给出错误的「PoC 不可复现」结论。
func TestImageForRejectsUnknown(t *testing.T) {
	for _, tc := range []struct{ tech, ver string }{
		{"weblogic", "12.2"}, // 不在支持列表
		{"nginx", ""},        // 版本读不出
		{"nginx", "latest"},  // 非数字版本
		{"", "1.0"},          // 组件为空
		{"mysql", "8.0.35"},  // 需初始化数据目录，刻意不支持
	} {
		if _, err := ImageFor(tc.tech, tc.ver); err == nil {
			t.Errorf("ImageFor(%q,%q) 应报错", tc.tech, tc.ver)
		}
	}
}

// ── 镜像白名单 ──────────────────────────────────────────────

func TestImageAllowed(t *testing.T) {
	ok := []string{"nginx:1.25-alpine", "httpd:2.4", "php:8.1-apache", "redis:7-alpine", "mysql:8.0"}
	for _, img := range ok {
		if !ImageAllowed(img) {
			t.Errorf("%q 应在白名单内", img)
		}
	}
	bad := []string{
		"nginx:",                  // 空 tag
		"nginx",                   // 无 tag
		"registry.evil.io/nginx:1", // 私有仓库（含 /）
		"ubuntu:22.04",            // 非白名单组件
		"",                        // 空
	}
	for _, img := range bad {
		if ImageAllowed(img) {
			t.Errorf("%q 不应通过白名单", img)
		}
	}
}

// ── 真实 docker（不可用时 skip）──────────────────────────────

func TestRunRejectsBadImageBeforeDocker(t *testing.T) {
	r := &Runtime{docker: "/bin/true"} // 假装可用，验证参数校验发生在 docker 之前
	_, err := r.Run(context.Background(), "evil.io/backdoor:1", "true")
	if err == nil || !strings.Contains(err.Error(), "白名单") {
		t.Errorf("非白名单镜像应被挡下，实际 err=%v", err)
	}
	if _, err := r.Run(context.Background(), "nginx:alpine", "  "); err == nil {
		t.Error("空命令应报错")
	}
}

// 沙箱的核心行为：容器内能访问自己的服务，但出不了网。
func TestRunNginxServesLocallyButNotInternet(t *testing.T) {
	r := New()
	if !r.Available() {
		t.Skip("本机没有 docker，跳过真实沙箱测试")
	}
	ctx := context.Background()

	// 1) 容器内 127.0.0.1 可达（这是 PoC 预验证的前提）。
	res, err := r.Run(ctx, "nginx:alpine", "wget -qO- --timeout=5 http://127.0.0.1/ 2>&1 | head -5")
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if !strings.Contains(strings.ToLower(res.Output), "welcome to nginx") {
		t.Errorf("容器内应能访问本地 nginx，实际输出: %q", res.Output)
	}

	// 2) 出网必须失败（--network none）。这条不成立就等于开了个跳板。
	res2, _ := r.Run(ctx, "nginx:alpine", "wget -qO- --timeout=5 http://1.1.1.1/ 2>&1 | head -3")
	if strings.Contains(res2.Output, "1.1.1.1") && !strings.Contains(strings.ToLower(res2.Output), "bad address") &&
		!strings.Contains(strings.ToLower(res2.Output), "network") && !strings.Contains(strings.ToLower(res2.Output), "timed out") {
		t.Errorf("容器不应能访问外网，实际输出: %q", res2.Output)
	}
}
