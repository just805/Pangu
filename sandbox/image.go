package sandbox

// 指纹（组件名 + 版本）→ 官方镜像。
//
// **映射不到就明确失败，不猜**。这是本模块最容易做错的地方：随便起个 ubuntu
// 跑一下，PoC 当然不会触发，然后模型会得出「这个 PoC 不可复现」的结论——
// 比不做验证更糟，因为它给了一个错误的负结论。

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// techSpec 描述一种可映射的组件。
type techSpec struct {
	// image 是镜像仓库名（不含 tag）。
	image string
	// tagFor 由版本号生成 tag；返回空表示该版本无法对应到官方 tag。
	// hasMinor 区分「只给了主版本」与「次版本恰好是 0」（8 vs 8.0 是两个 tag）。
	tagFor func(major int, hasMinor bool, minor int) string
}

// techs 覆盖设计稿列的四类 + mysql。加新组件时要确认官方镜像确实提供对应 tag——
// 写错 tag 会让 docker pull 失败，表现为「沙箱不可用」，比映射不到更难查。
var techs = map[string]techSpec{
	"nginx": {image: "nginx", tagFor: func(major int, hasMinor bool, minor int) string {
		if major <= 0 {
			return ""
		}
		if !hasMinor {
			return fmt.Sprintf("%d-alpine", major)
		}
		return fmt.Sprintf("%d.%d-alpine", major, minor)
	}},
	// apache 的官方镜像是 httpd；httpd 只提供主.次版本 tag。
	"apache": {image: "httpd", tagFor: apacheTag},
	"httpd":  {image: "httpd", tagFor: apacheTag},
	"php": {image: "php", tagFor: func(major int, hasMinor bool, minor int) string {
		if major <= 0 {
			return ""
		}
		// 必须用 apache 变体才带 web 服务；-cli / -fpm 起不来 HTTP。
		if !hasMinor {
			return fmt.Sprintf("%d-apache", major)
		}
		return fmt.Sprintf("%d.%d-apache", major, minor)
	}},
	"redis": {image: "redis", tagFor: func(major int, hasMinor bool, minor int) string {
		if major <= 0 {
			return ""
		}
		// redis 的次版本 tag 不稳定，只用主版本。
		return fmt.Sprintf("%d-alpine", major)
	}},
	// mysql 刻意不在此表：它要初始化数据目录才能起服务，耗时远超单次 PoC 验证的
	// 合理范围。映射不到就明确失败，比给一个「起不来」的环境诚实。
}

// apacheTag 生成 httpd 的 tag（官方只保留主.次版本，如 2.4）。
func apacheTag(major int, hasMinor bool, minor int) string {
	if major <= 0 {
		return ""
	}
	if !hasMinor {
		return fmt.Sprintf("%d", major)
	}
	return fmt.Sprintf("%d.%d", major, minor)
}

var versionRe = regexp.MustCompile(`(\d+)(?:\.(\d+))?`)

// ImageFor 把组件名与版本映射到官方镜像 tag。
//
// 例：nginx + "1.25.3" → "nginx:1.25-alpine"；php + "8.1" → "php:8.1-apache"。
func ImageFor(tech, version string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(tech))
	if key == "" {
		return "", fmt.Errorf("tech 不能为空（如 nginx / apache / php / redis / mysql）")
	}
	spec, ok := techs[key]
	if !ok {
		return "", fmt.Errorf("不支持在沙箱里仿制 %q：当前支持 nginx / apache(httpd) / php / redis"+
			"（mysql 需要初始化数据目录，暂不支持）。"+
			"**不要猜一个近似镜像去跑**——仿制环境不对，验证结论没有意义", tech)
	}
	m := versionRe.FindStringSubmatch(strings.TrimSpace(version))
	if m == nil {
		return "", fmt.Errorf("版本 %q 里读不出主版本号（需要形如 1.25.3 / 8.1 / 2.4）", version)
	}
	major, _ := strconv.Atoi(m[1])
	hasMinor := m[2] != ""
	minor := 0
	if hasMinor {
		minor, _ = strconv.Atoi(m[2])
	}
	tag := spec.tagFor(major, hasMinor, minor)
	if tag == "" {
		return "", fmt.Errorf("版本 %q 无法对应到 %s 的官方 tag", version, spec.image)
	}
	image := spec.image + ":" + tag
	if !ImageAllowed(image) {
		return "", fmt.Errorf("生成的镜像 %q 不在白名单内", image)
	}
	return image, nil
}

// bootstrapFor 返回「在容器内把服务拉起来」的命令。
//
// 为什么需要它：Run 用 `--entrypoint sh` 覆盖了镜像自带的启动命令（nginx 镜像
// 原本 ENTRYPOINT 就是起 nginx），所以容器里默认没有任何服务在监听——直接跑
// PoC 只会得到 Connection refused，看起来像「PoC 不成立」。
//
// 实测（2026-11-27）：不 bootstrap 时 nginx:alpine 内 wget 127.0.0.1 直接
// Connection refused。这条不处理，整个特征就是负结论制造机。
func bootstrapFor(image string) string {
	switch {
	case strings.HasPrefix(image, "nginx:"):
		return "nginx" // 默认 daemonize on，起完即返回
	case strings.HasPrefix(image, "httpd:"):
		return "httpd -k start"
	case strings.HasPrefix(image, "php:"):
		return "apache2ctl start"
	case strings.HasPrefix(image, "redis:"):
		return "redis-server --daemonize yes"
	}
	return ""
}

// BootstrapSupported 报告该镜像是否支持自动起服务。
func BootstrapSupported(image string) bool { return bootstrapFor(image) != "" }
