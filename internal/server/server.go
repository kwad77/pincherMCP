// Package server implements the pincherMCP MCP server with all 12 tools.
//
// Every tool response includes a "_meta" envelope (jcodemunch-mcp pattern):
//
//	{
//	  "result": { ... },
//	  "_meta": {
//	    "tokens_used":  450,
//	    "tokens_saved": 12300,
//	    "latency_ms":   3,
//	    "cost_avoided": "$0.0012"
//	  }
//	}
//
// This lets agents track context consumption and remaining budget.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pincherMCP/pincher/internal/cypher"
	"github.com/pincherMCP/pincher/internal/db"
	"github.com/pincherMCP/pincher/internal/index"
)

// Server is the pincherMCP MCP server.
type Server struct {
	mcp      *mcp.Server
	store    *db.Store
	indexer  *index.Indexer
	handlers map[string]mcp.ToolHandler

	sessionOnce    sync.Once
	sessionRoot    string
	sessionProject string // derived from sessionRoot
	sessionID      string // db.ProjectIDFromPath(sessionRoot)
}

// New creates and registers all 12 MCP tools.
func New(store *db.Store, indexer *index.Indexer, version string) *Server {
	s := &Server{
		store:    store,
		indexer:  indexer,
		handlers: make(map[string]mcp.ToolHandler),
	}
	s.mcp = mcp.NewServer(
		&mcp.Implementation{Name: "pincher", Version: version},
		&mcp.ServerOptions{
			InitializedHandler:      s.onInit,
			RootsListChangedHandler: s.onRoots,
		},
	)
	s.registerTools()
	return s
}

// MCPServer returns the underlying *mcp.Server.
func (s *Server) MCPServer() *mcp.Server { return s.mcp }

func (s *Server) onInit(ctx context.Context, req *mcp.InitializedRequest) {
	s.sessionOnce.Do(func() {
		s.detectRoot(ctx, req.Session)
	})
}

func (s *Server) onRoots(ctx context.Context, req *mcp.RootsListChangedRequest) {
	s.sessionOnce.Do(func() {
		s.detectRoot(ctx, req.Session)
	})
}

func (s *Server) detectRoot(ctx context.Context, session *mcp.ServerSession) {
	if session != nil {
		if result, err := session.ListRoots(ctx, nil); err == nil && len(result.Roots) > 0 {
			if path, ok := parseFileURI(result.Roots[0].URI); ok {
				s.setRoot(path)
				return
			}
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		s.setRoot(cwd)
	}
}

func (s *Server) setRoot(path string) {
	s.sessionRoot = path
	s.sessionProject = db.ProjectNameFromPath(path)
	s.sessionID = db.ProjectIDFromPath(path)
}

func parseFileURI(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	p := u.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p), true
}

// resolveProjectID returns the project ID for the given name/ID, falling back to session project.
func (s *Server) resolveProjectID(projectArg string) (string, error) {
	if projectArg == "" {
		if s.sessionID == "" {
			return "", fmt.Errorf("no project specified and no session project detected")
		}
		return s.sessionID, nil
	}
	// Accept either a name or ID
	p, err := s.store.GetProject(projectArg)
	if err != nil {
		return "", err
	}
	if p != nil {
		return p.ID, nil
	}
	// Try matching by name
	all, err := s.store.ListProjects()
	if err != nil {
		return "", err
	}
	for _, proj := range all {
		if proj.Name == projectArg {
			return proj.ID, nil
		}
	}
	return "", fmt.Errorf("project %q not found — use `list` to see available projects", projectArg)
}

// resolveProjectRoot returns the filesystem root for a project.
func (s *Server) resolveProjectRoot(projectID string) (string, error) {
	p, err := s.store.GetProject(projectID)
	if err != nil || p == nil {
		if s.sessionRoot != "" {
			return s.sessionRoot, nil
		}
		return "", fmt.Errorf("project not found")
	}
	return p.Path, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Tool registration
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) addTool(tool *mcp.Tool, handler mcp.ToolHandler) {
	s.mcp.AddTool(tool, handler)
	s.handlers[tool.Name] = handler
}

