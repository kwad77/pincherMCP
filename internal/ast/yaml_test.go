package ast

import (
	"strings"
	"testing"
)

const yamlSrc = `services:
  web:
    image: nginx:1.25
    ports:
      - "80:80"
      - "443:443"
    environment:
      LOG_LEVEL: info
  api:
    image: myapp:latest
    replicas: 3
version: "3.8"
`

func TestExtractYAML_TopLevelKeys(t *testing.T) {
	result := Extract([]byte(yamlSrc), "YAML", "docker-compose.yml")
	if result == nil {
		t.Fatal("Extract returned nil")
	}
	byQN := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byQN[s.QualifiedName] = s
	}
	for _, want := range []string{"services", "version", "services.web", "services.web.image", "services.api.replicas"} {
		if _, ok := byQN[want]; !ok {
			t.Errorf("expected symbol qn=%q to be extracted", want)
		}
	}
}

func TestExtractYAML_ScalarSignature(t *testing.T) {
	result := Extract([]byte(yamlSrc), "YAML", "docker-compose.yml")
	byQN := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byQN[s.QualifiedName] = s
	}
	if got := byQN["services.web.image"].Signature; got != "nginx:1.25" {
		t.Errorf("services.web.image signature = %q, want %q", got, "nginx:1.25")
	}
	if got := byQN["version"].Signature; got != "3.8" {
		t.Errorf("version signature = %q, want %q", got, "3.8")
	}
}

func TestExtractYAML_AllSettingKind(t *testing.T) {
	result := Extract([]byte(yamlSrc), "YAML", "docker-compose.yml")
	for _, s := range result.Symbols {
		if s.Kind != "Setting" {
			t.Errorf("symbol %q has kind %q, want Setting", s.QualifiedName, s.Kind)
		}
	}
}

func TestExtractYAML_ConfidenceOne(t *testing.T) {
	result := Extract([]byte(yamlSrc), "YAML", "docker-compose.yml")
	for _, s := range result.Symbols {
		if s.ExtractionConfidence != 1.0 {
			t.Errorf("symbol %q confidence = %v, want 1.0", s.QualifiedName, s.ExtractionConfidence)
			break
		}
	}
}

func TestExtractYAML_ByteOffsets(t *testing.T) {
	result := Extract([]byte(yamlSrc), "YAML", "docker-compose.yml")
	src := []byte(yamlSrc)
	byQN := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byQN[s.QualifiedName] = s
		if s.StartByte >= s.EndByte {
			t.Errorf("symbol %q has start_byte(%d) >= end_byte(%d)", s.QualifiedName, s.StartByte, s.EndByte)
		}
		if s.EndByte > len(src) {
			t.Errorf("symbol %q end_byte(%d) > len(source)(%d)", s.QualifiedName, s.EndByte, len(src))
		}
	}
	// Retrieving services.web.image by byte-offset must contain "nginx:1.25"
	img := byQN["services.web.image"]
	slice := string(src[img.StartByte:img.EndByte])
	if !strings.Contains(slice, "nginx:1.25") {
		t.Errorf("services.web.image byte slice = %q, want it to contain 'nginx:1.25'", slice)
	}
	// Retrieving services.web by byte-offset must cover its full subtree
	web := byQN["services.web"]
	slice = string(src[web.StartByte:web.EndByte])
	if !strings.Contains(slice, "nginx:1.25") || !strings.Contains(slice, "LOG_LEVEL") {
		t.Errorf("services.web byte slice missing children: %q", slice)
	}
	if strings.Contains(slice, "myapp:latest") {
		t.Errorf("services.web byte slice should NOT contain api keys, got: %q", slice)
	}
}

func TestExtractYAML_Sequences(t *testing.T) {
	result := Extract([]byte(yamlSrc), "YAML", "docker-compose.yml")
	byQN := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byQN[s.QualifiedName] = s
	}
	if _, ok := byQN["services.web.ports.0"]; !ok {
		t.Error("expected sequence element services.web.ports.0")
	}
	if _, ok := byQN["services.web.ports.1"]; !ok {
		t.Error("expected sequence element services.web.ports.1")
	}
	if got := byQN["services.web.ports.0"].Signature; got != "80:80" {
		t.Errorf("ports.0 signature = %q, want '80:80'", got)
	}
}

