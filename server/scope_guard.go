package server

// N2 scope-aware 拦截：工具调用里出现的 URL 主机若不在任务授权范围
// （company_scope 的 domain/ip/cidr，按任务 CompanyIDs 取），一律 deny——
// 用户规则里的 allow 也不能放行（守卫优先于规则）。任务没配 scope 时不拦
// （保守：没有白名单就无法判定，不误伤既有部署）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/just805/pangu/intercept"
)

// scopeGuardEnabledKey 设置键（默认开启：范围外一律 deny 是本批设计目标）。
const scopeGuardEnabledKey = "scope_guard_enabled"

var urlInText = regexp.MustCompile(`https?://[^\s"'<>\\]+`)

// wireScopeGuard 安装范围守卫到全局 interceptor（热更新，读 settings 即时生效）。
func (s *Server) wireScopeGuard() {
	s.m.interceptor.SetScopeChecker(func(ctx context.Context, toolName string, input []byte) (bool, string) {
		if !s.m.pg.GetBool(scopeGuardEnabledKey, true) {
			return false, ""
		}
		taskID := intercept.TaskIDFromContext(ctx)
		if taskID == "" {
			return false, ""
		}
		// 从 input 里提取 URL 主机（payload 是 JSON，取所有字符串值里的 http(s) URL）。
		hosts := hostsFromToolInput(input)
		if len(hosts) == 0 {
			return false, "" // 无网络目标（文件/本地命令）不判定
		}
		t := s.m.ResolveTask(taskID)
		if t == nil || len(t.CompanyIDs) == 0 {
			return false, ""
		}
		sc, err := scopeForCompanies(s, t.CompanyIDs)
		if err != nil || !sc.placed {
			return false, "" // 没配 scope：无白名单可判，保守放行
		}
		for _, h := range hosts {
			if !hostInScope(h, sc.domains, sc.nets) {
				return true, fmt.Sprintf("%s（工具 %s）", h, toolName)
			}
		}
		return false, ""
	})
}

// taskScope 是任务允许的目标集合。不缓存到 Server 字段——多个任务并发执行
// 时各自的 company scope 不同，全局字段会串；scope 配置很少，每次查即可。
type taskScope struct {
	domains []string
	nets    []*net.IPNet
	placed  bool
}

// hostsFromToolInput 从工具入参 JSON 里提取 http(s) URL 的 host（去重）。
func hostsFromToolInput(input []byte) []string {
	var generic any
	if err := json.Unmarshal(input, &generic); err != nil {
		// 非 JSON（如 Bash 命令文本）：也扫一遍 URL。
		return hostsFromText(string(input))
	}
	seen := map[string]bool{}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			for _, h := range hostsFromText(x) {
				if !seen[h] {
					seen[h] = true
					out = append(out, h)
				}
			}
		case map[string]any:
			for _, val := range x {
				walk(val)
			}
		case []any:
			for _, val := range x {
				walk(val)
			}
		}
	}
	walk(generic)
	return out
}

func hostsFromText(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range urlInText.FindAllString(text, -1) {
		u, err := url.Parse(m)
		if err != nil || u.Host == "" {
			continue
		}
		h := strings.ToLower(u.Hostname())
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// hostInScope host 为域名时匹配 company_scope.domain（精确或子域），为 IP 时
// 匹配 ip/cidr 网段。
func hostInScope(host string, domains []string, nets []*net.IPNet) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	ip := net.ParseIP(host)
	if ip != nil {
		for _, n := range nets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// scopeForCompanies 从 company_scope 查授权域名与网段。placed=false 表示
// 这些公司一条 scope 都没配（无白名单 → 守卫放行）。
func scopeForCompanies(s *Server, companyIDs []int64) (taskScope, error) {
	out := taskScope{}
	args := make([]any, 0, len(companyIDs))
	for _, id := range companyIDs {
		args = append(args, id)
	}
	rows, err := s.m.pg.Query(`SELECT kind, COALESCE(domain,''), COALESCE(net::text,'')
FROM company_scope WHERE company_id = ANY($1::bigint[]) AND kind IN ('domain','ip','cidr')`, args)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, domain, netText string
		if err := rows.Scan(&kind, &domain, &netText); err != nil {
			return out, err
		}
		out.placed = true
		switch kind {
		case "domain":
			if domain != "" {
				out.domains = append(out.domains, domain)
			}
		case "ip", "cidr":
			if netText == "" {
				continue
			}
			_, n, err := net.ParseCIDR(netText)
			if err == nil {
				out.nets = append(out.nets, n)
			}
		}
	}
	return out, rows.Err()
}
