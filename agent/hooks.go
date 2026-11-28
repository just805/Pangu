package agent

import (
	"context"
	"sync"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/just805/pangu/db"
)

// The server wires a set of process-wide hooks at startup: tool augmentation
// (skills/MCP), the DB-backed tool resolver, stored prompt/wrap-up templates,
// artifact lookups, and skill/traffic policy switches. Tests swap them to
// exercise wiring without a full Manager.
//
// They used to be plain package variables. That raced whenever a test restored
// a hook while another test's planner/worker goroutine was still reading it
// (`go test -race ./server` found several: tool hooks, wrap-up hooks, and
// Server.ctx). Hooks now go through a RWMutex: reads take a read lock, writes
// take the write lock, and callers can no longer assign the variables directly.
// All hooks share one mutex because they are process-wide config, written at
// startup or by tests; contention is irrelevant next to an LLM call.

// ToolAugmentFunc returns the extra tools an agent should see beyond its base
// set (visible skills + MCP servers) plus the deferred-tool wiring.
type ToolAugmentFunc func(ctx context.Context, agentKey string) (extra []actool.CoreTool, def DeferredInfo, cleanup func())

// ToolResolveFunc post-processes an agent's assembled tool list against the DB
// tools table (bindings, enabled flags, description/schema overrides).
type ToolResolveFunc func(ctx context.Context, agentKey string, tools []actool.CoreTool) []actool.CoreTool

// PromptOverrideFunc returns the stored system-prompt template for an agent.
type PromptOverrideFunc func(agentKey string) (string, bool)

// WrapupOverrideFunc returns the stored wrap-up prompt for an agent.
type WrapupOverrideFunc func(agentKey string) (string, bool)

// WrapupTurnsFunc returns the stored wrap-up turn budget for an agent.
type WrapupTurnsFunc func(agentKey string) (int, bool)

// SkillIndexBlockFunc returns the skill-index block appended to a system prompt.
type SkillIndexBlockFunc func(agentKey string) string

// FindingTrafficBindingEnabledFunc reports the global auto-bind policy.
type FindingTrafficBindingEnabledFunc func() bool

// ListTaskArtifactsFunc lists a task's artifacts (host-injected lookup).
type ListTaskArtifactsFunc func(taskID string) ([]db.TaskArtifact, error)

// FindingTaskIDFunc maps a finding id to its owning task id.
type FindingTaskIDFunc func(findingID int64) (string, error)

var (
	hooksMu sync.RWMutex

	toolAugmentHook    ToolAugmentFunc
	toolResolveHook    ToolResolveFunc
	promptOverrideHook PromptOverrideFunc

	wrapupOverrideHook         WrapupOverrideFunc
	wrapupMaxTurnsHook         WrapupTurnsFunc
	wrapupTaskTimeoutHook      WrapupOverrideFunc
	wrapupTaskTimeoutTurnsHook WrapupTurnsFunc
	skillIndexBlockHook        SkillIndexBlockFunc
	findingTrafficBindingHook  FindingTrafficBindingEnabledFunc
	listTaskArtifactsHook      ListTaskArtifactsFunc
	findingTaskIDHook          FindingTaskIDFunc
)

func SetToolAugment(fn ToolAugmentFunc) {
	hooksMu.Lock()
	toolAugmentHook = fn
	hooksMu.Unlock()
}

func SetToolResolve(fn ToolResolveFunc) {
	hooksMu.Lock()
	toolResolveHook = fn
	hooksMu.Unlock()
}

func SetPromptOverride(fn PromptOverrideFunc) {
	hooksMu.Lock()
	promptOverrideHook = fn
	hooksMu.Unlock()
}

func SetWrapupOverride(fn WrapupOverrideFunc) {
	hooksMu.Lock()
	wrapupOverrideHook = fn
	hooksMu.Unlock()
}

func SetWrapupMaxTurnsOverride(fn WrapupTurnsFunc) {
	hooksMu.Lock()
	wrapupMaxTurnsHook = fn
	hooksMu.Unlock()
}

func SetWrapupTaskTimeoutOverride(fn WrapupOverrideFunc) {
	hooksMu.Lock()
	wrapupTaskTimeoutHook = fn
	hooksMu.Unlock()
}

func SetWrapupTaskTimeoutTurnsOverride(fn WrapupTurnsFunc) {
	hooksMu.Lock()
	wrapupTaskTimeoutTurnsHook = fn
	hooksMu.Unlock()
}

func SetSkillIndexBlock(fn SkillIndexBlockFunc) {
	hooksMu.Lock()
	skillIndexBlockHook = fn
	hooksMu.Unlock()
}

