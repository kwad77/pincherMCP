// Package init carves the pure init-policy machinery out of cmd/pinch
// so the MCP server can register an `init` tool without dragging the
// whole `main` package along (#253). The CLI orchestration layer
// (flag parsing, stdout formatting, post-write banner) still lives in
// cmd/pinch/init.go and imports this package as `pinit` to avoid
// shadowing the language's per-package init() function name.
//
// The split:
//   - This package:   pure target plan + merge + per-target writers + detection
//   - cmd/pinch:      runInitCLI, runInitTarget orchestration, printNextSteps
//
// Both share PolicyMarkdown via go:embed; the file lives in this
// package's directory so the MCP path doesn't depend on where the
// caller's binary embed walked from.
package init

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed policy.md
var PolicyMarkdown string

const (
	// MarkerStart and MarkerEnd bracket the pincher-managed section
	// of CLAUDE.md so re-running `pincher init` (CLI or MCP) replaces
	// the block in place rather than duplicating content. They're
	// HTML comments so they render as nothing in Markdown viewers but
	// are trivially round-trippable via simple string scanning.
	MarkerStart = "<!-- pincher:start -->"
	MarkerEnd   = "<!-- pincher:end -->"

	// BlockHeader is the human-readable preface that appears inside
	// the marker block. It explains where the content came from so a
	// reader who lands on CLAUDE.md without context still understands.
	BlockHeader = "<!-- Managed by `pincher init`. Edit `pincher init` to change this block,\n     or delete the markers below to opt out of future updates. -->\n\n"
)

// ErrEmptyPolicy surfaces if the embedded policy.md is empty at
// runtime — only possible if the file was zeroed out at distribution
// time. Validated at package init() so a build-time mistake fails
// loudly instead of writing empty pincher blocks to every user's
// CLAUDE.md.
var ErrEmptyPolicy = errors.New("embedded pincher policy is empty")

// TargetPlan is the pure result of resolving a target against an
// existing file: where to write, what's there, what the merge would
// produce, and which action would be taken. CLI and MCP consume the
// same plan; the disk write is a separate step (RunTarget for CLI;
// the MCP handler honours its own write=true / dry-run gate).
//
// Action vocabulary: "wrote" (fresh file), "updated" (replaced a
// pre-existing pincher block), "appended" (no existing block; new
// content tacked on), "error" (current only path: malformed JSON in
// the continue target).
type TargetPlan struct {
	Target   string
	Path     string
	Existing string
	Updated  string
	Action   string
	BytesIn  int
	BytesOut int
}

// Plan resolves a target into a TargetPlan without touching the
// filesystem (apart from reading the existing file, if any). cwd is
// the project root paths resolve relative to; CLI passes os.Getwd(),
// MCP passes the session project root.
func Plan(t Target, cwd string, global bool) (TargetPlan, error) {
	useGlobal := global
	if t.AlwaysGlobal {
		useGlobal = true
	} else if !t.SupportsGlobal {
		useGlobal = false
	}

	path, err := t.PathFn(cwd, useGlobal)
	if err != nil {
		return TargetPlan{}, fmt.Errorf("[%s] %w", t.Name, err)
	}

	existing := ReadFileIfExists(path)
	updated, action := t.WriteFn(existing, PolicyMarkdown)
	if action == "error" {
		return TargetPlan{}, fmt.Errorf("[%s] cannot merge into %s: file exists but is not valid for this target (malformed JSON?)", t.Name, path)
	}

	return TargetPlan{
		Target:   t.Name,
		Path:     path,
		Existing: existing,
		Updated:  updated,
		Action:   action,
		BytesIn:  len(existing),
		BytesOut: len(updated),
	}, nil
}

// ResolveCLAUDEPath returns the absolute path to the CLAUDE.md that
// `pincher init` should write to. cwd is the project root; the CLI
// passes os.Getwd() and the MCP path passes the session project root
// so the server's own working directory doesn't influence the output.
func ResolveCLAUDEPath(cwd string, global bool) (string, error) {
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("user home dir: %w", err)
		}
		return filepath.Join(home, ".claude", "CLAUDE.md"), nil
	}
	return filepath.Join(cwd, "CLAUDE.md"), nil
}

