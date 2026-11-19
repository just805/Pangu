package server

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// B1 会话吊销回归：jti 签发/解析、哨兵全量吊销（含无 jti 历史 token）、
// 按 jti 精确吊销、confirm 二次确认、读失败 fail-open。
// 需要真 PG；不可用时整体跳过（与 auth_test.go 的模式一致）。

func newRevocationTestServer(t *testing.T) *Server {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	s := &Server{m: m}
	s.jwtKey.Store([]byte("0123456789abcdef0123456789abcdef"))
	return s
}

// TestSignJWTCarriesDistinctJTI：16 字节随机 jti（32 hex）必须可解析且逐 token 唯一。
func TestSignJWTCarriesDistinctJTI(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	a, err := signJWT(key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := signJWT(key)
	if err != nil {
		t.Fatal(err)
	}
	ca, ok := parseSessionJWT(a, key)
	if !ok {
		t.Fatal("signed token did not parse")
	}
	cb, _ := parseSessionJWT(b, key)
	if len(ca.JTI) != 32 {
		t.Fatalf("jti 不是 32 字符 hex: %q", ca.JTI)
	}
	if ca.JTI == cb.JTI {
		t.Fatal("两次签发的 jti 相同")
	}
	if ca.IAT.IsZero() {
		t.Fatal("iat 缺失，哨兵比对将失效")
	}
}

// TestRevokeAllSessionsSentinel：吊销后，哨兵之前签发的 token（含无 jti 的
// 历史形态）全部失效；iat 严格晚于哨兵的 token 仍然放行。
func TestRevokeAllSessionsSentinel(t *testing.T) {
	s := newRevocationTestServer(t)
	old, err := signJWT(s.currentJWTKey())
	if err != nil {
		t.Fatal(err)
	}
	claimsOld, ok := parseSessionJWT(old, s.currentJWTKey())
	if !ok {
		t.Fatal("old token did not parse")
	}
	if !s.sessionAllowed(claimsOld) {
		t.Fatal("未吊销时不应拒绝")
	}
	if err := s.revokeAllSessions(s.m.pg); err != nil {
		t.Fatal(err)
	}
	if s.sessionAllowed(claimsOld) {
		t.Fatal("哨兵之前的 token 应被全量吊销")
	}
	// 同秒签发（iat == 哨兵）fail-closed。
	if s.sessionAllowed(sessionClaims{JTI: "same-second", IAT: s.sessRev.allBefore}) {
		t.Fatal("iat 与哨兵同秒的 token 应按 fail-closed 拒绝")
	}
	// 重新登录即恢复：iat 晚于哨兵的 token 放行。
	if !s.sessionAllowed(sessionClaims{JTI: "new", IAT: s.sessRev.allBefore.Add(time.Second)}) {
		t.Fatal("哨兵之后签发的 token 不应被拒")
	}
	// 无 jti 的历史 token 由哨兵覆盖：哨兵之前签发的无 jti token 也失效。
	legacy := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "PANGU",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		IssuedAt:  jwt.NewNumericDate(s.sessRev.allBefore.Add(-time.Second)),
	})
	legacyStr, err := legacy.SignedString(s.currentJWTKey())
	if err != nil {
		t.Fatal(err)
	}
	legacyClaims, ok := parseSessionJWT(legacyStr, s.currentJWTKey())
	if !ok || legacyClaims.JTI != "" {
		t.Fatalf("legacy token 应无 jti: %+v", legacyClaims)
	}
	if s.sessionAllowed(legacyClaims) {
		t.Fatal("无 jti 的历史 token 应由哨兵覆盖")
	}
}