func (s *Server) registerTools() {
	// 1. index
	s.addTool(&mcp.Tool{
		Name:        "index",
		Description: "Index a repository. Extracts symbols with byte offsets, builds knowledge graph, and populates FTS5 search — all in one pass. Incremental by default (content-hash checks skip unchanged files). Set force=true to re-parse everything. Returns counts and latency.",
		InputSchema: json.RawMessage(`{
			"type":"object","properties":{
				"path":{"type":"string","description":"Absolute path to the repository root. Defaults to session project root."},
				"force":{"type":"boolean","description":"If true, re-parse all files even if unchanged."}
			}
		}`),
	}, s.handleIndex)

	// 2. symbol
	s.addTool(&mcp.Tool{
		Name:        "symbol",
		Description: "Retrieve source code for a symbol by its stable ID using O(1) byte-offset seeking. No re-parsing. Format: '{file_path}::{qualified_name}#{kind}'. Use `search` first to find the ID. Returns source, signature, location, and token savings metadata.",
		InputSchema: json.RawMessage(`{
			"type":"object","required":["id"],"properties":{
				"id":{"type":"string","description":"Stable symbol ID. Format: '{file_path}::{qualified_name}#{kind}'"},
				"project":{"type":"string","description":"Project name or ID. Defaults to session project."}
			}
		}`),
	}, s.handleSymbol)

	// 3. symbols (batch)
	s.addTool(&mcp.Tool{
		Name:        "symbols",
		Description: "Batch retrieve source code for multiple symbols in one call. Use instead of calling `symbol` in a loop. Minimises round trips. Returns array of {id, source, signature, file_path, start_line}.",
		InputSchema: json.RawMessage(`{
			"type":"object","required":["ids"],"properties":{
				"ids":{"type":"array","items":{"type":"string"},"description":"Array of stable symbol IDs."},
				"project":{"type":"string"}
			}
		}`),
	}, s.handleSymbols)

	// 4. context
	s.addTool(&mcp.Tool{
		Name:        "context",
		Description: "Get a symbol plus its direct imports as a minimal-token context bundle. Use this instead of reading entire files — ~90% token reduction. Returns {symbol: {source, ...}, imports: [{source, ...}]}.",
		InputSchema: json.RawMessage(`{
			"type":"object","required":["id"],"properties":{
				"id":{"type":"string","description":"Symbol ID to fetch with its imports."},
				"project":{"type":"string"}
			}
		}`),
	}, s.handleContext)

	// 5. search
	s.addTool(&mcp.Tool{
		Name:        "search",
		Description: "Full-text search across symbol names, qualified names, signatures, and docstrings. Uses FTS5 BM25 ranking. Supports wildcards (auth*) and phrase queries (\"process order\"). Filter by kind (Function/Class/etc) or language.",
		InputSchema: json.RawMessage(`{
			"type":"object","required":["query"],"properties":{
				"query":{"type":"string","description":"FTS5 search query. Supports: prefix (auth*), phrase (\"login flow\"), AND/OR."},
				"project":{"type":"string"},
				"kind":{"type":"string","description":"Filter by symbol kind: Function|Method|Class|Interface|Enum|Type|Variable|Module"},
				"language":{"type":"string","description":"Filter by language: Go|Python|TypeScript|etc"},
				"limit":{"type":"integer","description":"Max results (default 20)"}
			}
		}`),
	}, s.handleSearch)

	// 6. query
	s.addTool(&mcp.Tool{
		Name:        "query",
		Description: "Execute a Cypher-like graph query. Sub-ms for single-hop patterns. Supports: MATCH (n:Kind) WHERE n.name='x' RETURN n.name; MATCH (a)-[:CALLS]->(b) RETURN a.name,b.name; MATCH (a)-[:CALLS*1..3]->(b) WHERE a.name='main' RETURN b.name; WHERE with =, =~(regex), CONTAINS, >, < operators.",
		InputSchema: json.RawMessage(`{
			"type":"object","required":["cypher"],"properties":{
				"cypher":{"type":"string","description":"Cypher query. Example: MATCH (f:Function)-[:CALLS]->(g) WHERE f.name='main' RETURN g.name LIMIT 20"},
				"project":{"type":"string"},
				"max_rows":{"type":"integer","description":"Max rows (default 200, max 10000)"}
			}
		}`),
	}, s.handleQuery)

	// 7. trace
	s.addTool(&mcp.Tool{
		Name:        "trace",
		Description: "BFS call-path trace: who calls this function, or what does it call. Returns hops with risk labels (CRITICAL=depth1, HIGH=depth2, MEDIUM=depth3, LOW=depth4+). Use search first to find the exact function name.",
		InputSchema: json.RawMessage(`{
			"type":"object","required":["name"],"properties":{
				"name":{"type":"string","description":"Function name to trace (short name, e.g. 'ProcessOrder')"},
				"project":{"type":"string"},
				"direction":{"type":"string","enum":["outbound","inbound","both"],"description":"outbound=what it calls, inbound=what calls it. Default: both"},
				"depth":{"type":"integer","description":"BFS depth 1-5 (default 3)"},
				"risk":{"type":"boolean","description":"Add CRITICAL/HIGH/MEDIUM/LOW risk labels (default true)"}
			}
		}`),
	}, s.handleTrace)

	// 8. changes
	s.addTool(&mcp.Tool{
		Name:        "changes",
		Description: "Map git diff to affected symbols and compute blast radius. Runs `git diff` in the project directory, finds which symbols changed, then BFS-traces the impact. Returns changed_symbols, impacted (with risk), and summary.",
		InputSchema: json.RawMessage(`{
			"type":"object","properties":{
				"project":{"type":"string"},
				"scope":{"type":"string","enum":["unstaged","staged","all"],"description":"Which diff to analyse (default: unstaged)"},
				"depth":{"type":"integer","description":"Blast radius BFS depth 1-5 (default 3)"}
			}
		}`),
	}, s.handleChanges)

	// 9. architecture
	s.addTool(&mcp.Tool{
		Name:        "architecture",
		Description: "High-level codebase orientation. Returns languages, entry points, top packages, hotspot functions (most called), and graph statistics. Call this first when exploring an unfamiliar project.",
		InputSchema: json.RawMessage(`{
			"type":"object","properties":{
				"project":{"type":"string"}
			}
		}`),
	}, s.handleArchitecture)

	// 10. schema
	s.addTool(&mcp.Tool{
		Name:        "schema",
		Description: "Knowledge graph schema: node kind counts, edge kind counts, total symbols, total edges. Use before query to understand what's indexed.",
		InputSchema: json.RawMessage(`{
			"type":"object","properties":{
				"project":{"type":"string"}
			}
		}`),
	}, s.handleSchema)

	// 11. list
	s.addTool(&mcp.Tool{
		Name:        "list",
		Description: "List all indexed projects with stats: name, path, file count, symbol count, edge count, last indexed timestamp.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, s.handleList)

	// 12. adr
	s.addTool(&mcp.Tool{
		Name:        "adr",
		Description: "Architecture Decision Records — persistent project knowledge store. actions: get (retrieve value), set (store value), list (all entries), delete (remove). Use to record stack decisions, architectural patterns, team conventions.",
		InputSchema: json.RawMessage(`{
			"type":"object","required":["action"],"properties":{
				"action":{"type":"string","enum":["get","set","list","delete"]},
				"project":{"type":"string"},
				"key":{"type":"string","description":"ADR key (e.g. 'PURPOSE', 'STACK', 'PATTERNS')"},
				"value":{"type":"string","description":"ADR value (required for action=set)"}
			}
		}`),
	}, s.handleADR)
}

// ─────────────────────────────────────────────────────────────────────────────
// Tool handlers
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) handleIndex(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	args := parseArgs(req)

	path := str(args, "path")
	if path == "" {
		path = s.sessionRoot
	}
	if path == "" {
		return errResult("path is required (no session root detected)"), nil
	}
	force := boolArg(args, "force")

	result, err := s.indexer.Index(ctx, path, force)
	if err != nil {
		return errResult(fmt.Sprintf("index error: %v", err)), nil
	}

	data := map[string]any{
		"project":    result.Project,
		"path":       result.Path,
		"files":      result.Files,
		"symbols":    result.Symbols,
		"edges":      result.Edges,
		"skipped":    result.Skipped,
		"duration_ms": result.DurationMS,
	}
	return jsonResultWithMeta(data, start, 0), nil
}

func (s *Server) handleSymbol(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	args := parseArgs(req)

	id := str(args, "id")
	if id == "" {
		return errResult("id is required"), nil
	}
	projectArg := str(args, "project")

	sym, err := s.store.GetSymbol(id)
	if err != nil {
		return errResult(fmt.Sprintf("db error: %v", err)), nil
	}
	if sym == nil {
		return errResult(fmt.Sprintf("symbol %q not found", id)), nil
	}

	// Resolve project root for byte-offset seek
	projectID := sym.ProjectID
	if projectArg != "" {
		if pid, err := s.resolveProjectID(projectArg); err == nil {
			projectID = pid
		}
	}
	root, err := s.resolveProjectRoot(projectID)
	if err != nil {
		root = s.sessionRoot
	}

	// O(1) byte-offset retrieval — the pincherMCP core innovation
	source := ""
	if root != "" {
		source, _ = index.ReadSymbolSource(root, *sym)
	}

	// Estimate token savings vs. reading the whole file
	var fileSizeBytes int
	if root != "" {
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(sym.FilePath))); err == nil {
			fileSizeBytes = int(fi.Size())
		}
	}
	symbolBytes := sym.EndByte - sym.StartByte
	tokensSaved := db.ApproxTokens(strings.Repeat("x", max(0, fileSizeBytes-symbolBytes)))

	data := map[string]any{
		"id":            sym.ID,
		"name":          sym.Name,
		"qualified_name": sym.QualifiedName,
		"kind":          sym.Kind,
		"language":      sym.Language,
		"file_path":     sym.FilePath,
		"start_line":    sym.StartLine,
		"end_line":      sym.EndLine,
		"start_byte":    sym.StartByte,
		"end_byte":      sym.EndByte,
		"signature":     sym.Signature,
		"return_type":   sym.ReturnType,
		"docstring":     sym.Docstring,
		"complexity":    sym.Complexity,
		"is_exported":   sym.IsExported,
		"source":        source,
	}
	return jsonResultWithMeta(data, start, tokensSaved), nil
}

