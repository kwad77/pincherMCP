package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// initTarget describes one of the editor / agent rule files that
// `pincher init` knows how to seed. Each target writes the policy block
// in its expected location and format; the marker-block convention
// (`<!-- pincher:start --> ... <!-- pincher:end -->`) is shared across
// every target so re-runs replace in place rather than duplicating.
//
// Closes #191.
type initTarget struct {
	// name is the value the user passes to --target (e.g. "cursor").
	name string

	// describe is the one-line summary shown in --help output and the
	// post-write banner.
	describe string

	// supportsGlobal reports whether `--global` is meaningful for this
	// target. claude maps to ~/.claude/CLAUDE.md when global; cursor /
	// windsurf / aider have no equivalent global rules file (the files
	// live per-project), so passing --global with those is an error.
	// continue is global-only — the file always lives at
	// ~/.continue/config.json regardless of cwd.
	supportsGlobal bool

	// alwaysGlobal is true for targets where --global is implied (the
	// rules file is global by design — currently just continue).
	alwaysGlobal bool

	// pathFn resolves the absolute file path. global is the user's
	// --global value; honored only when supportsGlobal && !alwaysGlobal.
	// cwd is the project root the caller wants paths resolved against —
	// CLI passes os.Getwd(); MCP (#244) passes the session project root
	// so the server's own working directory doesn't influence target
	// paths. Targets ignoring cwd (the alwaysGlobal `continue` target,
	// for example) accept it as a no-op for signature uniformity.
	pathFn func(cwd string, global bool) (string, error)

	// detectFn returns true when a marker file or directory for this
	// editor exists under cwd. Used by --target=detect.
	detectFn func(cwd string) bool

	// writeFn produces the new file content given the existing content
	// (may be empty if file doesn't exist) and the raw policy markdown
	// embedded in the binary. Returns (newContent, action) where action
	// is "wrote" / "updated" / "appended" — same vocabulary as
	// claude's mergePolicyBlock so the post-write banner stays uniform
	// across targets.
	writeFn func(existing, policy string) (string, string)
}

// allInitTargets is the registry of every editor / agent target the
// init subcommand can write to. Order is meaningful for --target=all
// and detection-priority output ordering.
var allInitTargets = []initTarget{
	claudeInitTarget,
	cursorInitTarget,
	cursorLegacyInitTarget,
	windsurfInitTarget,
	aiderInitTarget,
	continueInitTarget,
}

// findInitTarget looks up a target by its --target value.
func findInitTarget(name string) (initTarget, bool) {
	for _, t := range allInitTargets {
		if t.name == name {
			return t, true
		}
	}
	return initTarget{}, false
}

// initTargetNames returns the list of valid --target values for help
// text. Order matches allInitTargets.
func initTargetNames() []string {
	out := make([]string, 0, len(allInitTargets)+2)
	for _, t := range allInitTargets {
		out = append(out, t.name)
	}
	out = append(out, "detect", "all")
	return out
}

// detectInitTargets walks cwd and returns every target whose detectFn
// returns true. If none match, returns just claude (the safe default).
func detectInitTargets(cwd string) []initTarget {
	var hits []initTarget
	for _, t := range allInitTargets {
		if t.detectFn != nil && t.detectFn(cwd) {
			hits = append(hits, t)
		}
	}
	if len(hits) == 0 {
		hits = append(hits, claudeInitTarget)
	}
	return hits
}

// ─────────────────────────────────────────────────────────────────────────────
// claude — the original target (./CLAUDE.md, ~/.claude/CLAUDE.md global)
// ─────────────────────────────────────────────────────────────────────────────

var claudeInitTarget = initTarget{
	name:           "claude",
	describe:       "Claude Code: ./CLAUDE.md (or ~/.claude/CLAUDE.md with --global)",
	supportsGlobal: true,
	pathFn:         resolveCLAUDEPath,
	detectFn: func(cwd string) bool {
		_, err := os.Stat(filepath.Join(cwd, "CLAUDE.md"))
		return err == nil
	},
	writeFn: mergePolicyBlock,
}

// ─────────────────────────────────────────────────────────────────────────────
// cursor (modern, .mdc with YAML frontmatter under .cursor/rules/)
// ─────────────────────────────────────────────────────────────────────────────

