package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/just805/pangu/db"
)

const (
	jwtKeyFilename = "jwt.key"
	authPassKey    = "auth.password_hash"
	jwtTTL         = 7 * 24 * time.Hour
	keyChars       = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	// 下限与 setup 页的前端校验一致——校验只放在前端等于没放，直接打 API 就能
	// 绕过。上限是 bcrypt 的硬限制：超过 72 字节 GenerateFromPassword 会返回
	// ErrPasswordTooLong，提前挡掉好过让用户收到一句含义不明的「密码加密失败」。
	minPasswordRunes = 8
	maxPasswordBytes = 72

	// initTokenEnv / initTokenHeader：公网部署的初始化抢注加固（可选开关）。
	// POST /api/auth/init 在密码未设置前是未鉴权可达的——服务一启动监听公网，
	// 任何先到的访客都能抢注管理员。设置 PANGU_INIT_TOKEN 后，init 请求必须带
	// 匹配的 X-Init-Token 头；token 由部署者生成并随环境变量注入，可以打印在
	// 控制台/写进部署产物里。未设置时行为完全不变（本机/内网部署不受影响）。
	initTokenEnv    = "PANGU_INIT_TOKEN"
	initTokenHeader = "X-Init-Token"
)

// errDataSourceUnavailable 是密码相关读操作失败时统一的回复。这些 handler 绝不能
// 把"读不到"当成"没有设置"：authInit 曾因此在数据库报错时放行，让未认证请求覆盖
// 掉已有的管理员密码。
const errDataSourceUnavailable = "数据源暂时不可用，请稍后重试"

// validatePassword 返回空串表示通过，否则返回可直接展示给用户的中文原因。
func validatePassword(pw string) string {
	if utf8.RuneCountInString(pw) < minPasswordRunes {
		return fmt.Sprintf("密码长度至少 %d 位", minPasswordRunes)
	}
	if len(pw) > maxPasswordBytes {
		return fmt.Sprintf("密码长度不能超过 %d 字节", maxPasswordBytes)
	}
	return ""
}

// loadOrCreateJWTKey reads the 32-byte signing key from keyDir/jwt.key. keyDir is
// the project base dir (next to the executable), NOT the browsable workspace root
// (dataDir) — the signing key must never be listable/downloadable via the file
// manager. Legacy installs kept it at dataDir/jwt.key; if present there and not yet
// at the new location, it is migrated (key preserved, so sessions stay valid) and
// the old file removed so it disappears from the workspace. On first run a random
// key is generated and persisted.
func loadOrCreateJWTKey(keyDir, dataDir string) ([]byte, error) {
	path := filepath.Join(keyDir, jwtKeyFilename)
	// one-time migration out of the old in-workspace location.
	if legacy := filepath.Join(dataDir, jwtKeyFilename); legacy != path {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if data, rerr := os.ReadFile(legacy); rerr == nil {
				if werr := os.WriteFile(path, data, 0o600); werr == nil {
					_ = os.Remove(legacy)
					log.Printf("[auth] JWT key 已从 %s 迁移到 %s（移出可浏览工作区）", legacy, path)
				}
			}
		}
	}
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) >= 32 {
		return []byte(strings.TrimSpace(string(data))), nil
	}
	buf := make([]byte, 32)
	for i := range buf {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(keyChars))))
		if err != nil {
			return nil, fmt.Errorf("generate jwt key: %w", err)
		}
		buf[i] = keyChars[n.Int64()]
	}
	if err := os.WriteFile(path, buf, 0600); err != nil {
		return nil, fmt.Errorf("write jwt key: %w", err)
	}
	log.Printf("[auth] 新 JWT key 已写入 %s", path)
	return buf, nil
}

// currentJWTKey 原子读取当前签名 key（改密码轮换后立即变化）。
func (s *Server) currentJWTKey() []byte {
	if v, ok := s.jwtKey.Load().([]byte); ok {
		return v
	}
	return nil
}