func ReadFileIfExists(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func WriteFileEnsuringDir(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// MergePolicyBlock inserts or replaces the pincher policy block in
// existing. Returns (updated, action). Behavior:
//
//   - existing is empty → emit a complete CLAUDE.md (h1 header + block)
//   - existing has both markers → replace content between them
//   - existing has neither marker → append block at end with a leading blank line
//   - existing has malformed markers (only start, only end) → append a
//     fresh block; we don't attempt automatic recovery because the cause
//     is more often "user edited the markers" than "tool corrupted them"
func MergePolicyBlock(existing, policy string) (string, string) {
	if existing == "" {
		header := "# CLAUDE.md\n\nThis file provides guidance to Claude Code (claude.ai/code) when working with this project.\n\n"
		bare, _ := MergePolicyBlockBare(existing, policy)
		return header + bare, "wrote"
	}
	return MergePolicyBlockBare(existing, policy)
}

// MergePolicyBlockBare is the merge primitive used by non-Claude
// targets (cursor, windsurf, aider, etc.) where the file's purpose is
// the rules block itself — adding a `# CLAUDE.md` header would be
// misleading. On fresh writes it emits just the marker block;
// otherwise it behaves identically to MergePolicyBlock.
func MergePolicyBlockBare(existing, policy string) (string, string) {
	block := BuildPolicyBlock(policy)

	if existing == "" {
		return block + "\n", "wrote"
	}

	// #243: when no markers exist but the file already contains a
	// hand-rolled pincher policy section (heading-bounded), wrap the
	// detected block with markers in-memory so the replace path below
	// canonicalizes it instead of appending a duplicate. The detector
	// is conservative — it only fires on heading-bounded blocks; loose
	// mcp__pincher__ mentions in code examples are left alone.
	if !HasMarkers(existing) {
		if hStart, hEnd, ok := DetectPincherPolicySection(existing); ok {
			existing = existing[:hStart] + MarkerStart + "\n" + existing[hStart:hEnd] + MarkerEnd + existing[hEnd:]
		}
	}

	startIdx := strings.Index(existing, MarkerStart)
	endIdx := strings.Index(existing, MarkerEnd)
	if startIdx >= 0 && endIdx > startIdx {
		before := existing[:startIdx]
		afterIdx := endIdx + len(MarkerEnd)
		after := existing[afterIdx:]
		before = strings.TrimRight(before, "\n")
		after = strings.TrimLeft(after, "\n")

		var b strings.Builder
		b.WriteString(before)
		if before != "" {
			b.WriteString("\n\n")
		}
		b.WriteString(block)
		if after != "" {
			b.WriteString("\n\n")
			b.WriteString(after)
		} else {
			b.WriteString("\n")
		}
		return b.String(), "updated"
	}

	trimmed := strings.TrimRight(existing, "\n")
	return trimmed + "\n\n" + block + "\n", "appended"
}

// BuildPolicyBlock wraps policy in the start/end markers plus the
// "managed by pincher init" header comment.
func BuildPolicyBlock(policy string) string {
	var b strings.Builder
	b.WriteString(MarkerStart)
	b.WriteString("\n")
	b.WriteString(BlockHeader)
	b.WriteString(strings.TrimRight(policy, "\n"))
	b.WriteString("\n\n")
	b.WriteString(MarkerEnd)
	return b.String()
}

// init validates the embed at package init so a build-time mistake
// (empty policy.md, missing file) surfaces immediately rather than at
// first call. The policy is embedded via go:embed; missing files
// would fail at compile time, but an accidentally emptied file would
// compile and fail at runtime — this gate keeps the failure adjacent
// to the binary's startup.
func init() {
	if bytes.TrimSpace([]byte(PolicyMarkdown)) == nil {
		panic(ErrEmptyPolicy)
	}
}
