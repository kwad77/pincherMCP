// Package index implements the pincherMCP file indexer.
//
// Pipeline per file:
//  1. Content-hash check (xxh3) — skip if file unchanged
//  2. AST extraction — symbols + edges with byte offsets
//  3. DB upsert — symbols into symbols table (triggers FTS5 sync)
//  4. DB upsert — edges into edges table
//  5. Hash update — record new hash for next incremental run
//
// All three indexes (byte-offset, graph, FTS5) are populated in a single pass.
package index

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/boyter/gocodewalker"
	"github.com/pincherMCP/pincher/internal/ast"
	"github.com/pincherMCP/pincher/internal/db"
	"github.com/zeebo/xxh3"
)

// Indexer manages repository indexing for pincherMCP.
type Indexer struct {
	store  *db.Store
	mu     sync.Mutex
	active map[string]bool // projectID → indexing in progress
}

// New creates a new Indexer.
func New(store *db.Store) *Indexer {
	return &Indexer{
		store:  store,
		active: make(map[string]bool),
	}
}

// IndexResult summarises a completed indexing run.
type IndexResult struct {
	ProjectID string
	Project   string
	Path      string
	Files     int
	Symbols   int
	Edges     int
	Skipped   int // files skipped (unchanged hash)
	DurationMS int64
}

// Index indexes a repository at the given path (incremental by default).
// If force=true, all files are re-parsed regardless of content hash.
func (idx *Indexer) Index(ctx context.Context, repoPath string, force bool) (*IndexResult, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("abs path: %w", err)
	}

	projectID := db.ProjectIDFromPath(absPath)
	projectName := db.ProjectNameFromPath(absPath)

	// Serialise per-project
	idx.mu.Lock()
	if idx.active[projectID] {
		idx.mu.Unlock()
		return nil, fmt.Errorf("project %q is already being indexed", projectName)
	}
	idx.active[projectID] = true
	idx.mu.Unlock()
	defer func() {
		idx.mu.Lock()
		delete(idx.active, projectID)
		idx.mu.Unlock()
	}()

	start := time.Now()

	// Ensure project record exists
	if err := idx.store.UpsertProject(db.Project{
		ID:        projectID,
		Path:      absPath,
		Name:      projectName,
		IndexedAt: start,
	}); err != nil {
		return nil, fmt.Errorf("upsert project: %w", err)
	}

	// Walk source files using gocodewalker (respects .gitignore)
	fileListQueue := make(chan *gocodewalker.File, 256)
	walker := gocodewalker.NewFileWalker(absPath, fileListQueue)
	walker.ExcludeDirectory = skippedDirSlice()

	// Start walker in background; gocodewalker closes the channel when done.
	go func() {
		if err := walker.Start(); err != nil {
			slog.Debug("pincher.walk.start.err", "err", err)
		}
	}()

	var (
		totalFiles   int
		totalSymbols int
		totalEdges   int
		totalSkipped int
		wg           sync.WaitGroup
		symBuf       []db.Symbol
		edgeBuf      []db.Edge
		bufMu        sync.Mutex
	)

	// Process files
	for fileJob := range fileListQueue {
		path := fileJob.Location
		if !ast.IsSourceFile(path) {
			continue
		}

		// Read file
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			slog.Debug("pincher.index.skip", "path", path, "err", readErr)
			continue
		}

		// Content-hash check (xxh3 — fastest non-crypto hash)
		hash := fmt.Sprintf("%x", xxh3.Hash(content))
		relPath, _ := filepath.Rel(absPath, path)
		relPath = filepath.ToSlash(relPath)

		if !force {
			stored := idx.store.GetFileHash(projectID, relPath)
			if stored == hash {
				totalSkipped++
				continue
			}
		}

		totalFiles++
		wg.Add(1)
		go func(path, relPath, hash string, content []byte) {
			defer wg.Done()

			lang := ast.DetectLanguage(path)
			if lang == "" {
				return
			}

			// Three-layer extraction in one pass
			result := ast.Extract(content, lang, relPath)
			if result == nil || len(result.Symbols) == 0 {
				return
			}

			// Convert to DB types
			syms := make([]db.Symbol, 0, len(result.Symbols))
			for _, s := range result.Symbols {
				sym := db.Symbol{
					ID:            db.MakeSymbolID(relPath, s.QualifiedName, s.Kind),
					ProjectID:     projectID,
					FilePath:      relPath,
					Name:          s.Name,
					QualifiedName: s.QualifiedName,
					Kind:          s.Kind,
					Language:      lang,
					StartByte:     s.StartByte,
					EndByte:       s.EndByte,
					StartLine:     s.StartLine,
					EndLine:       s.EndLine,
					Signature:     s.Signature,
					ReturnType:    s.ReturnType,
					Docstring:     s.Docstring,
					Parent:        s.Parent,
					Complexity:    s.Complexity,
					IsExported:    s.IsExported,
					IsTest:               s.IsTest,
					IsEntryPoint:         s.IsEntryPoint,
					FileHash:             hash,
					ExtractionConfidence: s.ExtractionConfidence,
				}
				syms = append(syms, sym)
			}

			// Build a quick name→id map for edge resolution
			nameToID := make(map[string]string, len(syms))
			for _, sym := range syms {
				nameToID[sym.Name] = sym.ID
				nameToID[sym.QualifiedName] = sym.ID
			}

			// Convert extracted edges to DB edges
			edges := make([]db.Edge, 0, len(result.Edges))
			for _, e := range result.Edges {
				fromID := nameToID[e.FromQN]
				if fromID == "" {
					// Try fuzzy: last component of FromQN
					parts := strings.Split(e.FromQN, ".")
					if len(parts) > 0 {
						fromID = nameToID[parts[len(parts)-1]]
					}
				}
				toID := nameToID[e.ToName]
				if fromID == "" || toID == "" {
					continue // unresolved — skip (will be re-resolved on full index)
				}
				edges = append(edges, db.Edge{
					ProjectID:  projectID,
					FromID:     fromID,
					ToID:       toID,
					Kind:       e.Kind,
					Confidence: e.Confidence,
				})
			}

			bufMu.Lock()
			symBuf = append(symBuf, syms...)
			edgeBuf = append(edgeBuf, edges...)
			// Flush when buffer is large enough
			if len(symBuf) >= 500 {
				if flushErr := idx.flushBuffers(projectID, &symBuf, &edgeBuf); flushErr != nil {
					slog.Warn("pincher.index.flush.err", "err", flushErr)
				}
			}
			bufMu.Unlock()
		}(path, relPath, hash, content)
	}

	wg.Wait()

	// Final flush
	bufMu.Lock()
	if len(symBuf) > 0 || len(edgeBuf) > 0 {
		totalSymbols += len(symBuf)
		totalEdges += len(edgeBuf)
		if flushErr := idx.flushBuffers(projectID, &symBuf, &edgeBuf); flushErr != nil {
			slog.Warn("pincher.index.final_flush.err", "err", flushErr)
		}
	}
	bufMu.Unlock()

	duration := time.Since(start)

	// Update project stats
	if err := idx.store.UpsertProject(db.Project{
		ID:        projectID,
		Path:      absPath,
		Name:      projectName,
		IndexedAt: start,
		FileCount: totalFiles,
		SymCount:  totalSymbols,
		EdgeCount: totalEdges,
	}); err != nil {
		slog.Warn("pincher.index.update_project.err", "err", err)
	}

	slog.Info("pincher.indexed",
		"project", projectName,
		"files", totalFiles,
		"symbols", totalSymbols,
		"edges", totalEdges,
		"skipped", totalSkipped,
		"ms", duration.Milliseconds(),
	)

	return &IndexResult{
		ProjectID:  projectID,
		Project:    projectName,
		Path:       absPath,
		Files:      totalFiles,
		Symbols:    totalSymbols,
		Edges:      totalEdges,
		Skipped:    totalSkipped,
		DurationMS: duration.Milliseconds(),
	}, nil
}

