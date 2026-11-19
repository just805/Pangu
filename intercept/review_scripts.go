package intercept

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// issue #21.2: script body visibility for the fallback judge.
//
// A tool command that references a local script file previously showed the
// judge only the filename — `python3 shared/scripts/exp.py` could hide any
// behavior inside a file the review layer never saw, and the judge's only
// honest answer was "content invisible". ReviewInput v5 attaches a bounded,
// read-only snapshot of each referenced script's body so the judge sees what
// will actually run. Paths that cannot be resolved deterministically (shell
// variables, globs, no working-directory anchor) are reported in
// unresolved_scripts and judged under the existing content-invisible rule.

const (
	scriptMaxFiles   = 4         // at most this many snapshots per call
	scriptMaxFile    = 32 << 10  // per-file snapshot cap
	scriptMaxTotal   = 128 << 10 // combined snapshot cap per call
	scriptMaxUnres   = 16        // unresolved path list cap
	scriptScanStrings = 64       // how many JSON string values to scan
	scriptScanString = 8 << 10   // max length of one scanned string value
)

// scriptExtensions marks a token as a script file candidate regardless of how
// it is invoked (./run.sh, xargs -a jobs.pl, a bare path argument ...).
var scriptExtensions = map[string]bool{
	".py": true, ".pyw": true, ".sh": true, ".bash": true, ".zsh": true,
	".js": true, ".mjs": true, ".cjs": true, ".ts": true,
	".pl": true, ".rb": true, ".php": true, ".ps1": true, ".psm1": true,
	".lua": true, ".awk": true, ".tcl": true, ".r": true, ".jl": true,
}

// interpreterTokens are command words whose first non-option argument is
// conventionally a script path — covering extensionless scripts.
var interpreterTokens = map[string]bool{
	"python": true, "python2": true, "python3": true, "pypy": true, "pypy3": true,
	"bash": true, "sh": true, "ash": true, "dash": true, "zsh": true, "ksh": true,
	"node": true, "nodejs": true, "deno": true, "bun": true,
	"perl": true, "ruby": true, "php": true, "lua": true,
	"pwsh": true, "powershell": true, "rscript": true, "julia": true,
}

// interpreterExecFlags take a command string as their value, so the token
// after them is NOT a script path (bash -c '…', python -c '…', node -e '…').
var interpreterExecFlags = map[string]bool{
	"-c": true, "-e": true, "-x": true, "-m": true,
	"--command": true, "--eval": true, "--exec": true,
}

// scriptMeta marks a path that depends on shell state and cannot be resolved
// deterministically at review time.
func scriptMeta(p string) bool {
	return strings.ContainsAny(p, "$`*?{}[]<>~|&;()")
}

// jsonStrings walks the decoded arguments and returns the string values to
// scan (command lines usually live in one of them). Bounded: scanning is a
// PreToolUse hook on every tool call, it must stay cheap.
func jsonStrings(raw json.RawMessage) []string {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return nil
	}
	out := make([]string, 0, 4)
	var walk func(x any)
	walk = func(x any) {
		if len(out) >= scriptScanStrings {
			return
		}
		switch t := x.(type) {
		case string:
			if t != "" && len(t) <= scriptScanString {
				out = append(out, t)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return out
}

// splitTokens splits one string value into shell-ish tokens: whitespace
// separates, single/double quotes group and are stripped. Best effort — this
// feeds a heuristic detector, not a parser.
func splitTokens(s string) []string {
	var toks []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return toks
}

// scriptCandidates picks script path tokens out of one command line.
func scriptCandidates(toks []string) []string {
	return scriptCandidatesDepth(toks, 0)
}

// maxScriptRecursion bounds nested command strings (bash -c 'python3 -c "…"').
const maxScriptRecursion = 3

func scriptCandidatesDepth(toks []string, depth int) []string {
	var out []string
	expectScript := false
	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		lower := strings.ToLower(tok)
		if expectScript && !strings.HasPrefix(tok, "-") && tok != "" {
			out = append(out, tok)
			expectScript = false
			continue
		}
		expectScript = false
		if interpreterTokens[lower] && !strings.Contains(tok, "/") {
			expectScript = true // next non-option token is the script path
			continue
		}
		if interpreterExecFlags[lower] {
			// Its value is a nested command string — scan it too
			// (bash -c 'chmod +x ./pwn.sh && ./pwn.sh').
			if depth < maxScriptRecursion && i+1 < len(toks) {
				out = append(out, scriptCandidatesDepth(splitTokens(toks[i+1]), depth+1)...)
				i++
			}
			continue
		}
		if ext := filepath.Ext(lower); scriptExtensions[ext] {
			out = append(out, tok)
		}
	}
	return out
}

// resolveScriptPath anchors a raw token against the run's working directory.
// ok=false means "cannot resolve deterministically" → unresolved_scripts.
func resolveScriptPath(workDir, raw string) (string, bool) {
	if scriptMeta(raw) {
		return "", false
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), true
	}
	if workDir == "" {
		return "", false // relative path with no anchor: which cwd is it?
	}
	return filepath.Clean(filepath.Join(workDir, raw)), true
}