func (s *Server) handleSymbols(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	args := parseArgs(req)

	ids := strSlice(args, "ids")
	if len(ids) == 0 {
		return errResult("ids array is required"), nil
	}

	projectArg := str(args, "project")
	root := s.sessionRoot
	if projectArg != "" {
		if pid, err := s.resolveProjectID(projectArg); err == nil {
			if r, err := s.resolveProjectRoot(pid); err == nil {
				root = r
			}
		}
	}

	var results []map[string]any
	for _, id := range ids {
		sym, err := s.store.GetSymbol(id)
		if err != nil || sym == nil {
			results = append(results, map[string]any{"id": id, "error": "not found"})
			continue
		}
		source := ""
		if root != "" {
			source, _ = index.ReadSymbolSource(root, *sym)
		}
		results = append(results, map[string]any{
			"id":         sym.ID,
			"name":       sym.Name,
			"kind":       sym.Kind,
			"file_path":  sym.FilePath,
			"start_line": sym.StartLine,
			"signature":  sym.Signature,
			"source":     source,
		})
	}

	data := map[string]any{
		"symbols": results,
		"count":   len(results),
	}
	return jsonResultWithMeta(data, start, 0), nil
}

func (s *Server) handleContext(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	args := parseArgs(req)

	id := str(args, "id")
	if id == "" {
		return errResult("id is required"), nil
	}

	sym, err := s.store.GetSymbol(id)
	if err != nil || sym == nil {
		return errResult(fmt.Sprintf("symbol %q not found", id)), nil
	}

	root, _ := s.resolveProjectRoot(sym.ProjectID)
	source, _ := index.ReadSymbolSource(root, *sym)

	// Find IMPORTS edges from this symbol
	importEdges, _ := s.store.EdgesFrom(sym.ID, []string{"IMPORTS"})
	var imports []map[string]any
	tokensSaved := 0
	for _, e := range importEdges {
		imp, err := s.store.GetSymbol(e.ToID)
		if err != nil || imp == nil {
			continue
		}
		impSource, _ := index.ReadSymbolSource(root, *imp)
		imports = append(imports, map[string]any{
			"id":        imp.ID,
			"name":      imp.Name,
			"kind":      imp.Kind,
			"file_path": imp.FilePath,
			"source":    impSource,
		})
		tokensSaved += db.ApproxTokens(impSource) // we fetched only what's needed
	}

	data := map[string]any{
		"symbol":  map[string]any{"id": sym.ID, "name": sym.Name, "kind": sym.Kind, "source": source},
		"imports": imports,
	}
	return jsonResultWithMeta(data, start, tokensSaved), nil
}

