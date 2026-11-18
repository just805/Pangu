// Package netguard 提供出站拨号守卫，供「目标地址可由已登录会话配置」的服务器端
// HTTP 客户端（LLM 连通性测试、MCP HTTP 客户端等）复用。
//
// 背景：这些客户端的目标 URL 由管理员/已登录会话填写，而连通性测试类请求会把
// 错误信息或响应体回显给调用者——这是一条半盲读原语。公网/云部署下最敏感的目标
// 是链路本地地址段（云元数据 169.254.169.254，可读出实例 IAM 凭据）。
//
// 取舍与 notify/http.go 的 isBlockedDialIP 一致但更宽松：只拒绝链路本地与组播，
// 环回与 RFC1918 私网**放行**——本机 vLLM/ollama、内网推理网关、本机 MCP server
// 是常见合法配置，把它们挡掉会让功能在真实环境里直接不可用。
package netguard

import (
	"fmt"
	"net"
	"syscall"
)

// BlockLinkLocalDial 是 http.Transport 的 Control 钩子，在连接建立时检查目标
// 地址：拒绝链路本地（IPv4 169.254.0.0/16 含云元数据、IPv6 fe80::/10）、接口
// 本地组播、组播与未指定地址。
//
// 为什么设在拨号阶段而不是只在保存配置时校验 URL 字符串：这里才是最终生效点，
// 同时覆盖两类绕过配置校验的情形——DNS 重绑定（校验时解析到公网 IP、真正连接
// 时解析到元数据地址）与重定向。
func BlockLinkLocalDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("无法解析目标地址 %q", host)
	}
	// IPv4-mapped IPv6（::ffff:169.254.169.254）要还原成 IPv4 再判，否则绕过检查。
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("拒绝连接到链路本地/组播地址 %s（含云元数据端点）", ip)
	}
	return nil
}