// TestRevokeCurrentSessionPrecise：按 jti 吊销只影响目标会话，其他设备不受牵连。
func TestRevokeCurrentSessionPrecise(t *testing.T) {
	s := newRevocationTestServer(t)
	tokA, err := signJWT(s.currentJWTKey())
	if err != nil {
		t.Fatal(err)
	}
	tokB, err := signJWT(s.currentJWTKey())
	if err != nil {
		t.Fatal(err)
	}
	claimsA, _ := parseSessionJWT(tokA, s.currentJWTKey())
	claimsB, _ := parseSessionJWT(tokB, s.currentJWTKey())
	if claimsA.JTI == claimsB.JTI || claimsA.JTI == "" || claimsB.JTI == "" {
		t.Fatalf("jti 应唯一且非空: %q vs %q", claimsA.JTI, claimsB.JTI)
	}
	if err := s.revokeSessionJTI(s.m.pg, claimsA.JTI); err != nil {
		t.Fatal(err)
	}
	if s.sessionAllowed(claimsA) {
		t.Fatal("被吊销的 jti 应拒绝")
	}
	if !s.sessionAllowed(claimsB) {
		t.Fatal("其他设备（不同 jti）不应被牵连")
	}
}

// TestRevokeAllRequiresExplicitConfirm：缺 confirm 直接 400，不落哨兵。
func TestRevokeAllRequiresExplicitConfirm(t *testing.T) {
	s := newRevocationTestServer(t)
	tok, err := signJWT(s.currentJWTKey())
	if err != nil {
		t.Fatal(err)
	}
	call := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/auth/revoke-all-sessions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		s.authRevokeAllSessions(w, req)
		return w
	}
	if w := call(`{}`); w.Code != 400 {
		t.Fatalf("缺 confirm 应 400，得到 %d: %s", w.Code, w.Body.String())
	}
	if w := call(`{"confirm":false}`); w.Code != 400 {
		t.Fatalf("confirm=false 应 400，得到 %d: %s", w.Code, w.Body.String())
	}
	w := call(`{"confirm":true}`)
	if w.Code != 200 {
		t.Fatalf("显式 confirm=true 应成功，得到 %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Set-Cookie") == "" {
		t.Fatal("成功吊销后应清除会话 cookie")
	}
}

// TestRevokeCurrentRejectsLegacyToken：无 jti 的历史 token 无法精确吊销，400。
func TestRevokeCurrentRejectsLegacyToken(t *testing.T) {
	s := newRevocationTestServer(t)
	legacy := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "PANGU",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	})
	legacyStr, err := legacy.SignedString(s.currentJWTKey())
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/auth/revoke-current", nil)
	req.Header.Set("Authorization", "Bearer "+legacyStr)
	s.authRevokeCurrentSession(w, req)
	if w.Code != 400 {
		t.Fatalf("无 jti 的 token 应 400 引导走全量吊销，得到 %d: %s", w.Code, w.Body.String())
	}
}

// TestSessionRevokeFailsOpen：settings 读失败时沿用上次快照放行（fail-open）。
// 从未装载过快照（无任何吊销事实）也放行，不挡正常请求。
func TestSessionRevokeFailsOpen(t *testing.T) {
	s := newRevocationTestServer(t)
	claims := sessionClaims{JTI: "abc", IAT: time.Now()}
	if !s.sessionAllowed(claims) {
		t.Fatal("从未装载快照时（无吊销事实）应放行")
	}
	// 装一份"已吊销"的内存快照，再关掉 DB：读失败必须沿用旧值，而不是清空。
	if err := s.revokeSessionJTI(s.m.pg, "abc"); err != nil {
		t.Fatal(err)
	}
	if s.sessionAllowed(claims) {
		t.Fatal("快照中的吊销应生效")
	}
	if err := s.m.pg.Close(); err != nil {
		t.Fatal(err)
	}
	// 强制下一次走 DB 刷新（TTL 过期路径），刷新失败沿用内存旧值。
	s.sessRev.mu.Lock()
	s.sessRev.loadedAt = time.Now().Add(-sessionRevokeCacheTTL - time.Second)
	s.sessRev.mu.Unlock()
	if s.sessionAllowed(claims) {
		t.Fatal("settings 读失败应沿用旧快照拒绝已吊销会话")
	}
	// 而一个不在旧快照里的会话在故障期间放行（fail-open 不扩大拒绝面）。
	if !s.sessionAllowed(sessionClaims{JTI: "other", IAT: time.Now()}) {
		t.Fatal("settings 读失败不应拒绝快照之外的会话")
	}
}