// rotateJWTKey 生成新 key 原子替换并落盘：**所有已签发 token 立即失效**。
// 改密码必须轮换——否则公网上泄露的旧 token 能一直用到 7 天自然过期，
// 「改密码踢掉攻击者」就成了一句空话。
func (s *Server) rotateJWTKey() error {
	buf := make([]byte, 32)
	for i := range buf {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(keyChars))))
		if err != nil {
			return fmt.Errorf("generate jwt key: %w", err)
		}
		buf[i] = keyChars[n.Int64()]
	}
	if err := os.WriteFile(filepath.Join(s.keyDir, jwtKeyFilename), buf, 0o600); err != nil {
		return fmt.Errorf("write jwt key: %w", err)
	}
	s.jwtKey.Store(buf)
	log.Printf("[auth] JWT key 已轮换（改密码），全部旧会话失效")
	return nil
}

// trustedProxyCIDRs 解析 PANGU_TRUSTED_PROXIES（逗号分隔的 IP 或 CIDR，如
// "127.0.0.1,10.0.0.0/8"）。默认为空 = 不信任任何代理头，直连安全。
func trustedProxyCIDRs() []*net.IPNet {
	var out []*net.IPNet
	for _, part := range strings.Split(os.Getenv("PANGU_TRUSTED_PROXIES"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			// 裸 IP 补全掩码位；IPv6 用 /128。
			if ip := net.ParseIP(part); ip != nil {
				if ip.To4() != nil {
					part += "/32"
				} else {
					part += "/128"
				}
			}
		}
		if _, n, err := net.ParseCIDR(part); err == nil {
			out = append(out, n)
		} else {
			log.Printf("[auth] PANGU_TRUSTED_PROXIES 里的 %q 不是合法 IP/CIDR，已忽略", part)
		}
	}
	return out
}

// ipInTrustedProxy 报告直连对端是否是配置过的可信代理。
func ipInTrustedProxy(ip net.IP) bool {
	for _, n := range trustedProxyCIDRs() {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP 提取客户端 IP：
//   - 直连对端（RemoteAddr）落在 PANGU_TRUSTED_PROXIES 网段内 → 取
//     X-Forwarded-For 首段（部署手册要求反代覆写该头）/X-Real-IP；
//   - 否则一律取 RemoteAddr。
//
// XFF/X-Real-IP 可无条件伪造：直连公网部署下信任它们等于让 per-IP 防爆破失效
// （无限伪造 IP 绕过 5 次/10min 锁定），还会把「伪造海量失败 IP」放大成全局
// 登录锁——管理员被攻击者反复锁死。所以只在显式声明了可信代理时才采信。
// 反代部署需设置 PANGU_TRUSTED_PROXIES 指向代理网段，防爆破才按真实客户端 IP
// 生效；不设置时全局限流退化为按反代 IP 计（更严，安全侧默认）。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ipInTrustedProxy(ip) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.Index(xff, ","); i > 0 {
				if first := strings.TrimSpace(xff[:i]); first != "" {
					return first
				}
			} else if first := strings.TrimSpace(xff); first != "" {
				return first
			}
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			if xr = strings.TrimSpace(xr); xr != "" {
				return xr
			}
		}
	}
	return host
}

// loginLimiter 是登录/初始化接口的进程级防爆破闸门：
//   - 每 IP：10 分钟窗口内失败 5 次 → 锁 15 分钟；
//   - 全局：1 分钟窗口内失败 30 次**且来自 ≥5 个不同 IP** → 全局锁 5 分钟
//     （兜真实分布式低速爆破）。
//
// 「不同 IP」条件是防 DoS 的关键：单源攻击者（哪怕伪造 XFF——clientIP 修复后
// 伪造已无效）只会把自己锁进 per-IP 闸门，不能把全体管理员的登录拖进全局锁。
// 没有这个条件，攻击者每 5 分钟发 30 个坏登录就能让管理员无限期 429。
//
// 内存态即可：重启清零只影响攻击者的等待时间，不影响正确性。
// 返回 (允许, 剩余锁定时长)。
type loginLimiter struct {
	mu          sync.Mutex
	fails       map[string][]time.Time // ip → 失败时间序列（滑动窗口）
	lockedUntil map[string]time.Time   // ip → 锁定截止
	globalFails []globalFail           // (时间, ip) 滑动窗口
	globalUntil time.Time
}

