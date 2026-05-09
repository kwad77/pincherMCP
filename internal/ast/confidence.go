package ast

import (
	"path/filepath"
	"regexp"
	"strings"
)

// PROTOTYPE — perf evaluation only (#34 Phase 1+2 sketch).
// Not on a release path. Numbers from this file feed the design review's
// performance question; behavior here is illustrative, not final.

// Signals carries the per-symbol score components. Each field is a pure
// function of (extractor output, file path, kind, source). Compose() reduces
// them to a single confidence score in [0, 1].
type Signals struct {
	BaseExtractor  float64
	KindBaseline   float64
	PathPenalty    float64
	BreadthPenalty float64 // not wired in this prototype (needs parent-fanout pass)
	LeafPenalty    float64 // not wired in this prototype (needs structural info)
	IdentBonus     float64
	GeneratedPen   float64
}

func (s Signals) Compose() float64 {
	base := (s.BaseExtractor + s.KindBaseline) / 2.0
	score := base + s.PathPenalty + s.BreadthPenalty + s.LeafPenalty +
		s.IdentBonus + s.GeneratedPen
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

var kindBaseline = map[string]float64{
	"Function":    1.00,
	"Method":      1.00,
	"Class":       1.00,
	"Interface":   1.00,
	"Type":        1.00,
	"Enum":        1.00,
	"Module":      0.95,
	"Variable":    0.95,
	"Setting":     0.95,
	"Resource":    0.95,
	"Block":       0.85,
	"Section":     0.80,
	"Heading":     0.80,
	"Document":    0.70,
	"CodeSnippet": 0.70,
}

type pathPattern struct {
	Glob    string // matched against filepath.Base(relPath) OR full relPath
	Penalty float64
	IsDir   bool // true → match against directory components in relPath
}

var pathPatterns = []pathPattern{
	{Glob: "package-lock.json", Penalty: -0.40},
	{Glob: "yarn.lock", Penalty: -0.40},
	{Glob: "Gemfile.lock", Penalty: -0.40},
	{Glob: "Cargo.lock", Penalty: -0.40},
	{Glob: "Pipfile.lock", Penalty: -0.40},
	{Glob: "go.sum", Penalty: -0.40},
	{Glob: "*.min.js", Penalty: -0.40},
	{Glob: "*.min.css", Penalty: -0.40},
	{Glob: "*.map", Penalty: -0.40},
	{Glob: "vendor", Penalty: -0.30, IsDir: true},
	{Glob: "node_modules", Penalty: -0.30, IsDir: true},
	{Glob: "dist", Penalty: -0.20, IsDir: true},
	{Glob: "build", Penalty: -0.20, IsDir: true},
	{Glob: "README.md", Penalty: -0.20},
	{Glob: "CHANGELOG.md", Penalty: -0.20},
}

var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// computeSignals builds the Signals struct for one symbol. This is the
// per-symbol cost we're trying to measure.
func computeSignals(sym *ExtractedSymbol, baseExtractor float64, relPath string, source []byte) Signals {
	s := Signals{BaseExtractor: baseExtractor}

	if k, ok := kindBaseline[sym.Kind]; ok {
		s.KindBaseline = k
	} else {
		s.KindBaseline = baseExtractor
	}

	base := filepath.Base(relPath)
	for _, p := range pathPatterns {
		if p.IsDir {
			if pathContainsDir(relPath, p.Glob) {
				s.PathPenalty = p.Penalty
				break
			}
			continue
		}
		if matched, _ := filepath.Match(p.Glob, base); matched {
			s.PathPenalty = p.Penalty
			break
		}
	}

	if identRE.MatchString(sym.Name) {
		s.IdentBonus = 0.05
	} else if strings.TrimSpace(sym.Name) == "" {
		s.IdentBonus = -0.10
	}

	if isGeneratedFile(source) {
		s.GeneratedPen = -0.30
	}

	return s
}

// pathContainsDir checks whether dir is a path component of relPath.
// e.g. pathContainsDir("vendor/foo/bar.go", "vendor") == true.
func pathContainsDir(relPath, dir string) bool {
	for _, part := range strings.Split(filepath.ToSlash(relPath), "/") {
		if part == dir {
			return true
		}
	}
	return false
}

// isGeneratedFile inspects the head of source for the conventional
// "// Code generated" marker. Bounded to first 1KB so the cost is fixed
// regardless of file size.
func isGeneratedFile(source []byte) bool {
	head := source
	if len(head) > 1024 {
		head = head[:1024]
	}
	if len(head) == 0 {
		return false
	}
	// Avoid string allocation on the hot path — substring search via
	// IndexByte then verify.
	idx := 0
	for idx < len(head) {
		i := bytesIndex(head[idx:], "Code generated")
		if i < 0 {
			break
		}
		return true
	}
	return false
}

func bytesIndex(b []byte, s string) int {
	if len(s) == 0 {
		return 0
	}
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return i
		}
	}
	return -1
}