const cursorRuleFrontmatter = `---
description: pincher MCP code-intelligence usage policy
globs:
  - "**/*"
alwaysApply: true
---

`

var cursorInitTarget = initTarget{
	name:     "cursor",
	describe: "Cursor (modern): ./.cursor/rules/pincher.mdc with YAML frontmatter",
	pathFn: func(cwd string, global bool) (string, error) {
		if global {
			return "", fmt.Errorf("cursor target has no global variant; rules live per-project")
		}
		return filepath.Join(cwd, ".cursor", "rules", "pincher.mdc"), nil
	},
	detectFn: func(cwd string) bool {
		_, err := os.Stat(filepath.Join(cwd, ".cursor"))
		return err == nil
	},
	writeFn: cursorMDCWriter,
}

// cursorMDCWriter wraps the policy in MDX YAML frontmatter on first
// write. On subsequent writes (existing file present), it preserves
// any frontmatter the user has customised and only replaces the
// marker block in the body. This means tweaking `globs:` or
// `alwaysApply:` in the frontmatter survives `pincher init` re-runs.
func cursorMDCWriter(existing, policy string) (string, string) {
	if existing == "" {
		body, _ := mergePolicyBlockBare("", policy)
		return cursorRuleFrontmatter + body, "wrote"
	}
	// Existing file: preserve everything before the body (frontmatter +
	// any prose the user added above pincher's block), and run the
	// usual marker merge over the body.
	frontmatter, body := splitMDXFrontmatter(existing)
	mergedBody, action := mergePolicyBlockBare(body, policy)
	return frontmatter + mergedBody, action
}

// splitMDXFrontmatter returns (frontmatterIncludingTrailingBlank, body).
// If no frontmatter delimiter is found, returns ("", content).
func splitMDXFrontmatter(content string) (string, string) {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return "", content
	}
	// Find the closing `---` line. Search after the opening delimiter.
	rest := content[4:]
	if strings.HasPrefix(content, "---\r\n") {
		rest = content[5:]
	}
	closeIdx := strings.Index(rest, "\n---\n")
	if closeIdx < 0 {
		closeIdx = strings.Index(rest, "\n---\r\n")
		if closeIdx < 0 {
			// Malformed frontmatter (no close); treat whole file as body.
			return "", content
		}
	}
	// Compute split point in the original string. Include the closing
	// `---` line and the blank line following it (which conventionally
	// separates frontmatter from body).
	end := len(content) - len(rest) + closeIdx + len("\n---\n")
	if end > len(content) {
		end = len(content)
	}
	// Skip a single trailing blank line if present.
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return content[:end], content[end:]
}

// ─────────────────────────────────────────────────────────────────────────────
// cursor-legacy (./.cursorrules, plain text)
// ─────────────────────────────────────────────────────────────────────────────

var cursorLegacyInitTarget = initTarget{
	name:     "cursor-legacy",
	describe: "Cursor (legacy): ./.cursorrules plain text",
	pathFn: func(cwd string, global bool) (string, error) {
		if global {
			return "", fmt.Errorf("cursor-legacy target has no global variant")
		}
		return filepath.Join(cwd, ".cursorrules"), nil
	},
	detectFn: func(cwd string) bool {
		_, err := os.Stat(filepath.Join(cwd, ".cursorrules"))
		return err == nil
	},
	writeFn: mergePolicyBlockBare,
}

// ─────────────────────────────────────────────────────────────────────────────
// windsurf (./.windsurfrules, plain text/markdown)
// ─────────────────────────────────────────────────────────────────────────────

var windsurfInitTarget = initTarget{
	name:     "windsurf",
	describe: "Windsurf: ./.windsurfrules plain text/markdown",
	pathFn: func(cwd string, global bool) (string, error) {
		if global {
			return "", fmt.Errorf("windsurf target has no global variant")
		}
		return filepath.Join(cwd, ".windsurfrules"), nil
	},
	detectFn: func(cwd string) bool {
		_, err := os.Stat(filepath.Join(cwd, ".windsurfrules"))
		return err == nil
	},
	writeFn: mergePolicyBlockBare,
}

// ─────────────────────────────────────────────────────────────────────────────
// aider (./CONVENTIONS.md, the documented Aider convention)
// ─────────────────────────────────────────────────────────────────────────────

