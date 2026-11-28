package agent

import (
	"context"
	"sync"

	actool "github.com/Autumn-27/norma/tool"
)

// The server wires three process-wide hooks at startup: tool augmentation
// (skills/MCP), the DB-backed tool resolver, and stored prompt templates.
// Tests swap them to exercise wiring without a full Manager.
//
// They used to be plain package variables. That raced whenever a test restored
// a hook while another test's planner goroutine was still assembling tools
// (`go test -race ./server` found it in TestRetest* cleanup vs plannerLoop).
// Hooks now go through a RWMutex: reads on the hot path take a read lock, writes
// take the write lock, and callers can no longer assign the variables directly.

// ToolAugmentFunc returns the extra tools an agent should see beyond its base
// set (visible skills + MCP servers) plus the deferred-tool wiring.
type ToolAugmentFunc func(ctx context.Context, agentKey string) (extra []actool.CoreTool, def DeferredInfo, cleanup func())

// ToolResolveFunc post-processes an agent's assembled tool list against the DB
// tools table (bindings, enabled flags, description/schema overrides).
type ToolResolveFunc func(ctx context.Context, agentKey string, tools []actool.CoreTool) []actool.CoreTool

// PromptOverrideFunc returns the stored system-prompt template for an agent.
type PromptOverrideFunc func(agentKey string) (string, bool)

var (
	hooksMu            sync.RWMutex
	toolAugmentHook    ToolAugmentFunc
	toolResolveHook    ToolResolveFunc
	promptOverrideHook PromptOverrideFunc
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