func (s *Server) handleSearch(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	args := parseArgs(req)

	query := str(args, "query")
	if query == "" {
		return errResult("query is required"), nil
	}
	projectArg := str(args, "project")
	kind := str(args, "kind")
	language := str(args, "language")
	limit := intArg(args, "limit", 20)

	projectID, err := s.resolveProjectID(projectArg)
	if err != nil {
		return errResult(err.Error()), nil
	}

	results, err := s.store.SearchSymbols(projectID, query, kind, language, limit)
	if err != nil {
		return errResult(fmt.Sprintf("search error: %v", err)), nil
	}

	var rows []map[string]any
	for _, r := range results {
		rows = append(rows, map[string]any{
			"id":             r.Symbol.ID,
			"name":           r.Symbol.Name,
			"qualified_name": r.Symbol.QualifiedName,
			"kind":           r.Symbol.Kind,
			"language":       r.Symbol.Language,
			"file_path":      r.Symbol.FilePath,
			"start_line":     r.Symbol.StartLine,
			"signature":      r.Symbol.Signature,
			"score":          r.Score,
		})
	}

	// Token savings: returned only symbol stubs, not full file contents
	tokensSaved := db.ApproxTokens(strings.Repeat("x", len(results)*2000))

	data := map[string]any{
		"results": rows,
		"count":   len(rows),
		"query":   query,
	}
	return jsonResultWithMeta(data, start, tokensSaved), nil
}

