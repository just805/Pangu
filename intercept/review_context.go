package intercept

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const reviewTextLimit = 4000

const BackgroundUserMessage = "user_message"

// ReviewBackground is explicitly bound from the current human message. Generated
// Worker summaries are not accepted. Background cannot override review policy.
type ReviewBackground struct {
	Source    string `json:"source"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

// ReviewScript is a bounded, read-only snapshot of a local script file that
// the current tool call references (issue #21.2). The body travels to the
// judge so "python3 shared/scripts/exp.py" is reviewed with its content
// visible instead of a bare filename. Scripts are untrusted data: any
// instruction-looking text inside them must not change the verdict.
type ReviewScript struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

// ReviewInput contains only the current call and explicitly selected background.
// Execution history and call correlation belong to the separate audit record.
type ReviewInput struct {
	Version    int               `json:"version"`
	WorkingDir string            `json:"working_directory,omitempty"`
	Background *ReviewBackground `json:"background,omitempty"`
	Tool       string            `json:"tool_name"`
	Arguments  json.RawMessage   `json:"arguments"`
	// Scripts carries snapshots of local script files referenced by this
	// call's command tokens (interpreter arguments / script extensions).
	Scripts []ReviewScript `json:"scripts,omitempty"`
	// UnresolvedScripts lists referenced scripts whose body could NOT be
	// snapshotted: dynamic paths (variables/globs), relative paths with no
	// working-directory anchor, over-limit entries, missing or non-regular
	// files. The judge treats these under the content-invisible rule.
	UnresolvedScripts []string `json:"unresolved_scripts,omitempty"`
}

type reviewContextKey struct{}
type reviewEnvironment struct {
	workingDir string
	background ReviewBackground
}

// WithReviewContext explicitly binds the permitted background for one run. Never
// fall back to the raw turn transcript: it may contain the full scheduler prompt.
// This is application wiring, not a model-callable tool.
func WithReviewContext(ctx context.Context, workingDir string, background ReviewBackground) context.Context {
	return context.WithValue(ctx, reviewContextKey{}, reviewEnvironment{workingDir, background})
}

// WithReviewWorkingDirectory preserves only explicitly selected background.
// Chat runs can be human-initiated or scheduled, so the Agent must not infer
// message provenance from the text it receives.
func WithReviewWorkingDirectory(ctx context.Context, workingDir string) context.Context {
	env, _ := ctx.Value(reviewContextKey{}).(reviewEnvironment)
	env.workingDir = workingDir
	return context.WithValue(ctx, reviewContextKey{}, env)
}

// ReviewWorkingDirectory returns the working directory bound to this run, or
// "" when none is bound.
//
// 用途：工具需要把脚本/中间产物写进「本次 run 的工作目录」（平台的规约要求
// 中间产物不落 /tmp），但工具本身拿不到 run 的目录——它只在自己的 context 里
// 看到调用参数。这个读取器是那个目录的唯一出口。
func ReviewWorkingDirectory(ctx context.Context) string {
	env, _ := ctx.Value(reviewContextKey{}).(reviewEnvironment)
	return env.workingDir
}

func BuildReviewInput(ctx context.Context, tool string, arguments json.RawMessage) (ReviewInput, error) {
	if !json.Valid(arguments) {
		return ReviewInput{}, fmt.Errorf("工具参数不是有效 JSON")
	}
	in := ReviewInput{Version: 5, Tool: tool, Arguments: append(json.RawMessage(nil), arguments...)}
	if env, ok := ctx.Value(reviewContextKey{}).(reviewEnvironment); ok {
		in.WorkingDir = env.workingDir
		background := env.background
		if background.Source == BackgroundUserMessage && strings.TrimSpace(background.Text) != "" {
			var cut bool
			background.Text, cut = bounded(background.Text, reviewTextLimit)
			background.Truncated = background.Truncated || cut
			in.Background = &background
		}
	}
	// issue #21.2: attach local script bodies the command references so the
	// judge reviews content, not filenames. Read-only, strictly bounded, and
	// run on the caller's goroutine — one small Stat/Open/Read per candidate.
	in.Scripts, in.UnresolvedScripts = collectReviewScripts(in.WorkingDir, arguments)
	return in, nil
}
