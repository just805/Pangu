package agent

import (
	"testing"
	"time"
)

func TestCredBreakerOpensAfterTwoAuthRejects(t *testing.T) {
	b := &credBreaker{states: map[string]*credBreakerState{}}
	key := "asset1:0:hash"
	now := time.Now()

	if ok, _ := b.check(key, now); !ok {
		t.Fatal("初始应放行")
	}
	b.record(key, 401, now)
	if ok, _ := b.check(key, now); !ok {
		t.Fatal("一次失败后仍应放行")
	}
	if left := b.remaining(key, now); left != 1 {
		t.Fatalf("一次失败后剩余次数应为 1，得到 %d", left)
	}
	b.record(key, 403, now)
	if ok, wait := b.check(key, now); ok || wait <= 0 {
		t.Fatalf("连续两次 401/403 应熔断，ok=%v wait=%v", ok, wait)
	}
	if ok, _ := b.check(key, now.Add(credCooldown+time.Second)); !ok {
		t.Fatal("熔断到期后应 half-open 放行一次")
	}
}

func TestCredBreakerSuccessClearsAndNeutralKeeps(t *testing.T) {
	b := &credBreaker{states: map[string]*credBreakerState{}}
	key := "k"
	now := time.Now()

	b.record(key, 401, now)
	b.record(key, 500, now) // 中性：不清零
	if left := b.remaining(key, now); left != 1 {
		t.Fatalf("5xx 不应清零失败计数，剩余=%d", left)
	}
	b.record(key, 200, now)
	if left := b.remaining(key, now); left != 0 {
		t.Fatalf("2xx 应清零，剩余=%d", left)
	}
	if ok, _ := b.check(key, now); !ok {
		t.Fatal("清零后应放行")
	}
}

func TestCredBreakerKeyChangesWithCredential(t *testing.T) {
	k1 := credBreakerKey(1, 0, authEntry{Username: "a", Password: "p1"})
	k2 := credBreakerKey(1, 0, authEntry{Username: "a", Password: "p2"})
	k3 := credBreakerKey(1, 1, authEntry{Username: "a", Password: "p1"})
	if k1 == k2 {
		t.Fatal("密码变化时 key 必须变化（熔断自动失效）")
	}
	if k1 == k3 {
		t.Fatal("auth_index 变化时 key 必须变化")
	}
}

func TestCredBreakerTransportErrorIgnored(t *testing.T) {
	b := &credBreaker{states: map[string]*credBreakerState{}}
	key := "k"
	now := time.Now()
	b.record(key, 0, now) // 传输错误 status=0
	if left := b.remaining(key, now); left != 0 {
		t.Fatalf("传输错误不应计入失败，剩余=%d", left)
	}
}