func (s *Server) handleQuery(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	args := parseArgs(req)

	cql := str(args, "cypher")
	if cql == "" {
		return errResult("cypher query is required"), nil
	}
	projectArg := str(args, "project")
	maxRows := intArg(args, "max_rows", 200)

	projectID, err := s.resolveProjectID(projectArg)
	if err != nil {
		return errResult(err.Error()), nil
	}
	_ = projectID // Cypher executor uses the DB directly

	exec := &cypher.Executor{DB: s.store.DB(), MaxRows: maxRows}
	result, err := exec.Execute(ctx, cql)
	if err != nil {
		return errResult(fmt.Sprintf("cypher error: %v", err)), nil
	}

	data := map[string]any{
		"columns": result.Columns,
		"rows":    result.Rows,
		"total":   result.Total,
	}
	return jsonResultWithMeta(data, start, 0), nil
}

func (s *Server) handleTrace(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	args := parseArgs(req)

	name := str(args, "name")
	if name == "" {
		return errResult("name is required"), nil
	}
	projectArg := str(args, "project")
	direction := str(args, "direction")
	if direction == "" {
		direction = "both"
	}
	depth := intArg(args, "depth", 3)
	addRisk := boolArgDefault(args, "risk", true)

	projectID, err := s.resolveProjectID(projectArg)
	if err != nil {
		return errResult(err.Error()), nil
	}

	hops, err := s.indexer.Trace(ctx, projectID, name, direction, depth, addRisk)
	if err != nil {
		return errResult(fmt.Sprintf("trace error: %v", err)), nil
	}

	// Group by depth
	byDepth := make(map[int][]map[string]any)
	riskCounts := map[string]int{"CRITICAL": 0, "HIGH": 0, "MEDIUM": 0, "LOW": 0}
	for _, h := range hops {
		entry := map[string]any{
			"id":         h.Symbol.ID,
			"name":       h.Symbol.Name,
			"kind":       h.Symbol.Kind,
			"file_path":  h.Symbol.FilePath,
			"start_line": h.Symbol.StartLine,
			"via":        h.Via,
		}
		if addRisk {
			entry["risk"] = h.Risk
			riskCounts[h.Risk]++
		}
		byDepth[h.Depth] = append(byDepth[h.Depth], entry)
	}

	var hopsList []map[string]any
	for d := 1; d <= depth; d++ {
		if nodes, ok := byDepth[d]; ok {
			hop := map[string]any{"depth": d, "nodes": nodes}
			if addRisk {
				hop["risk"] = riskLabel(d)
			}
			hopsList = append(hopsList, hop)
		}
	}

	data := map[string]any{
		"root":      name,
		"direction": direction,
		"hops":      hopsList,
		"total":     len(hops),
	}
	if addRisk {
		data["risk_summary"] = riskCounts
	}
	return jsonResultWithMeta(data, start, 0), nil
}