var aiderInitTarget = initTarget{
	name:     "aider",
	describe: "Aider: ./CONVENTIONS.md (Aider's documented convention)",
	pathFn: func(cwd string, global bool) (string, error) {
		if global {
			return "", fmt.Errorf("aider --global needs ~/.aider.conf.yml work — not yet implemented; use project CONVENTIONS.md")
		}
		return filepath.Join(cwd, "CONVENTIONS.md"), nil
	},
	detectFn: func(cwd string) bool {
		// Heuristic: CONVENTIONS.md exists, OR an aider config marker.
		if _, err := os.Stat(filepath.Join(cwd, "CONVENTIONS.md")); err == nil {
			return true
		}
		if _, err := os.Stat(filepath.Join(cwd, ".aider.conf.yml")); err == nil {
			return true
		}
		return false
	},
	writeFn: mergePolicyBlockBare,
}

// ─────────────────────────────────────────────────────────────────────────────
// continue (~/.continue/config.json, JSON-string merge into systemMessage)
// ─────────────────────────────────────────────────────────────────────────────

var continueInitTarget = initTarget{
	name:         "continue",
	describe:     "Continue.dev: ~/.continue/config.json (merges into systemMessage)",
	alwaysGlobal: true,
	pathFn: func(cwd string, global bool) (string, error) {
		// Always global — passing --global is a no-op (and not erroneous).
		// cwd is ignored; the config lives in the user's home directory.
		_ = cwd
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("user home dir: %w", err)
		}
		return filepath.Join(home, ".continue", "config.json"), nil
	},
	detectFn: func(cwd string) bool {
		// cwd doesn't determine continue presence; this is global. We
		// detect via the home directory instead.
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		_, err = os.Stat(filepath.Join(home, ".continue"))
		return err == nil
	},
	writeFn: continueJSONWriter,
}

// continueJSONWriter merges the policy into the `systemMessage` field
// of a Continue config.json. Markers are line-prefixed with `// ` so
// the same scan-and-replace pattern works inside a JSON-escaped string.
//
// Behaviour:
//   - Empty file → emit a minimal config with just `systemMessage`.
//   - Existing JSON with no systemMessage → add the field with the block.
//   - Existing systemMessage → replace the marker block in place; if no
//     markers, append a separator + block.
//   - Malformed JSON → error (caller surfaces it; we don't risk
//     corrupting a user-edited config).
func continueJSONWriter(existing, policy string) (string, string) {
	const (
		startMark = "// pincher:start"
		endMark   = "// pincher:end"
	)
	header := "// Managed by `pincher init --target=continue`. Edit `pincher init` to change this block.\n"
	block := startMark + "\n" + header + strings.TrimRight(policy, "\n") + "\n" + endMark

	mergeMessage := func(prev string) (string, string) {
		if prev == "" {
			return block, "wrote"
		}
		startIdx := strings.Index(prev, startMark)
		endIdx := strings.Index(prev, endMark)
		if startIdx >= 0 && endIdx > startIdx {
			afterIdx := endIdx + len(endMark)
			before := strings.TrimRight(prev[:startIdx], "\n")
			after := strings.TrimLeft(prev[afterIdx:], "\n")
			merged := block
			if before != "" {
				merged = before + "\n\n" + merged
			}
			if after != "" {
				merged = merged + "\n\n" + after
			}
			return merged, "updated"
		}
		return strings.TrimRight(prev, "\n") + "\n\n" + block, "appended"
	}

	if existing == "" {
		// Fresh write: minimal config with just the systemMessage.
		msg, action := mergeMessage("")
		raw, _ := json.MarshalIndent(map[string]any{"systemMessage": msg}, "", "  ")
		return string(raw) + "\n", action
	}

	// Decode existing into a generic map so we can preserve unknown
	// keys (Continue's config has many fields we don't touch).
	var cfg map[string]any
	if err := json.Unmarshal([]byte(existing), &cfg); err != nil {
		// Caller catches this via a follow-up json.Unmarshal — but
		// since we're called inline, we surface it by returning the
		// existing content unchanged with an "error" action. The
		// runInitCLI loop checks `action` to detect this.
		return existing, "error"
	}
	prev, _ := cfg["systemMessage"].(string)
	merged, action := mergeMessage(prev)
	cfg["systemMessage"] = merged

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return existing, "error"
	}
	return string(out) + "\n", action
}
