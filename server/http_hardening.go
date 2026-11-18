package server

import (
	"net/http"
	"strings"
	"time"
)

// 全局请求读限时（公网部署加固）：兜住 body 阶段的 slowloris。
//
// 此前 http.Server 只设了 ReadHeaderTimeout（10s），header 读完之后的慢读
// （缓慢滴水 body）可以无限占住连接与内存——未认证攻击者靠打登录/init 等
// POST 端点就能耗尽服务。本中间件给每个 /api 请求设置从「请求开始」到「body
// 读完」的墙钟上限。
//
// 对 SSE 的影响：SSE 端点都是 GET、无 body，读阶段瞬间结束，响应阶段的写不受
// 读限时影响——长流安全。对上传的影响见 apiLongBodyPrefixes。
const (
	apiReadDeadline     = 5 * time.Minute
	apiLongReadDeadline = 30 * time.Minute
)

// apiLongBodyPrefixes 自带 MaxBytesReader 的大文件上传端点：全局 5 分钟对慢速
// 链路上的 512MB workspace 上传可能不够，给它们单独放宽到 30 分钟（仍是有限
// 的，不是豁免——慢滴攻击者最多占用连接 30 分钟而非无限）。
var apiLongBodyPrefixes = []string{
	"/api/workspace/upload",
	"/api/chat/upload",
}

// withAPIReadDeadline 给 /api 请求设置整体读限时。放在 requireAuth 外层：限时
// 与鉴权无关，未鉴权端点（auth/*、health）同样要被兜住。
func withAPIReadDeadline(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline := apiReadDeadline
		for _, p := range apiLongBodyPrefixes {
			if strings.HasPrefix(r.URL.Path, p) {
				deadline = apiLongReadDeadline
				break
			}
		}
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(deadline))
		h.ServeHTTP(w, r)
	})
}