// globalFail 记录一次全局窗口内的失败来源，用于「不同 IP 数」判定。
type globalFail struct {
	at time.Time
	ip string
}

var loginLimit = &loginLimiter{fails: map[string][]time.Time{}, lockedUntil: map[string]time.Time{}}

const (
	loginMaxFailsPerIP   = 5
	loginFailWindow      = 10 * time.Minute
	loginLockoutPerIP    = 15 * time.Minute
	loginGlobalMaxFails  = 30
	loginGlobalMinIPs    = 5                       // 全局锁要求的最小独立来源数
	loginGlobalWindow    = time.Minute
	loginGlobalLockout   = 5 * time.Minute
	loginFailureMinDelay = 300 * time.Millisecond // 失败响应的人为下限，抹平时序差
)

func (l *loginLimiter) allow(ip string) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if until, ok := l.lockedUntil[ip]; ok && now.Before(until) {
		return false, until.Sub(now)
	}
	if now.Before(l.globalUntil) {
		return false, l.globalUntil.Sub(now)
	}
	// 惰性清理过期窗口，防 map 随扫描流量无限增长。
	for k, until := range l.lockedUntil {
		if now.After(until) && len(l.fails[k]) == 0 {
			delete(l.lockedUntil, k)
		}
	}
	return true, 0
}

func (l *loginLimiter) recordFail(ip string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	// per-IP 滑动窗口。
	f := append(l.fails[ip], now)
	cut := now.Add(-loginFailWindow)
	i := 0
	for i < len(f) && f[i].Before(cut) {
		i++
	}
	f = f[i:]
	l.fails[ip] = f
	if len(f) >= loginMaxFailsPerIP {
		l.lockedUntil[ip] = now.Add(loginLockoutPerIP)
		l.fails[ip] = nil
	}
	// 全局滑动窗口（带独立来源数判定，见 loginGlobalMinIPs）。
	l.globalFails = append(l.globalFails, globalFail{at: now, ip: ip})
	gcut := now.Add(-loginGlobalWindow)
	j := 0
	for j < len(l.globalFails) && l.globalFails[j].at.Before(gcut) {
		j++
	}
	l.globalFails = l.globalFails[j:]
	if len(l.globalFails) >= loginGlobalMaxFails {
		distinct := map[string]bool{}
		for _, f := range l.globalFails {
			distinct[f.ip] = true
		}
		if len(distinct) >= loginGlobalMinIPs {
			l.globalUntil = now.Add(loginGlobalLockout)
			l.globalFails = nil
		}
	}
}

func (l *loginLimiter) recordSuccess(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
	delete(l.lockedUntil, ip)
}

// loginGuard 统一包住 login/init 的防爆破语义：超限直接 429，失败记一笔并
// 人为拖慢响应。返回 false 表示已写响应、调用方直接 return。
func loginGuard(w http.ResponseWriter, r *http.Request, action string) bool {
	ip := clientIP(r)
	if ok, wait := loginLimit.allow(ip); !ok {
		log.Printf("[auth] %s 限流触发 ip=%s 剩余=%s", action, ip, wait.Truncate(time.Second))
		writeErr(w, http.StatusTooManyRequests, "尝试过于频繁，请稍后再试")
		return false
	}
	if action == "login-fail" {
		loginLimit.recordFail(ip)
		log.Printf("[auth] 登录失败 ip=%s", ip)
		time.Sleep(loginFailureMinDelay)
	}
	return true
}

