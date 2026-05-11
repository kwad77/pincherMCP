package server

import (
	"encoding/json"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// #558 Phase 1: parity gate. Every tool registered in s.handlers MUST
// appear in /v1/openapi.json. Pre-fix the openAPISpec hardcoded a
// 15-element slice that drifted whenever a new tool was added —
// dead_code, guide, neighborhood, init were silently invisible to
// OpenAPI consumers (Postman, Cursor, copilots) for releases.
//
// Same shape as TestStore_AllExportedMethodsClassified: when someone
// adds a new tool, the dynamic openAPISpec auto-includes it; this
// test guarantees the auto-include actually happened by re-deriving
// the expected set from s.handlers and asserting equality.
func TestOpenAPI_ParityWithRegisteredHandlers(t *testing.T) {
	srv, _, _ := newTestServer(t)

	// Expected: every handler name shows up at /v1/<name>.
	expected := map[string]bool{}
	for name := range srv.handlers {
		expected[name] = true
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/openapi.json", nil)
	srv.ServeHTTP(w, r)

	var spec map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatalf("openapi.json parse: %v", err)
	}
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("openapi.json missing paths object")
	}

	// Strip the /v1/ prefix to recover tool names.
	got := map[string]bool{}
	for path := range paths {
		if name := strings.TrimPrefix(path, "/v1/"); name != path {
			got[name] = true
		}
	}

	// Every handler must be in the spec.
	var missing []string
	for name := range expected {
		if !got[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("handlers absent from /v1/openapi.json: %v\n"+
			"openAPISpec must enumerate every name in s.handlers — pre-#558 "+
			"this slice was hardcoded and silently drifted as new tools landed.",
			missing)
	}

	// And the spec shouldn't claim a tool that doesn't exist.
	var extra []string
	for name := range got {
		// Skip the special-cased GET endpoint surfaced separately.
		if name == "health" {
			continue
		}
		if !expected[name] {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		t.Errorf("/v1/openapi.json claims paths with no registered handler: %v", extra)
	}
}

// TestOpenAPI_PerToolSchemaIsRealNotPlaceholder pins that the
// dynamic spec uses the tool's actual InputSchema (with properties,
// required fields, enums) rather than the bare {type: object}
// placeholder the pre-fix hardcoded version emitted. Catches
// regressions where InputSchema retrieval breaks silently.
func TestOpenAPI_PerToolSchemaIsRealNotPlaceholder(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/openapi.json", nil)
	srv.ServeHTTP(w, r)

	var spec map[string]any
	json.Unmarshal(w.Body.Bytes(), &spec)
	paths := spec["paths"].(map[string]any)

	// `search` is the canonical tool with a rich schema (kind, language,
	// corpus enum, limit, query). If its OpenAPI schema is bare {type:
	// object}, the InputSchema pull-through is broken.
	searchPath, ok := paths["/v1/search"].(map[string]any)
	if !ok {
		t.Fatal("/v1/search missing from openapi paths")
	}
	post := searchPath["post"].(map[string]any)
	body := post["requestBody"].(map[string]any)
	content := body["content"].(map[string]any)
	app := content["application/json"].(map[string]any)
	schema, ok := app["schema"].(map[string]any)
	if !ok {
		t.Fatal("/v1/search request schema not an object")
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		t.Errorf("/v1/search schema has no properties — InputSchema pull-through broken; got: %v", schema)
	}
	if _, hasQuery := props["query"]; !hasQuery {
		t.Errorf("/v1/search schema missing 'query' property; got props: %v", props)
	}
}