// flushBuffers writes accumulated symbols and edges to the DB then resets the slices.
func (idx *Indexer) flushBuffers(projectID string, syms *[]db.Symbol, edges *[]db.Edge) error {
	if err := idx.flushBatch(projectID, *syms, *edges); err != nil {
		return err
	}
	// Update file hashes for all flushed symbols
	seen := make(map[string]bool)
	for _, s := range *syms {
		if !seen[s.FilePath] {
			_ = idx.store.SetFileHash(projectID, s.FilePath, s.FileHash)
			seen[s.FilePath] = true
		}
	}
	*syms = (*syms)[:0]
	*edges = (*edges)[:0]
	return nil
}

func (idx *Indexer) flushBatch(projectID string, syms []db.Symbol, edges []db.Edge) error {
	if err := idx.store.BulkUpsertSymbols(syms); err != nil {
		return fmt.Errorf("upsert symbols: %w", err)
	}
	if err := idx.store.BulkUpsertEdges(edges); err != nil {
		return fmt.Errorf("upsert edges: %w", err)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Byte-offset source retrieval
// ─────────────────────────────────────────────────────────────────────────────

// ReadSymbolSource retrieves the source code for a symbol using O(1) byte-offset seeking.
// This is the core pincherMCP innovation: no re-parsing, no line scanning.
//
//	1 SQL lookup  → get start_byte, end_byte, file_path
//	1 os.Open     → open the file
//	1 Seek        → seek to start_byte
//	1 Read        → read (end_byte - start_byte) bytes
func ReadSymbolSource(projectRoot string, sym db.Symbol) (string, error) {
	if sym.StartByte == sym.EndByte {
		return "", nil
	}
	absPath := filepath.Join(projectRoot, filepath.FromSlash(sym.FilePath))
	f, err := os.Open(absPath)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", sym.FilePath, err)
	}
	defer f.Close()

	size := sym.EndByte - sym.StartByte
	if size <= 0 {
		return "", nil
	}
	if _, err := f.Seek(int64(sym.StartByte), 0); err != nil {
		return "", fmt.Errorf("seek: %w", err)
	}
	buf := make([]byte, size)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", fmt.Errorf("read: %w", err)
	}
	return string(buf[:n]), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// File watcher
// ─────────────────────────────────────────────────────────────────────────────

// Watch starts a background goroutine that re-indexes projects when files change.
// Uses polling with adaptive interval (2s for active projects, 30s otherwise).
func (idx *Indexer) Watch(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			projects, err := idx.store.ListProjects()
			if err != nil {
				continue
			}
			for _, p := range projects {
				if idx.hasChanges(p) {
					slog.Debug("pincher.watcher.reindex", "project", p.Name)
					go func(p db.Project) {
						if _, err := idx.Index(ctx, p.Path, false); err != nil {
							slog.Warn("pincher.watcher.reindex.err", "project", p.Name, "err", err)
						}
					}(p)
				}
			}
		}
	}
}