func (s *Server) handleChanges(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	args := parseArgs(req)

	projectArg := str(args, "project")
	scope := str(args, "scope")
	if scope == "" {
		scope = "unstaged"
	}
	depth := intArg(args, "depth", 3)

	projectID, err := s.resolveProjectID(projectArg)
	if err != nil {
		return errResult(err.Error()), nil
	}
	root, err := s.resolveProjectRoot(projectID)
	if err != nil {
		return errResult(err.Error()), nil
	}

	// Run git diff
	diffOutput, diffErr := runGitDiff(root, scope)
	if diffErr != nil {
		return errResult(fmt.Sprintf("git diff failed: %v", diffErr)), nil
	}

	// Parse changed files from diff
	changedFiles := parseGitDiffFiles(diffOutput)

	// Find symbols in changed files
	var changedSymbols []db.Symbol
	for _, f := range changedFiles {
		syms, err := s.store.GetSymbolsForFile(projectID, f)
		if err != nil {
			continue
		}
		changedSymbols = append(changedSymbols, syms...)
	}

	// BFS trace for blast radius
	var impacted []map[string]any
	seen := make(map[string]bool)
	for _, sym := range changedSymbols {
		hops, err := s.indexer.Trace(ctx, projectID, sym.Name, "inbound", depth, true)
		if err != nil {
			continue
		}
		for _, h := range hops {
			if seen[h.Symbol.ID] {
				continue
			}
			seen[h.Symbol.ID] = true
			impacted = append(impacted, map[string]any{
				"id":         h.Symbol.ID,
				"name":       h.Symbol.Name,
				"kind":       h.Symbol.Kind,
				"file_path":  h.Symbol.FilePath,
				"risk":       h.Risk,
				"changed_by": sym.Name,
			})
		}
	}

	// Build risk summary
	riskCounts := map[string]int{"CRITICAL": 0, "HIGH": 0, "MEDIUM": 0, "LOW": 0}
	for _, item := range impacted {
		if r, ok := item["risk"].(string); ok {
			riskCounts[r]++
		}
	}

	var changedSymNames []map[string]any
	for _, sym := range changedSymbols {
		changedSymNames = append(changedSymNames, map[string]any{
			"id": sym.ID, "name": sym.Name, "kind": sym.Kind, "file_path": sym.FilePath,
		})
	}

	data := map[string]any{
		"changed_files":   changedFiles,
		"changed_symbols": changedSymNames,
		"impacted":        impacted,
		"summary": map[string]any{
			"changed_files":   len(changedFiles),
			"changed_symbols": len(changedSymbols),
			"total_impacted":  len(impacted),
			"critical":        riskCounts["CRITICAL"],
			"high":            riskCounts["HIGH"],
			"medium":          riskCounts["MEDIUM"],
			"low":             riskCounts["LOW"],
		},
	}
	return jsonResultWithMeta(data, start, 0), nil
}

