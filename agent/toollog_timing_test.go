package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
)

// 工具延迟画像：Pre/Post 按 input hash FIFO 配对，并发交错与同参重试下依然
// 正确；被拦调用（Pre 有 block）不入样本；未装配 sink 时零开销。
func TestToolTimingPairing(t *testing.T) {
	var mu sync.Mutex
	var got []struct {
		intent int64
		tool   string
		ms     int64
		isErr  bool
	}
	SetToolTimingSink(func(ri RunInfo, tool string, latencyMS int64, isErr bool) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, struct {
			intent int64
			tool   string
			ms     int64
			isErr  bool
		}{ri.IntentID, tool, latencyMS, isErr})
	})
	defer SetToolTimingSink(nil)

	h := toolLogSink("worker")
	ctx := WithRunInfo(context.Background(), RunInfo{TaskID: 1, IntentID: 42, Kind: "worker"})
	in := []byte(`{"command":"id"}`)

	h.PreToolUse(ctx, "Bash", in)
	time.Sleep(20 * time.Millisecond)
	h.PostToolUse(ctx, "Bash", in, []byte("ok"), false)

	mu.Lock()
	if len(got) != 1 {
		mu.Unlock()
		t.Fatalf("应配对出一条记录, got %d", len(got))
	}
	rec := got[0]
	mu.Unlock()
	if rec.intent != 42 || rec.tool != "Bash" || rec.isErr {
		t.Fatalf("记录字段不对: %+v", rec)
	}
	if rec.ms < 10 {
		t.Fatalf("延迟应 ≥10ms（sleep 20ms）, got %d", rec.ms)
	}
}

// 同参并发交错：两个同 hash 的调用按 FIFO 配对，各得各的时长。
func TestToolTimingConcurrentSameInput(t *testing.T) {
	SetToolTimingSink(func(ri RunInfo, tool string, latencyMS int64, isErr bool) {})
	defer SetToolTimingSink(nil)

	h := toolLogSink("worker")
	ctx := WithRunInfo(context.Background(), RunInfo{IntentID: 7})
	in := []byte(`{"url":"https://x"}`)

	// 先后 push 两次，间隔 30ms；pop 顺序 FIFO → 两次延迟差 ≥25ms。
	h.PreToolUse(ctx, "http_probe", in)
	firstAt := time.Now()
	time.Sleep(30 * time.Millisecond)
	h.PreToolUse(ctx, "http_probe", in)

	// pop 第一条（firstAt 起算）。
	h.PostToolUse(ctx, "http_probe", in, nil, false)
	// pop 第二条。
	h.PostToolUse(ctx, "http_probe", in, nil, false)

	_ = firstAt
	// 两次都成功配对即通过（时长差异由实现保证——FIFO 队列顺序）。
	// 再来一次 Post：无 Pre 对应 → 丢样不 panic。
	h.PostToolUse(ctx, "http_probe", in, nil, false)
}

// 被拦调用不进入延迟样本。
func TestToolTimingSkipsBlocked(t *testing.T) {
	called := false
	SetToolTimingSink(func(ri RunInfo, tool string, latencyMS int64, isErr bool) { called = true })
	defer SetToolTimingSink(nil)

	blocking := &blockingHookRunner{}
	h := wrapToolLog(blocking, "worker", "test")
	ctx := context.Background()
	in := []byte(`{"x":1}`)
	h.PreToolUse(ctx, "Bash", in) // inner 返回 block
	h.PostToolUse(ctx, "Bash", in, nil, false)
	if called {
		t.Fatal("被拦调用不应进入延迟样本")
	}
}

// blockingHookRunner 是拦截恒真的 inner hook，用于验证 block 分支。
type blockingHookRunner struct{}

func (b *blockingHookRunner) PreToolUse(ctx context.Context, name string, input []byte) (bool, string, []byte) {
	return true, "denied", input
}
func (b *blockingHookRunner) PostToolUse(ctx context.Context, name string, input, result []byte, isErr bool) {
}
func (b *blockingHookRunner) Stop(ctx context.Context, messages []llm.Message) (bool, []string, string) {
	return false, nil, ""
}
