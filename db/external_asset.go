package db

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// ═══ M-08：外部测绘数据源归一器 ═══
//
// 现状：FOFA 走 MCP（结果只是工具输出文本）、ScopeSentry 走同步页，各自接入、
// 各自落库，没有统一的资产口径。本文件提供**纯函数归一器**：把各源的一条原始
// 记录转成平台的资产口径（domain/root_domain/ip/port/type/url/technologies）。
//
// 边界（重要）：这里只做「字段映射 + 类型判定」，**不做网络请求、不写库**。
// 各源的真实接入需要 API 凭据（Hunter/Quake/Shodan/ZoomEye），本机无法验证，
// 因此先交付归一器与单测；接线时各源只需把响应逐条喂进来。

// NormalizedAsset 是归一后的资产。
type NormalizedAsset struct {
	Type         string   `json:"type"` // root_domain|subdomain|ip|service
	Domain       string   `json:"domain,omitempty"`
	RootDomain   string   `json:"root_domain,omitempty"`
	IP           string   `json:"ip,omitempty"`
	Port         int      `json:"port,omitempty"`
	URL          string   `json:"url,omitempty"`
	AppName      string   `json:"app_name,omitempty"`
	Technologies []string `json:"technologies,omitempty"`
	Source       string   `json:"source"`
	SourceID     string   `json:"source_id,omitempty"`
}

// 各源的字段别名。先按通用别名取，取不到再走源特有路径。
var sourceFieldAliases = map[string]map[string][]string{
	"fofa": {
		"host": {"host"}, "ip": {"ip"}, "port": {"port"},
		"protocol": {"protocol"}, "title": {"title"}, "tech": {"server", "product"},
	},
	"hunter": {
		"host": {"domain", "url"}, "ip": {"ip"}, "port": {"port"},
		"protocol": {"protocol"}, "title": {"web_title"}, "tech": {"component", "os"},
	},
	"quake": {
		"host": {"domain"}, "ip": {"ip"}, "port": {"port"},
		"protocol": {"service.http.protocol", "transport"}, "title": {"service.http.title"},
		"tech": {"service.http.server"},
	},
	"shodan": {
		"host": {"hostnames", "domains"}, "ip": {"ip_str", "ip"}, "port": {"port"},
		"protocol": {"transport"}, "title": {"http.title"}, "tech": {"http.server", "product"},
	},
	"zoomeye": {
		"host": {"hostname", "site"}, "ip": {"ip"}, "port": {"port"},
		"protocol": {"protocol"}, "title": {"title"}, "tech": {"app"},
	},
}

// NormalizeExternalAsset 把某源的一条原始记录归一成平台资产口径。
// 未知 source 也走通用别名（host/domain/ip/port/...），尽量不丢数据。
func NormalizeExternalAsset(source string, raw map[string]any) (NormalizedAsset, error) {
	src := strings.ToLower(strings.TrimSpace(source))
	aliases := sourceFieldAliases[src]
	if aliases == nil {
		aliases = map[string][]string{
			"host": {"host", "domain", "hostname", "hostnames", "site"},
			"ip": {"ip", "ip_str"},
			"port": {"port"},
			"protocol": {"protocol", "transport", "scheme"},
			"title": {"title", "web_title", "http.title"},
			"tech": {"server", "component", "app", "http.server", "product"},
		}
	}
	out := NormalizedAsset{Source: src}
	if s := pickString(raw, aliases["source_id"]); s != "" {
		out.SourceID = s
	}

	host := normalizeHost(pickString(raw, aliases["host"]))
	ip := strings.TrimSpace(pickString(raw, aliases["ip"]))
	port := pickInt(raw, aliases["port"])
	proto := strings.ToLower(strings.TrimSpace(pickString(raw, aliases["protocol"])))

	// host 里可能带端口（fofa 的 host 常是 "example.com:8080"）。
	if h, p, ok := splitHostPortLoose(host); ok {
		host = h
		if port == 0 {
			port = p
		}
	}

	out.IP = ip
	out.Port = port
	if host != "" && !isIPLiteral(host) {
		out.Domain = host
		if root, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
			out.RootDomain = root
		}
	}

	// 技术栈：标题/服务名/组件都算「指纹线索」，去重保序。
	for _, k := range aliases["tech"] {
		if v := pickString(raw, []string{k}); v != "" {
			out.Technologies = appendUnique(out.Technologies, v)
		}
	}
	if t := pickString(raw, aliases["title"]); t != "" {
		out.Technologies = appendUnique(out.Technologies, "title:"+t)
	}

	// URL：能看出是 web 且端口明确时拼出来，便于直接探测。
	if host != "" && (proto == "http" || proto == "https" || proto == "http/https") {
		scheme := proto
		if scheme == "http/https" {
			scheme = "http"
		}
		hp := host
		if port > 0 && !((scheme == "http" && port == 80) || (scheme == "https" && port == 443)) {
			hp = fmt.Sprintf("%s:%d", host, port)
		}
		out.URL = scheme + "://" + hp
	}

	// 类型判定（与 assets.type 的 6 类口径对齐，但归一器只产 4 类——
	// app/endpoint 需要更深的上下文，不在测绘源里判断）。
	switch {
	case out.Domain != "":
		if out.RootDomain != "" && strings.EqualFold(out.Domain, out.RootDomain) {
			out.Type = "root_domain"
		} else {
			out.Type = "subdomain"
		}
		if port > 0 {
			// 带端口的域名记录更像一条服务；但保留域名以免丢失归属信息。
			out.Type = "service"
		}
	case ip != "":
		if port > 0 {
			out.Type = "service"
		} else {
			out.Type = "ip"
		}
	}
	if out.Type == "" {
		return out, fmt.Errorf("记录里没有可识别的 host/ip：%v", raw)
	}
	return out, nil
}

// pickString 按别名顺序取第一个非空字符串；支持 []string（shodan 的 hostnames）。
func pickString(raw map[string]any, keys []string) string {
	for _, k := range keys {
		v, ok := lookupPath(raw, k)
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				return s
			}
		case []any:
			for _, e := range t {
				if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		case []string:
			for _, s := range t {
				if strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64)
		case int:
			return strconv.Itoa(t)
		}
	}
	return ""
}

func pickInt(raw map[string]any, keys []string) int {
	for _, k := range keys {
		v, ok := lookupPath(raw, k)
		if !ok {
			continue
		}
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				return n
			}
		}
	}
	return 0
}

// lookupPath 支持 "a.b.c" 形式的嵌套取值。
func lookupPath(raw map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = raw
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func normalizeHost(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "http://")
	h = strings.TrimPrefix(h, "https://")
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	return strings.TrimSuffix(h, ".")
}

func splitHostPortLoose(s string) (string, int, bool) {
	i := strings.LastIndex(s, ":")
	if i <= 0 || i == len(s)-1 {
		return s, 0, false
	}
	p, err := strconv.Atoi(s[i+1:])
	if err != nil || p <= 0 || p > 65535 {
		return s, 0, false
	}
	return s[:i], p, true
}

// 复用 finding_assets.go 的 isIPLiteral（IPv6 也覆盖）。

func appendUnique(list []string, v string) []string {
	v = strings.TrimSpace(v)
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return list
		}
	}
	return append(list, v)
}