// signJWT issues a 7-day HS256 token for user PANGU. Each token carries a
// random 16-byte jti so an individual session can be revoked precisely
// without touching other devices (see session_revoke.go).
func signJWT(key []byte) (string, error) {
	jti, err := randomJTI()
	if err != nil {
		return "", err
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "PANGU",
		ID:        jti,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(jwtTTL)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}).SignedString(key)
}

// parseSessionJWT validates a token and returns the claims requireAuth needs
// (jti + iat). It is the claims-aware counterpart of verifyJWT.
func parseSessionJWT(tokenStr string, key []byte) (sessionClaims, bool) {
	var claims jwt.RegisteredClaims
	t, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return key, nil
	})
	if err != nil || !t.Valid {
		return sessionClaims{}, false
	}
	c := sessionClaims{JTI: claims.ID}
	if claims.IssuedAt != nil {
		c.IAT = claims.IssuedAt.Time
	}
	return c, true
}

// verifyJWT returns true when tokenStr is a valid, non-expired HS256 token.
func verifyJWT(tokenStr string, key []byte) bool {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return key, nil
	})
	return err == nil && t.Valid
}

// extractToken reads the JWT from the Authorization: Bearer header or the
// pangu_token cookie.
//
// 历史上 SSE 流端点接受 ?token= 查询参数——但 token 会进反向代理的 access log、
// X-Forwarded 头与浏览器历史/Referer，一个 7 天有效的 JWT 留在别人的日志里是
// 实打实的会话劫持面。现在 EventSource 走 HttpOnly cookie（同源请求浏览器自动
// 携带，cookie 不区分端口，dev 的 :3000→:8787 同样适用），query 通道整体移除。
// apiKeyVerify 校验开放 API 凭证（M-12）。优先 X-API-Key 头，其次
// Authorization: Bearer pk_…（CI/脚本常见写法）。查询失败 fail-closed。
func (s *Server) apiKeyVerify(r *http.Request) (db.APIKeyAuth, bool) {
	if s.m == nil || s.m.pg == nil {
		return db.APIKeyAuth{}, false
	}
	raw := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if raw == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer pk_") {
			raw = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
	}
	if raw == "" {
		return db.APIKeyAuth{}, false
	}
	return s.m.pg.VerifyAPIKey(r.Context(), raw, clientIP(r))
}

// apiKeyCtxKey 把已校验的 Key 授权信息挂到请求上下文：MCP 服务端等需要知道
// 调用方是 JWT 会话还是某把程序化凭证（例如决定是否允许写操作）。
type apiKeyCtxKey struct{}

// apiKeyAuthFrom 读取上下文里的 Key 授权信息；JWT 会话返回 false。
func apiKeyAuthFrom(r *http.Request) (db.APIKeyAuth, bool) {
	v, ok := r.Context().Value(apiKeyCtxKey{}).(db.APIKeyAuth)
	return v, ok
}

// apiKeyAdminSegments 是「改平台自身行为」的入口首段——这类能力给到程序化凭证
// 等于给了整台平台：装宿主工具（任意包安装）、自定义工具（任意代码执行）、
// LLM 通道配置（可把 token 引到外部）、一键更新、MCP 客户端配置（可外连任意服务）。
//
// 用首段而不是字符串前缀匹配：/api/llm 是配置（admin），/api/llm-records 是
// 只读请求记录（read），前缀匹配会把后者一起升到 admin。
var apiKeyAdminSegments = map[string]bool{
	"llm": true, "settings": true, "system": true, "update": true,
	"hostenv": true, "hosttools": true, "mcp": true, "api-keys": true,
	"skills": true, "agents": true, "prompts": true, "tools": true,
	"notify": true, "proxypool": true, "c2": true, "listeners": true,
	"backup": true, "logs": true, "report-templates": true,
	// 认证面：改密码 / 吊销全部会话 / 吊销当前会话都能直接接管或踢掉管理员。
	"auth": true,
}

