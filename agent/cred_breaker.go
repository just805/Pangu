package agent

// 预置凭据登录熔断（上游 0.4.82 同款语义）。
//
// 问题：密码过期/被改密后，多个 worker 会各自对同一预置凭据反复登录，很容易
// 触发目标的账号锁定策略——锁死的是客户提供的测试或生产账号。
//
// 这里在 Go 执行层做确定性熔断，不依赖模型自律：
//   - 只覆盖【预置凭据】（资产上登记、经 asset_id+auth_index 引用）；模型显式
//     手输的一次性凭据不纳入。
//   - 失败只认 401/403（明确鉴权拒绝）；2xx 清零；其余状态码中性（目标偶发
//     故障或参数写错不应刷爆计数）。
//   - 连续 2 次 401/403 → 熔断 30 分钟：期间 session_login 引用该凭据本地直接
//     拒绝，不发任何网络请求。
//   - 到期放行一次（half-open）：成功复位，仍失败重新熔断。
//   - 凭据内容变化（操作员改密/换 token）哈希不同，熔断自动失效。
//   - 传输错误（连接失败/代理故障）不计失败——那是出口问题。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"time"
)

const (
	credFailThreshold = 2
	credCooldown      = 30 * time.Minute
)

type credBreakerState struct {
	fails     int
	openUntil time.Time
}

type credBreaker struct {
	mu     sync.Mutex
	states map[string]*credBreakerState
}

var loginCredBreaker = &credBreaker{states: map[string]*credBreakerState{}}

// credBreakerKey 用凭据内容做 key：内容一变 key 就变，旧熔断自然失效。
func credBreakerKey(assetID int64, authIndex int, e authEntry) string {
	sum := sha256.Sum256([]byte(e.Type + "\x00" + e.Username + "\x00" + e.Password + "\x00" + e.Token))
	return strconv.FormatInt(assetID, 10) + ":" + strconv.Itoa(authIndex) + ":" + hex.EncodeToString(sum[:8])
}

// check 返回是否放行。熔断期间返回剩余时间用于提示。
func (b *credBreaker) check(key string, now time.Time) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.states[key]
	if st == nil || st.openUntil.IsZero() {
		return true, 0
	}
	if now.Before(st.openUntil) {
		return false, st.openUntil.Sub(now)
	}
	// 到期：half-open 放行一次，由 record 决定复位还是重新熔断。
	return true, 0
}

// record 按状态码记账：2xx 清零；401/403 累计并在达阈值时熔断；其余中性。
func (b *credBreaker) record(key string, status int, now time.Time) {
	if status <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.states[key]
	if st == nil {
		st = &credBreakerState{}
		b.states[key] = st
	}
	switch {
	case status >= 200 && status < 300:
		st.fails = 0
		st.openUntil = time.Time{}
	case status == 401 || status == 403:
		st.fails++
		if st.fails >= credFailThreshold {
			st.openUntil = now.Add(credCooldown)
			st.fails = 0
		}
	default:
		// 中性：不累积也不清零（避免目标 5xx 抖动把已积累的失败洗掉）。
	}
}

// remaining 返回熔断前还剩几次尝试（0 表示已熔断或无计数）。
func (b *credBreaker) remaining(key string, now time.Time) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.states[key]
	if st == nil || !st.openUntil.IsZero() {
		return 0
	}
	if st.fails == 0 {
		return 0
	}
	return credFailThreshold - st.fails
}

func credCooldownMessage(d time.Duration) string {
	mins := int(d.Round(time.Minute) / time.Minute)
	if mins < 1 {
		mins = 1
	}
	return fmt.Sprintf("该预置凭据连续登录失败，已本地熔断 %d 分钟（期间不会向目标发登录请求，避免锁死账号）。"+
		"请改用资产上的其他凭据（auth_index），或先测未授权面；操作员改密后凭据哈希变化会自动解除。", mins)
}
