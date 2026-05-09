package db

import "fmt"

// Corpus is the FTS5-routing label assigned to every symbol. The per-corpus
// FTS5 split (#32) keeps the same search UX but routes BM25 ranking through
// three independent indexes so identifier-shaped queries don't get diluted
// by configuration / documentation rows.
//
// One corpus per symbol — symbols never appear in two FTS5 indexes.
const (
	CorpusCode   = "code"   // source code identifiers (Function, Method, Class, etc.)
	CorpusConfig = "config" // YAML/JSON/HCL Settings, Resources, Variables, Outputs
	CorpusDocs   = "docs"   // Markdown sections, fetched URL Documents
)

// ClassifyCorpus returns the corpus name for a (language, kind) tuple. The
// rules:
//
//  1. Document kind always routes to docs (`fetch` tool stores remote URL
//     content with a `Document` kind regardless of detected language).
//  2. Markdown language → docs.
//  3. YAML / JSON / HCL / TOML → config.
//  4. Everything else (Go, Python, JS, TS, Rust, Java, Ruby, PHP, C, C++,
//     C#, Kotlin, Swift, Bash, JSX, TSX) → code.
//
// **PARITY WITH SQL:** The v9 schema migration encodes the same routing
// in three FTS5 sync triggers (sym_fts_corpus_insert/delete/update). The
// language lists in this function and in the SQL must match. The
// TestClassifyCorpus_MatchesSQLTriggerRouting parity test inserts one
// symbol per registered language and asserts the routing observed in
// SQL matches what this function says — adding a new language without
// updating both sides fails CI loudly.
//
// Returns CorpusCode for unknown languages so a missing classifier rule
// can never silently drop a symbol from search; the parity test catches
// the omission separately.
func ClassifyCorpus(language, kind string) string {
	if kind == "Document" {
		return CorpusDocs
	}
	switch language {
	case "Markdown":
		return CorpusDocs
	case "YAML", "JSON", "HCL", "TOML":
		return CorpusConfig
	default:
		return CorpusCode
	}
}

// corpusVtab maps a corpus parameter (the user-facing label) to the SQL
// vtab name used in queries.
//
// **Default flipped in #32 part 3**: empty string now routes to
// `symbols_code_fts` (the code-corpus index), not the legacy mixed
// `symbols_fts`. Pincher is a code-intelligence tool; the most common
// `search` call is for an identifier and code is the right default. Use
// "all" to explicitly hit the legacy mixed index when you want config or
// docs results in the same query.
//
// Returns an error on unknown corpus names — a typo from a caller should
// fail loudly, not silently fall through to legacy.
func corpusVtab(corpus string) (string, error) {
	switch corpus {
	case "", CorpusCode:
		return "symbols_code_fts", nil
	case "all":
		// Legacy mixed index — kept populated by the v9 triggers for
		// callers that want all corpora in one query. Slated for
		// removal in a future release; new code should use `code` /
		// `config` / `docs` instead, or run separate queries.
		return "symbols_fts", nil
	case CorpusConfig:
		return "symbols_config_fts", nil
	case CorpusDocs:
		return "symbols_docs_fts", nil
	default:
		return "", fmt.Errorf("unknown corpus %q (valid: %q, %q, %q, %q, %q)",
			corpus, "", "all", CorpusCode, CorpusConfig, CorpusDocs)
	}
}