func (s *Server) handleArchitecture(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	args := parseArgs(req)
	projectArg := str(args, "project")

	projectID, err := s.resolveProjectID(projectArg)
	if err != nil {
		return errResult(err.Error()), nil
	}

	p, _ := s.store.GetProject(projectID)

	// Language breakdown
	langRows, _ := s.store.DB().QueryContext(ctx,
		`SELECT language, COUNT(*) FROM symbols WHERE project_id=? GROUP BY language ORDER BY COUNT(*) DESC LIMIT 20`,
		projectID)
	langs := make(map[string]int)
	if langRows != nil {
		for langRows.Next() {
			var lang string
			var cnt int
			_ = langRows.Scan(&lang, &cnt)
			langs[lang] = cnt
		}
		langRows.Close()
	}

	// Entry points
	epRows, _ := s.store.DB().QueryContext(ctx,
		`SELECT name, file_path, start_line FROM symbols WHERE project_id=? AND is_entry_point=1 LIMIT 20`,
		projectID)
	var entryPoints []map[string]any
	if epRows != nil {
		for epRows.Next() {
			var name, fp string
			var line int
			_ = epRows.Scan(&name, &fp, &line)
			entryPoints = append(entryPoints, map[string]any{"name": name, "file_path": fp, "start_line": line})
		}
		epRows.Close()
	}

	// Hotspots (most-called)
	hotspots, _ := s.store.GetHotspots(projectID, 10)
	var hotspotMaps []map[string]any
	for _, h := range hotspots {
		hotspotMaps = append(hotspotMaps, map[string]any{
			"name": h.Name, "kind": h.Kind, "file_path": h.FilePath,
		})
	}

	// Graph stats
	_, _, kindCounts, edgeKindCounts, _ := s.store.GraphStats(projectID)

	data := map[string]any{
		"project":         p,
		"languages":       langs,
		"entry_points":    entryPoints,
		"hotspots":        hotspotMaps,
		"node_kinds":      kindCounts,
		"edge_kinds":      edgeKindCounts,
	}
	return jsonResultWithMeta(data, start, 0), nil
}

func (s *Server) handleSchema(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	args := parseArgs(req)
	projectArg := str(args, "project")

	projectID, err := s.resolveProjectID(projectArg)
	if err != nil {
		return errResult(err.Error()), nil
	}

	symCount, edgeCount, kindCounts, edgeKindCounts, err := s.store.GraphStats(projectID)
	if err != nil {
		return errResult(fmt.Sprintf("stats error: %v", err)), nil
	}

	data := map[string]any{
		"symbols":         symCount,
		"edges":           edgeCount,
		"node_kinds":      kindCounts,
		"edge_kinds":      edgeKindCounts,
	}
	return jsonResultWithMeta(data, start, 0), nil
}