// apiKeyReadOnlyPaths 是落在 admin 段内、但语义只读的精确路径例外。
// 对外 MCP 出口（/api/mcp/server）目前只暴露只读工具，read 档应当可用。
var apiKeyReadOnlyPaths = map[string]bool{
	"/api/mcp/server": true,
}

// requiredAPIKeyScope 判定一次请求需要的最低权限档。判定规则刻意保守：
// 未知路径按 admin 段处理（多拦不漏放），非 GET 一律至少 write。
func requiredAPIKeyScope(method, path string) string {
	if apiKeyReadOnlyPaths[path] {
		return db.APIKeyScopeRead
	}
	seg := strings.TrimPrefix(path, "/api/")
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	if apiKeyAdminSegments[seg] {
		return db.APIKeyScopeAdmin
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return db.APIKeyScopeRead
	}
	return db.APIKeyScopeWrite
}

// authExemptPath 是 requireAuth 的免鉴权白名单。刻意精确到端点而不是
// "/api/auth/" 前缀——前缀豁免会把 change-password / revoke-* 一起放进来，
// 一把 read 档的 Key 就能吊销管理员全部会话。
func authExemptPath(p string) bool {
	switch p {
	case "/api/health",
		"/api/auth/status",
		"/api/auth/init",
		"/api/auth/login",
		"/api/auth/logout":
		return true
	}
	return false
}

// apiKeyPass 用 API Key 完成一次鉴权：校验 → 判 scope → 放行或 403。
// 返回 true 表示请求已被本函数处理（无论放行还是拒绝）；false 表示请求没带 Key。
func (s *Server) apiKeyPass(w http.ResponseWriter, r *http.Request, h http.Handler) bool {
	auth, ok := s.apiKeyVerify(r)
	if !ok {
		return false
	}
	need := requiredAPIKeyScope(r.Method, r.URL.Path)
	if !db.APIKeyScopeAllows(auth.Scope, need) {
		log.Printf("[api-key] 拒绝 #%d（scope=%s）%s %s：需要 %s", auth.ID, auth.Scope, r.Method, r.URL.Path, need)
		writeErr(w, 403, fmt.Sprintf("该 API Key 的权限范围是 %s，本次调用需要 %s", auth.Scope, need))
		return true
	}
	h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), apiKeyCtxKey{}, auth)))
	return true
}

func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie("pangu_token"); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