func SetFindingTrafficBindingEnabled(fn FindingTrafficBindingEnabledFunc) {
	hooksMu.Lock()
	findingTrafficBindingHook = fn
	hooksMu.Unlock()
}

func SetListTaskArtifacts(fn ListTaskArtifactsFunc) {
	hooksMu.Lock()
	listTaskArtifactsHook = fn
	hooksMu.Unlock()
}

func SetFindingTaskID(fn FindingTaskIDFunc) {
	hooksMu.Lock()
	findingTaskIDHook = fn
	hooksMu.Unlock()
}

// Get* expose the currently installed hooks. Production reads go through the
// wrapper functions below; tests use the getters to save/restore a hook.
func GetToolAugment() ToolAugmentFunc {
	hooksMu.RLock()
	defer hooksMu.RUnlock()
	return toolAugmentHook
}

func GetToolResolve() ToolResolveFunc {
	hooksMu.RLock()
	defer hooksMu.RUnlock()
	return toolResolveHook
}

func GetPromptOverride() PromptOverrideFunc {
	hooksMu.RLock()
	defer hooksMu.RUnlock()
	return promptOverrideHook
}

func GetWrapupOverride() WrapupOverrideFunc {
	hooksMu.RLock()
	defer hooksMu.RUnlock()
	return wrapupOverrideHook
}

func GetWrapupMaxTurnsOverride() WrapupTurnsFunc {
	hooksMu.RLock()
	defer hooksMu.RUnlock()
	return wrapupMaxTurnsHook
}

func GetSkillIndexBlock() SkillIndexBlockFunc {
	hooksMu.RLock()
	defer hooksMu.RUnlock()
	return skillIndexBlockHook
}

func GetFindingTrafficBindingEnabled() FindingTrafficBindingEnabledFunc {
	hooksMu.RLock()
	defer hooksMu.RUnlock()
	return findingTrafficBindingHook
}

func GetListTaskArtifacts() ListTaskArtifactsFunc {
	hooksMu.RLock()
	defer hooksMu.RUnlock()
	return listTaskArtifactsHook
}

func GetFindingTaskID() FindingTaskIDFunc {
	hooksMu.RLock()
	defer hooksMu.RUnlock()
	return findingTaskIDHook
}

// ToolAugment applies the configured augmentation hook. With no hook installed
// it returns no extras, leaving the base tool set unchanged.
func ToolAugment(ctx context.Context, agentKey string) ([]actool.CoreTool, DeferredInfo, func()) {
	if fn := GetToolAugment(); fn != nil {
		return fn(ctx, agentKey)
	}
	return nil, DeferredInfo{}, nil
}

// ToolResolve applies the configured resolver. With no hook installed it returns
// the input unchanged.
func ToolResolve(ctx context.Context, agentKey string, tools []actool.CoreTool) []actool.CoreTool {
	if fn := GetToolResolve(); fn != nil {
		return fn(ctx, agentKey, tools)
	}
	return tools
}

// PromptOverride applies the configured prompt override. With no hook installed
// (or no stored template) it reports false and the built-in default is used.
func PromptOverride(agentKey string) (string, bool) {
	if fn := GetPromptOverride(); fn != nil {
		return fn(agentKey)
	}
	return "", false
}

func WrapupOverride(agentKey string) (string, bool) {
	if fn := GetWrapupOverride(); fn != nil {
		return fn(agentKey)
	}
	return "", false
}

func WrapupMaxTurnsOverride(agentKey string) (int, bool) {
	if fn := GetWrapupMaxTurnsOverride(); fn != nil {
		return fn(agentKey)
	}
	return 0, false
}

func WrapupTaskTimeoutOverride(agentKey string) (string, bool) {
	hooksMu.RLock()
	fn := wrapupTaskTimeoutHook
	hooksMu.RUnlock()
	if fn != nil {
		return fn(agentKey)
	}
	return "", false
}

func WrapupTaskTimeoutTurnsOverride(agentKey string) (int, bool) {
	hooksMu.RLock()
	fn := wrapupTaskTimeoutTurnsHook
	hooksMu.RUnlock()
	if fn != nil {
		return fn(agentKey)
	}
	return 0, false
}

// SkillIndexBlock returns the configured skill-index block, or "" when unset.
func SkillIndexBlock(agentKey string) string {
	if fn := GetSkillIndexBlock(); fn != nil {
		return fn(agentKey)
	}
	return ""
}

// FindingTrafficBindingEnabled reports whether the global auto-bind switch is
// on. Unset means off.
func FindingTrafficBindingEnabled() bool {
	if fn := GetFindingTrafficBindingEnabled(); fn != nil {
		return fn()
	}
	return false
}