func (s *Server) handleList(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	projects, err := s.store.ListProjects()
	if err != nil {
		return errResult(fmt.Sprintf("list error: %v", err)), nil
	}
	var rows []map[string]any
	for _, p := range projects {
		rows = append(rows, map[string]any{
			"id":         p.ID,
			"name":       p.Name,
			"path":       p.Path,
			"files":      p.FileCount,
			"symbols":    p.SymCount,
			"edges":      p.EdgeCount,
			"indexed_at": p.IndexedAt.Format(time.RFC3339),
		})
	}
	data := map[string]any{"projects": rows, "count": len(rows)}
	return jsonResultWithMeta(data, start, 0), nil
}

func (s *Server) handleADR(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	args := parseArgs(req)
	action := str(args, "action")
	projectArg := str(args, "project")
	key := str(args, "key")
	value := str(args, "value")

	projectID, err := s.resolveProjectID(projectArg)
	if err != nil {
		return errResult(err.Error()), nil
	}

	var data map[string]any
	switch action {
	case "get":
		if key == "" {
			return errResult("key is required for action=get"), nil
		}
		val, ok, err := s.store.GetADR(projectID, key)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if !ok {
			return errResult(fmt.Sprintf("ADR key %q not found", key)), nil
		}
		data = map[string]any{"key": key, "value": val}

	case "set":
		if key == "" || value == "" {
			return errResult("key and value are required for action=set"), nil
		}
		if err := s.store.SetADR(projectID, key, value); err != nil {
			return errResult(err.Error()), nil
		}
		data = map[string]any{"key": key, "stored": true}

	case "list":
		entries, err := s.store.ListADRs(projectID)
		if err != nil {
			return errResult(err.Error()), nil
		}
		data = map[string]any{"entries": entries}

	case "delete":
		if key == "" {
			return errResult("key is required for action=delete"), nil
		}
		if err := s.store.DeleteADR(projectID, key); err != nil {
			return errResult(err.Error()), nil
		}
		data = map[string]any{"key": key, "deleted": true}

	default:
		return errResult(fmt.Sprintf("unknown action %q", action)), nil
	}

	return jsonResultWithMeta(data, start, 0), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// _meta envelope (jcodemunch-mcp pattern)
// ─────────────────────────────────────────────────────────────────────────────

// baseCostPer1M is the approximate cost per 1M tokens for Claude Sonnet (USD).
const baseCostPer1M = 3.0

func jsonResultWithMeta(data map[string]any, start time.Time, tokensSaved int) *mcp.CallToolResult {
	latency := time.Since(start).Milliseconds()

	// Estimate tokens in this response
	b, _ := json.Marshal(data)
	tokensUsed := db.ApproxTokens(string(b))

	// Cost avoided by not sending tokensSaved tokens to the model
	costAvoided := float64(tokensSaved) / 1_000_000.0 * baseCostPer1M

	data["_meta"] = map[string]any{
		"tokens_used":  tokensUsed,
		"tokens_saved": tokensSaved,
		"latency_ms":   latency,
		"cost_avoided": fmt.Sprintf("$%.4f", costAvoided),
	}

	out, _ := json.MarshalIndent(data, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(out)}},
	}
}

func errResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Argument helpers
// ─────────────────────────────────────────────────────────────────────────────

func parseArgs(req *mcp.CallToolRequest) map[string]any {
	if len(req.Params.Arguments) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	_ = json.Unmarshal(req.Params.Arguments, &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func str(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func strSlice(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func intArg(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok {
		return def
	}
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return def
}

func boolArg(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

func boolArgDefault(args map[string]any, key string, def bool) bool {
	v, ok := args[key]
	if !ok {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}

// ─────────────────────────────────────────────────────────────────────────────
// Git helpers
// ─────────────────────────────────────────────────────────────────────────────

func runGitDiff(root, scope string) (string, error) {
	args := []string{"diff", "--name-only"}
	switch scope {
	case "staged":
		args = append(args, "--cached")
	case "all":
		args = append(args, "HEAD")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func parseGitDiffFiles(diff string) []string {
	var files []string
	for _, line := range strings.Split(diff, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			files = append(files, line)
		}
	}
	return files
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