// hasChanges checks if any source file in the project has changed since last index.
// Uses a fast mtime check before doing the full xxh3 hash comparison.
func (idx *Indexer) hasChanges(p db.Project) bool {
	// Quick stat check on a sample of files
	entries, err := os.ReadDir(p.Path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !ast.IsSourceFile(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// If any file is newer than the last index time, trigger re-index
		if info.ModTime().After(p.IndexedAt) {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// Call graph (BFS for trace tool)
// ─────────────────────────────────────────────────────────────────────────────

// Hop is one step in a BFS call trace.
type Hop struct {
	Symbol db.Symbol
	Depth  int
	Via    string // edge kind that brought us here
	Risk   string // CRITICAL|HIGH|MEDIUM|LOW
}

// Trace performs BFS from a named symbol, returning the call chain.
// direction: "outbound" (what it calls), "inbound" (what calls it), "both"
// maxDepth: 1-5
//
// Implementation: delegates to db.TraceViaCTE which issues at most 2 SQL
// recursive CTE queries regardless of graph size, replacing the old approach
// that issued O(nodes × depth × 2) individual SQL round trips.
func (idx *Indexer) Trace(ctx context.Context, projectID, name string, direction string, maxDepth int, addRisk bool) ([]Hop, error) {
	if maxDepth <= 0 || maxDepth > 5 {
		maxDepth = 3
	}

	// Find start symbol
	starts, err := idx.store.GetSymbolsByName(projectID, name, 5)
	if err != nil {
		return nil, err
	}
	if len(starts) == 0 {
		return nil, fmt.Errorf("symbol %q not found in project", name)
	}
	start := starts[0]

	edgeKinds := []string{"CALLS", "HTTP_CALLS", "ASYNC_CALLS"}

	// Single CTE traversal per direction (max 2 SQL calls total for "both").
	traceResults, err := idx.store.TraceViaCTE(start.ID, direction, edgeKinds, maxDepth)
	if err != nil {
		return nil, err
	}

	var hops []Hop
	for _, tr := range traceResults {
		sym, getErr := idx.store.GetSymbol(tr.SymbolID)
		if getErr != nil || sym == nil {
			continue
		}
		risk := ""
		if addRisk {
			risk = riskLabel(tr.Depth)
		}
		hops = append(hops, Hop{Symbol: *sym, Depth: tr.Depth, Via: tr.ViaKind, Risk: risk})
		if len(hops) >= 500 {
			break
		}
	}
	return hops, nil
}

func riskLabel(depth int) string {
	switch depth {
	case 1:
		return "CRITICAL"
	case 2:
		return "HIGH"
	case 3:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

var skippedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".cache":       true,
	"dist":         true,
	"build":        true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	"env":          true,
	"target":       true, // Rust
	".next":        true,
	".nuxt":        true,
	"coverage":     true,
}

func isSkippedDir(name string) bool {
	return skippedDirs[name] || strings.HasPrefix(name, ".")
}

// skippedDirSlice returns the skippedDirs map keys as a slice for gocodewalker.ExcludeDirectory.
func skippedDirSlice() []string {
	out := make([]string, 0, len(skippedDirs))
	for k := range skippedDirs {
		out = append(out, k)
	}
	return out
}
