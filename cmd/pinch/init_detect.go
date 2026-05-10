package main

import (
	"strings"
)

// #243 — detect a hand-rolled pincher policy section in an existing
// markdown/text file so `pincher init` can canonicalize it instead of
// appending a duplicate block. The detector is conservative: it only
// fires on heading-bounded blocks (a markdown heading containing the
// word "pincher", case-insensitive). Loose `mcp__pincher__` mentions
// in code examples or top-level prose are left alone — wrapping them
// would risk accidentally clobbering content the user meant to keep.
//
// When the detector returns ok=true, the caller wraps the [start,end)
// byte range with the standard markers in-memory and runs the normal
// replace-between-markers path. The user's hand-rolled section gets
// canonicalized to the embedded policy.md.

// hasInitMarkers reports whether the file already contains a paired
// set of pincher init markers. When true, the detector should NOT
// fire — markers take precedence and the replace path handles them.
func hasInitMarkers(content string) bool {
	startIdx := strings.Index(content, pincherInitMarkerStart)
	endIdx := strings.Index(content, pincherInitMarkerEnd)
	return startIdx >= 0 && endIdx > startIdx
}

// detectPincherPolicySection scans content for a markdown heading
// whose text contains "pincher" (case-insensitive). When found,
// returns the byte range [start, end) covering that heading's
// section — from the heading line through the line BEFORE the next
// markdown heading at the same level OR EOF. The returned range
// excludes the trailing newline so the caller can splice markers
// without doubling up blank lines.
//
// Returns ok=false when no qualifying heading is found. Multi-match:
// the FIRST qualifying heading wins; subsequent ones are left alone
// (and would surface as duplicate content after the canonical block,
// which is a recoverable state — the user can delete them manually).
func detectPincherPolicySection(content string) (start, end int, ok bool) {
	lines := strings.SplitAfter(content, "\n")
	pos := 0
	headingStart := -1
	headingLevel := 0
	for _, line := range lines {
		level, isHeading := markdownHeadingLevel(line)
		if isHeading {
			if headingStart >= 0 {
				// We were inside a pincher section; this new heading at
				// the same or higher level closes it. Return the range.
				if level <= headingLevel {
					return headingStart, pos, true
				}
				// Deeper heading inside the section — still inside.
			} else if level <= 6 && lineMentionsPincher(line) {
				// Start of a pincher-headed section.
				headingStart = pos
				headingLevel = level
			}
		}
		pos += len(line)
	}
	// EOF closes an open section.
	if headingStart >= 0 {
		return headingStart, pos, true
	}
	return 0, 0, false
}

// markdownHeadingLevel returns (level, true) when line is a markdown
// ATX heading (`#`, `##`, etc., up to 6). Setext headings (=== / ---
// underline style) are not detected — they're rare in the kind of
// rules files pincher init writes to. Trailing whitespace allowed
// before the `#` per CommonMark; we don't normalize.
func markdownHeadingLevel(line string) (int, bool) {
	trimmed := strings.TrimLeft(line, " ")
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, false
	}
	// Must be followed by a space (CommonMark) — `#hashtag` is not a heading.
	if level == len(trimmed) || trimmed[level] != ' ' {
		return 0, false
	}
	return level, true
}

// lineMentionsPincher reports whether line contains the substring
// "pincher" case-insensitively. Used by detectPincherPolicySection to
// decide which heading qualifies. Deliberately broad — "Pincher
// Usage Policy", "Pincher MCP", and even "pincher tools" all match.
// Sub-string match means accents/punctuation in the heading don't
// trip it up.
func lineMentionsPincher(line string) bool {
	return strings.Contains(strings.ToLower(line), "pincher")
}