func TestExtractYAML_MultiDoc(t *testing.T) {
	src := []byte(`apiVersion: v1
kind: Service
metadata:
  name: web
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: web-config
`)
	result := Extract(src, "YAML", "k8s.yml")
	byQN := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byQN[s.QualifiedName] = s
	}
	if _, ok := byQN["doc0.kind"]; !ok {
		t.Error("expected doc0.kind for first document")
	}
	if _, ok := byQN["doc1.kind"]; !ok {
		t.Error("expected doc1.kind for second document")
	}
	if got := byQN["doc0.kind"].Signature; got != "Service" {
		t.Errorf("doc0.kind signature = %q, want Service", got)
	}
	if got := byQN["doc1.kind"].Signature; got != "ConfigMap" {
		t.Errorf("doc1.kind signature = %q, want ConfigMap", got)
	}
}

func TestExtractYAML_AnchorsAndAliases(t *testing.T) {
	src := []byte(`defaults: &defaults
  timeout: 30
  retries: 3

production:
  <<: *defaults
  host: prod.example.com
`)
	result := Extract(src, "YAML", "config.yml")
	byQN := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byQN[s.QualifiedName] = s
	}
	if _, ok := byQN["defaults.timeout"]; !ok {
		t.Error("expected defaults.timeout (anchored)")
	}
	if _, ok := byQN["production.host"]; !ok {
		t.Error("expected production.host")
	}
}

func TestExtractYAML_KeySanitization(t *testing.T) {
	src := []byte(`key.with.dots: value
key/with/slashes: value
`)
	result := Extract(src, "YAML", "config.yml")
	byQN := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byQN[s.QualifiedName] = s
	}
	// Dots and slashes are replaced with underscores so they don't collide
	// with the dotted-path qualified name.
	if _, ok := byQN["key_with_dots"]; !ok {
		t.Errorf("expected sanitized key 'key_with_dots', got: %v", keysOf(byQN))
	}
	if _, ok := byQN["key_with_slashes"]; !ok {
		t.Errorf("expected sanitized key 'key_with_slashes', got: %v", keysOf(byQN))
	}
}

func TestExtractYAML_LongScalarTruncated(t *testing.T) {
	long := strings.Repeat("x", 500)
	src := []byte("description: " + long + "\n")
	result := Extract(src, "YAML", "doc.yml")
	for _, s := range result.Symbols {
		if s.QualifiedName == "description" {
			if len(s.Signature) != 200 {
				t.Errorf("long scalar signature length = %d, want 200", len(s.Signature))
			}
		}
	}
}

func TestExtractYAML_Empty(t *testing.T) {
	result := Extract([]byte(""), "YAML", "empty.yml")
	if result == nil {
		t.Fatal("Extract returned nil for empty input")
	}
	if len(result.Symbols) != 0 {
		t.Errorf("expected 0 symbols for empty input, got %d", len(result.Symbols))
	}
}

func TestExtractYAML_Malformed(t *testing.T) {
	// Malformed YAML should not panic; we accept whatever the parser produces.
	src := []byte("key: value\n  bad indent: foo\n[unclosed")
	result := Extract(src, "YAML", "bad.yml")
	if result == nil {
		t.Fatal("Extract returned nil for malformed YAML")
	}
	// No assertion on symbol count — just no panic.
}

func TestExtractJSON(t *testing.T) {
	src := []byte(`{
  "name": "pincherMCP",
  "version": "0.2.1",
  "scripts": {
    "build": "go build",
    "test": "go test ./..."
  },
  "keywords": ["mcp", "go", "search"]
}`)
	result := Extract(src, "JSON", "package.json")
	if result == nil {
		t.Fatal("Extract returned nil")
	}
	byQN := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byQN[s.QualifiedName] = s
	}
	if got := byQN["name"].Signature; got != "pincherMCP" {
		t.Errorf("name signature = %q, want pincherMCP", got)
	}
	if _, ok := byQN["scripts.build"]; !ok {
		t.Error("expected scripts.build")
	}
	if _, ok := byQN["keywords.0"]; !ok {
		t.Error("expected keywords.0 (sequence element)")
	}
	for _, s := range result.Symbols {
		if s.Kind != "Setting" {
			t.Errorf("JSON symbol %q kind = %q, want Setting", s.QualifiedName, s.Kind)
		}
	}
}

func TestExtractYAML_ModuleName(t *testing.T) {
	result := Extract([]byte("foo: bar\n"), "YAML", "infrastructure/ansible/site.yml")
	if result.Module != "site" {
		t.Errorf("Module = %q, want 'site'", result.Module)
	}
}

func keysOf(m map[string]ExtractedSymbol) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
