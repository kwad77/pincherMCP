package ast

import (
	"path/filepath"
	"strings"
)

// ShouldSkip reports whether the indexer should refuse to extract symbols
// from this file even when DetectLanguage / IsSourceFile would otherwise
// recognise it. Returns (true, reason) for blocked files, (false, "")
// otherwise.
//
// The blocklist exists to prevent two classes of indexing waste:
//
//  1. Lockfiles parsed as JSON or YAML — package-lock.json, pnpm-lock.yaml,
//     composer.lock, etc. would otherwise produce thousands of low-signal
//     Setting symbols per file and dominate the symbol store with noise.
//
//  2. Minified bundles and source maps — *.min.js, *.bundle.js, *.map files
//     would otherwise be parsed by language extractors that aren't designed
//     for single-line megabyte-scale output.
//
// All matching is filename-based (basename, lowercase). No content sniffing.
// This keeps the check O(1) per file and avoids reading any file we plan
// to skip.
func ShouldSkip(path string) (bool, string) {
	base := strings.ToLower(filepath.Base(path))
	if reason, ok := lockfileNames[base]; ok {
		return true, reason
	}
	if isMinified(base) {
		return true, "minified bundle"
	}
	if strings.HasSuffix(base, ".map") {
		return true, "source map"
	}
	return false, ""
}

// lockfileNames maps the lowercased basename of well-known lockfiles and
// checksum files to a human-readable reason. Matching is exact basename;
// a lockfile inside a subdirectory still matches.
var lockfileNames = map[string]string{
	// JavaScript / TypeScript ecosystem
	"package-lock.json":   "npm lockfile",
	"npm-shrinkwrap.json": "npm shrinkwrap",
	"yarn.lock":           "yarn lockfile",
	"pnpm-lock.yaml":      "pnpm lockfile",
	"bun.lockb":           "bun lockfile",
	"bun.lock":            "bun lockfile",

	// Rust
	"cargo.lock": "cargo lockfile",

	// PHP
	"composer.lock": "composer lockfile",

	// Ruby
	"gemfile.lock": "gemfile lockfile",

	// Python
	"pipfile.lock": "pipfile lockfile",
	"poetry.lock":  "poetry lockfile",
	"uv.lock":      "uv lockfile",
	"pdm.lock":     "pdm lockfile",

	// Elixir
	"mix.lock": "mix lockfile",

	// Dart / Flutter
	"pubspec.lock": "pubspec lockfile",

	// CocoaPods / Swift
	"podfile.lock":      "cocoapods lockfile",
	"cartfile.resolved": "carthage lockfile",
	"package.resolved":  "swift pm lockfile",

	// Nix
	"flake.lock": "nix flake lockfile",

	// Go (checksum file, not strictly a lockfile but same intent)
	"go.sum": "go.sum checksum file",
}

// minifiedSuffixes lists the suffixes that mark a file as auto-generated /
// minified output we don't want symbol-extracted. CSS is included even
// though pincher doesn't currently extract CSS — keeps the rule honest if
// CSS extraction is ever added.
var minifiedSuffixes = []string{
	".min.js",
	".min.mjs",
	".min.cjs",
	".min.jsx",
	".min.ts",
	".min.tsx",
	".min.css",
}

func isMinified(lowerBase string) bool {
	for _, s := range minifiedSuffixes {
		if strings.HasSuffix(lowerBase, s) {
			return true
		}
	}
	return false
}