// setAuthCookie 把 JWT 写进 HttpOnly cookie：token 的浏览器侧载体从
// localStorage + 可读 cookie 收敛为 HttpOnly cookie——XSS 偷不走，前端也不再
// 需要落盘。PANGU_COOKIE_SECURE=1 时加 Secure（经 TLS 部署时打开）。
func setAuthCookie(w http.ResponseWriter, tok string) {
	c := &http.Cookie{
		Name:     "pangu_token",
		Value:    tok,
		Path:     "/",
		MaxAge:   int(jwtTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	if os.Getenv("PANGU_COOKIE_SECURE") == "1" {
		c.Secure = true
	}
	http.SetCookie(w, c)
}

// clearAuthCookie 使浏览器侧会话 cookie 立即失效（登出/改密轮换后调用）。
func clearAuthCookie(w http.ResponseWriter) {
	c := &http.Cookie{
		Name:     "pangu_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	if os.Getenv("PANGU_COOKIE_SECURE") == "1" {
		c.Secure = true
	}
	http.SetCookie(w, c)
}

// POST /api/auth/logout — 清除 HttpOnly 会话 cookie。JWT 服务端无逐请求黑名单
// （需要踢会话用 /api/auth/revoke-current 或 /api/auth/revoke-all-sessions），
// 这个端点存在的意义是把「登出」从前端手搓 document.cookie 收敛为后端权威动作。
func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	clearAuthCookie(w)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// requireAuth wraps h with JWT validation plus session revocation checks
// (sentinel timestamp + per-jti list, see session_revoke.go).
// /api/auth/* and /api/health are exempt.
func (s *Server) requireAuth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if authExemptPath(p) {
			h.ServeHTTP(w, r)
			return
		}
		tok := extractToken(r)
		if tok == "" {
			// 开放 API 凭证（M-12）：X-API-Key 头。与 JWT 走完全相同的下游——
			// 任务创建、拦截审批、范围守卫一个不少。它不是多用户，只是同一管理员
			// 账号的程序化调用方式；scope 决定它能走到哪一层。
			if s.apiKeyPass(w, r, h) {
				return
			}
			writeErr(w, 401, "未授权")
			return
		}
		claims, ok := parseSessionJWT(tok, s.currentJWTKey())
		if !ok {
			// 兼容把 Key 放进 Authorization: Bearer 的调用方（CI 常见写法）。
			if s.apiKeyPass(w, r, h) {
				return
			}
			writeErr(w, 401, "token 无效或已过期")
			return
		}
		if !s.sessionAllowed(claims) {
			writeErr(w, 401, "会话已被吊销，请重新登录")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// POST /api/auth/revoke-all-sessions — 吊销全部会话（含历史无 jti token）。
// 在 settings 落哨兵时间戳：iat 早于（含同秒，fail-closed）该时点的 token 全部
// 失效。破坏性动作，要求请求体显式 {"confirm":true}，缺则 400——防误触/CSRF
// 一发即全端登出。
func (s *Server) authRevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	if _, ok := parseSessionJWT(extractToken(r), s.currentJWTKey()); !ok {
		writeErr(w, 401, "未授权")
		return
	}
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if err := decode(r, &req); err != nil || !req.Confirm {
		writeErr(w, 400, `缺少二次确认：请求体必须显式包含 {"confirm":true}`)
		return
	}
	if err := s.revokeAllSessions(pg); err != nil {
		writeErr(w, 500, "吊销失败: "+err.Error())
		return
	}
	clearAuthCookie(w)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST /api/auth/revoke-current — 按 jti 精确吊销当前会话，不影响其他设备。
// 旧版 token（无 jti）无法精确吊销，由哨兵（吊销全部会话）覆盖。
func (s *Server) authRevokeCurrentSession(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	claims, ok := parseSessionJWT(extractToken(r), s.currentJWTKey())
	if !ok {
		writeErr(w, 401, "未授权")
		return
	}
	if claims.JTI == "" {
		writeErr(w, 400, "当前会话为旧版 token（无 jti），无法精确吊销；请使用「吊销全部会话」")
		return
	}
	if err := s.revokeSessionJTI(pg, claims.JTI); err != nil {
		writeErr(w, 500, "吊销失败: "+err.Error())
		return
	}
	clearAuthCookie(w)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// GET /api/auth/status — reports whether the admin password has been initialised.
// 读失败必须回 503 而不是 initialized:false：前端在 initialized:false 时会把用户
// 送到 /setup 去设置密码（login/page.tsx），把数据库故障包装成 200 等于把用户往
// 覆盖已有密码的路上推。
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	hash, _, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	// init_token_required：告知客户端 init 端点要求 X-Init-Token（PANGU_INIT_TOKEN
	// 已设置）。前端暂不消费该字段，主要用于部署排障（curl 一眼确认开关生效）。
	writeJSON(w, 200, map[string]any{
		"initialized":         hash != "",
		"init_token_required": strings.TrimSpace(os.Getenv(initTokenEnv)) != "",
	})
}

// POST /api/auth/init — sets the password for the first time; rejected if already set.
func (s *Server) authInit(w http.ResponseWriter, r *http.Request) {
	if !loginGuard(w, r, "init") {
		return
	}
	// 公网抢注加固：设置了 PANGU_INIT_TOKEN 时，init 必须携带匹配的令牌头。
	// 恒时比较，避免用响应时序逐字节猜令牌。
	if want := strings.TrimSpace(os.Getenv(initTokenEnv)); want != "" {
		got := r.Header.Get(initTokenHeader)
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeErr(w, 403, "需要初始化令牌（"+initTokenHeader+"）")
			return
		}
	}
	pg := s.pg(w)
	if pg == nil {
		return
	}
	existing, _, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	if existing != "" {
		writeErr(w, 403, "密码已设置")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil || req.Password == "" {
		writeErr(w, 400, "密码不能为空")
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "密码加密失败")
		return
	}
	// 用 INSERT ... ON CONFLICT DO NOTHING 而不是 upsert：上面那次 GetSetting 只是
	// 快速失败路径，真正"仅首次可设"的保证落在主键约束上。bcrypt 要跑几十毫秒，
	// 这期间别的请求完全可能先把密码设好，而读检查本身也可能因故障而失效。
	inserted, err := pg.InsertSettingIfAbsent(authPassKey, string(hash))
	if err != nil {
		writeErr(w, 500, "保存失败: "+err.Error())
		return
	}
	if !inserted {
		writeErr(w, 403, "密码已设置")
		return
	}
	tok, err := signJWT(s.currentJWTKey())
	if err != nil {
		writeErr(w, 500, "token 生成失败")
		return
	}
	setAuthCookie(w, tok)
	writeJSON(w, 200, map[string]any{"token": tok})
}

// POST /api/auth/change-password — changes the admin password. Requires a valid
// token (this route is under /api/auth/* which requireAuth exempts, so the token
// is validated here) AND the current password.
func (s *Server) authChangePassword(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	if !verifyJWT(extractToken(r), s.currentJWTKey()) {
		writeErr(w, 401, "未授权")
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if req.NewPassword == "" {
		writeErr(w, 400, "新密码不能为空")
		return
	}
	if msg := validatePassword(req.NewPassword); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	hash, ok, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	if !ok || hash == "" {
		writeErr(w, 403, "密码未初始化，请先设置密码")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.OldPassword)); err != nil {
		writeErr(w, 401, "当前密码错误")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "密码加密失败")
		return
	}
	if err := pg.SetSetting(authPassKey, string(newHash)); err != nil {
		writeErr(w, 500, "保存失败: "+err.Error())
		return
	}
	// 轮换签名 key：旧密码可能已经泄露（改密码本身往往就是怀疑泄露的动作），
	// 不轮换的话用旧 token 的会话全部存活到自然过期。
	if err := s.rotateJWTKey(); err != nil {
		// 密码已改成功、轮换失败：不回滚（密码改了比没改安全），但要显式告警。
		log.Printf("[auth] 改密码后 JWT key 轮换失败: %v（旧 token 仍然有效，请重启服务强制轮换）", err)
	}
	// 旧 token 已随 key 轮换全部失效，浏览器侧 cookie 一并清掉，前端回到登录页。
	clearAuthCookie(w)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST /api/auth/login — validates username/password and returns a JWT.
func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	if !loginGuard(w, r, "login") {
		return
	}
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if req.Username != "PANGU" && req.Username != "ARTEX" { // v0.4.19 改名：兼容旧用户名
		writeErr(w, 401, "用户名或密码错误")
		return
	}
	hash, ok, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	if !ok || hash == "" {
		writeErr(w, 403, "密码未初始化，请先设置密码")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		loginGuard(w, r, "login-fail")
		writeErr(w, 401, "用户名或密码错误")
		return
	}
	loginLimit.recordSuccess(clientIP(r))
	tok, err := signJWT(s.currentJWTKey())
	if err != nil {
		writeErr(w, 500, "token 生成失败")
		return
	}
	setAuthCookie(w, tok)
	writeJSON(w, 200, map[string]any{"token": tok})
}
