package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/just805/pangu/db"
)

// B1 会话吊销：JWT 签发带 jti（16 字节随机）。「吊销全部会话」在 settings 落
// 哨兵时间戳——iat 早于（含同秒，fail-closed）它的 token 全部失效，含无 jti 的
// 历史 token；「吊销当前会话」按 jti 精确失效，不牵连其他设备。校验挂在
// requireAuth，内存快照 30s TTL 从 settings 刷新，写端点主动更新内存。settings
// 读失败沿用上次快照放行（fail-open，纵深防御不挡正常请求）；旧 token（无 jti）
// 不能逐个吊销，由哨兵覆盖。

const (
	settingRevokeAllBefore = "auth.revoke_all_before" // 哨兵：unix 秒
	settingRevokedJtis     = "auth.revoked_jtis"      // JSON: {jti: revoked_at_unix}
	sessionRevokeCacheTTL  = 30 * time.Second
	maxRevokedJtis         = 500 // 上限：到 jwtTTL 自然过期前最多留这么多条目
)

// sessionRevocations 是 settings 吊销状态的内存快照（30s TTL）。零值可用：
// 无快照 = 无吊销，放行。
type sessionRevocations struct {
	mu        sync.RWMutex
	allBefore time.Time             // 零值 = 未吊销过全部会话
	jtis      map[string]time.Time  // 已精确吊销的会话
	loadedAt  time.Time             // 快照装载时间
}

// sessionClaims 是 requireAuth 需要的最小 token 事实。
type sessionClaims struct {
	JTI string
	IAT time.Time
}

// randomJTI 返回 16 字节随机的 32 字符 hex 串。
func randomJTI() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate jti: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// refreshRevocations 从 settings 装载吊销快照（带 30s TTL 与并发去重）。单个
// key 读失败时保留该 key 的旧值（fail-open）；装载成功与否都推进 loadedAt，
// 避免故障期间每个请求都打一次 DB。
func (s *Server) refreshRevocations(pg *db.DB) {
	s.sessRev.mu.Lock()
	defer s.sessRev.mu.Unlock()
	if time.Since(s.sessRev.loadedAt) < sessionRevokeCacheTTL {
		return
	}
	if s.sessRev.jtis == nil {
		s.sessRev.jtis = map[string]time.Time{}
	}
	if v, ok, err := pg.GetSetting(settingRevokeAllBefore); err == nil {
		if ok && strings.TrimSpace(v) != "" {
			if sec, perr := strconv.ParseInt(strings.TrimSpace(v), 10, 64); perr == nil {
				s.sessRev.allBefore = time.Unix(sec, 0)
			}
		}
	}
	if v, ok, err := pg.GetSetting(settingRevokedJtis); err == nil && ok && strings.TrimSpace(v) != "" {
		var m map[string]int64
		if json.Unmarshal([]byte(v), &m) == nil {
			now := time.Now()
			trimmed := make(map[string]time.Time, len(m))
			for id, at := range m {
				t := time.Unix(at, 0)
				// 超过 jwtTTL 的吊销记录已无存活 token 可匹配，裁剪防无限增长。
				if id != "" && now.Sub(t) < jwtTTL {
					trimmed[id] = t
				}
			}
			s.sessRev.jtis = trimmed
		}
	}
	s.sessRev.loadedAt = time.Now()
}

// sessionAllowed 报告一个签名合法的 token 是否已被吊销。
func (s *Server) sessionAllowed(c sessionClaims) bool {
	pg := s.m.pg
	if pg != nil {
		s.sessRev.mu.RLock()
		stale := time.Since(s.sessRev.loadedAt) >= sessionRevokeCacheTTL
		s.sessRev.mu.RUnlock()
		if stale {
			s.refreshRevocations(pg)
		}
	}
	s.sessRev.mu.RLock()
	defer s.sessRev.mu.RUnlock()
	if !s.sessRev.allBefore.IsZero() && !c.IAT.IsZero() && !c.IAT.After(s.sessRev.allBefore) {
		// iat <= 哨兵（同秒 fail-closed：吊销时点及同秒签发的 token 一律失效）。
		return false
	}
	if c.JTI != "" {
		if _, ok := s.sessRev.jtis[c.JTI]; ok {
			return false
		}
	}
	return true
}

// revokeAllSessions 落哨兵并立即更新内存快照（不等 30s TTL）。
func (s *Server) revokeAllSessions(pg *db.DB) error {
	now := time.Now().Truncate(time.Second)
	if err := pg.SetSetting(settingRevokeAllBefore, strconv.FormatInt(now.Unix(), 10)); err != nil {
		return err
	}
	s.sessRev.mu.Lock()
	s.sessRev.allBefore = now
	s.sessRev.loadedAt = time.Now()
	s.sessRev.mu.Unlock()
	log.Printf("[auth] 已吊销全部会话（哨兵 %s，iat 早于该时点的 token 全部失效）", now.Format(time.RFC3339))
	return nil
}

// revokeSessionJTI 按 jti 精确吊销单个会话并立即更新内存快照。
func (s *Server) revokeSessionJTI(pg *db.DB, jti string) error {
	s.sessRev.mu.Lock()
	defer s.sessRev.mu.Unlock()
	if s.sessRev.jtis == nil {
		s.sessRev.jtis = map[string]time.Time{}
	}
	now := time.Now()
	s.sessRev.jtis[jti] = now
	m := make(map[string]int64, len(s.sessRev.jtis))
	for id, at := range s.sessRev.jtis {
		if now.Sub(at) < jwtTTL {
			m[id] = at.Unix()
		}
	}
	// 超上限时丢弃最早的记录：最旧的也快到 jwtTTL 自然过期了。
	for len(m) > maxRevokedJtis {
		oldestID, oldest := "", time.Now().Add(time.Hour)
		for id, at := range s.sessRev.jtis {
			if at.Before(oldest) {
				oldestID, oldest = id, at
			}
		}
		if oldestID == "" {
			break
		}
		delete(s.sessRev.jtis, oldestID)
		delete(m, oldestID)
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := pg.SetSetting(settingRevokedJtis, string(data)); err != nil {
		return err
	}
	s.sessRev.loadedAt = time.Now()
	log.Printf("[auth] 已吊销当前会话 jti=%s", jti)
	return nil
}