// snapshotScript reads a bounded snapshot of a regular file. Non-regular
// entries (devices, FIFOs, directories) are filtered BEFORE open — os.Open on
// a FIFO blocks until a writer appears and would hang PreToolUse.
func snapshotScript(path string, budget *int) (ReviewScript, bool) {
	if *budget <= 0 {
		return ReviewScript{}, false
	}
	// Stat (follows symlinks) does not open, so it cannot block.
	st, err := os.Stat(path)
	if err != nil {
		return ReviewScript{}, false // not there yet — judge stays on "invisible"
	}
	if !st.Mode().IsRegular() {
		return ReviewScript{}, false // directory / device / pipe: never read
	}
	limit := scriptMaxFile
	if *budget < limit {
		limit = *budget
	}
	f, err := os.Open(path)
	if err != nil {
		return ReviewScript{}, false
	}
	defer f.Close()
	// Re-check regularity on the open handle (cheap TOCTOU guard).
	if st2, err := f.Stat(); err != nil || !st2.Mode().IsRegular() {
		return ReviewScript{}, false
	}
	buf := make([]byte, limit+1)
	n, _ := io.ReadFull(f, buf)
	content := buf[:min(n, limit)]
	truncated := n > limit
	// Drop a trailing partial rune (snapshot cut mid-character).
	for len(content) > 0 {
		r, size := utf8.DecodeLastRune(content)
		if r == utf8.RuneError && size == 1 {
			content = content[:len(content)-1]
			continue
		}
		break
	}
	out := ReviewScript{Path: path, Content: string(content), Truncated: truncated}
	*budget -= len(out.Content)
	return out, true
}

// collectReviewScripts extracts script references from the tool arguments and
// reads snapshots within the configured limits. Everything not snapshotted —
// dynamic paths, unanchored relative paths, over-limit entries, unreadable or
// non-regular files — lands in unresolved_scripts so the judge handles it
// under the conservative content-invisible rule.
func collectReviewScripts(workDir string, arguments json.RawMessage) ([]ReviewScript, []string) {
	var candidates []string
	seenCand := map[string]bool{}
	for _, s := range jsonStrings(arguments) {
		for _, raw := range scriptCandidates(splitTokens(s)) {
			if !seenCand[raw] {
				seenCand[raw] = true
				candidates = append(candidates, raw)
			}
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	var scripts []ReviewScript
	var unresolved []string
	budget := scriptMaxTotal
	seenPath := map[string]bool{}
	for _, raw := range candidates {
		resolved, ok := resolveScriptPath(workDir, raw)
		if !ok || seenPath[resolved] {
			if !ok && len(unresolved) < scriptMaxUnres {
				unresolved = append(unresolved, raw)
			}
			continue
		}
		if len(scripts) >= scriptMaxFiles {
			if len(unresolved) < scriptMaxUnres {
				unresolved = append(unresolved, raw)
			}
			continue
		}
		sn, ok := snapshotScript(resolved, &budget)
		if !ok {
			if len(unresolved) < scriptMaxUnres {
				unresolved = append(unresolved, raw)
			}
			continue
		}
		seenPath[resolved] = true
		scripts = append(scripts, sn)
	}
	return scripts, unresolved
}
