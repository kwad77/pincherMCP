package db

import (
	"testing"
)

// #423 piece 2: tests for the struct_fields persistence surface and
// the pending_edges.receiver_type column. Resolver wiring lands in
// piece 3; these tests gate only the Store API.

func TestReplaceStructFieldsForFile_InsertThenReplace(t *testing.T) {
	s := newTestStore(t)
	pid := "proj1"
	_ = s.UpsertProject(testProject(pid))

	// Seed a struct symbol so the JOIN-based delete has something to
	// key off of (ReplaceStructFieldsForFile only deletes rows whose
	// struct_id matches a symbol in the named file).
	if err := s.BulkUpsertSymbols([]Symbol{
		{
			ID:            MakeSymbolID("supervisor.go", "p.Supervisor", "Class"),
			ProjectID:     pid,
			FilePath:      "supervisor.go",
			Name:          "Supervisor",
			QualifiedName: "p.Supervisor",
			Kind:          "Class",
			Language:      "Go",
		},
	}); err != nil {
		t.Fatalf("seed symbol: %v", err)
	}

	initial := []StructField{
		{ProjectID: pid, StructID: MakeSymbolID("supervisor.go", "p.Supervisor", "Class"), FieldName: "stdin", FieldType: "io.Writer"},
		{ProjectID: pid, StructID: MakeSymbolID("supervisor.go", "p.Supervisor", "Class"), FieldName: "cmd", FieldType: "*exec.Cmd"},
	}
	if err := s.ReplaceStructFieldsForFile(pid, "supervisor.go", initial); err != nil {
		t.Fatalf("first replace: %v", err)
	}

	got, err := s.LoadStructFields(pid)
	if err != nil {
		t.Fatalf("LoadStructFields: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("after first replace, len = %d, want 2", len(got))
	}

	// Re-write with a different field set — must clear old, insert new.
	replacement := []StructField{
		{ProjectID: pid, StructID: MakeSymbolID("supervisor.go", "p.Supervisor", "Class"), FieldName: "out", FieldType: "io.Writer"},
	}
	if err := s.ReplaceStructFieldsForFile(pid, "supervisor.go", replacement); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, _ = s.LoadStructFields(pid)
	if len(got) != 1 {
		t.Fatalf("after replace, len = %d, want 1 (DELETE should have cleared the old two)", len(got))
	}
	if got[0].FieldName != "out" {
		t.Errorf("after replace, FieldName = %q, want out", got[0].FieldName)
	}
}

func TestReplaceStructFieldsForFile_EmptyDeletesOnly(t *testing.T) {
	s := newTestStore(t)
	pid := "proj1"
	_ = s.UpsertProject(testProject(pid))
	_ = s.BulkUpsertSymbols([]Symbol{
		{ID: MakeSymbolID("s.go", "p.S", "Class"), ProjectID: pid, FilePath: "s.go", Name: "S", QualifiedName: "p.S", Kind: "Class", Language: "Go"},
	})
	_ = s.ReplaceStructFieldsForFile(pid, "s.go", []StructField{
		{ProjectID: pid, StructID: MakeSymbolID("s.go", "p.S", "Class"), FieldName: "f", FieldType: "int"},
	})

	if err := s.ReplaceStructFieldsForFile(pid, "s.go", nil); err != nil {
		t.Fatalf("replace empty: %v", err)
	}
	got, _ := s.LoadStructFields(pid)
	if len(got) != 0 {
		t.Errorf("after empty replace, len = %d, want 0", len(got))
	}
}

func TestDeleteSymbolsForFile_CascadesStructFields(t *testing.T) {
	s := newTestStore(t)
	pid := "proj1"
	_ = s.UpsertProject(testProject(pid))
	_ = s.BulkUpsertSymbols([]Symbol{
		{ID: MakeSymbolID("s.go", "p.S", "Class"), ProjectID: pid, FilePath: "s.go", Name: "S", QualifiedName: "p.S", Kind: "Class", Language: "Go"},
	})
	_ = s.ReplaceStructFieldsForFile(pid, "s.go", []StructField{
		{ProjectID: pid, StructID: MakeSymbolID("s.go", "p.S", "Class"), FieldName: "f", FieldType: "int"},
	})

	if err := s.DeleteSymbolsForFile(pid, "s.go"); err != nil {
		t.Fatalf("DeleteSymbolsForFile: %v", err)
	}
	got, _ := s.LoadStructFields(pid)
	if len(got) != 0 {
		t.Errorf("after symbol delete, len = %d, want 0 (cascade missed)", len(got))
	}
}

func TestPendingEdges_ReceiverTypeRoundTrip(t *testing.T) {
	s := newTestStore(t)
	pid := "proj1"
	_ = s.UpsertProject(testProject(pid))

	in := []PendingEdge{
		{ProjectID: pid, FromFile: "x.go", Kind: "CALLS", FromQN: "p.*S.M", ToName: "s.f.Write", Confidence: 0.7, ReceiverType: "*S"},
		// Plain-function call — empty ReceiverType must round-trip cleanly.
		{ProjectID: pid, FromFile: "x.go", Kind: "CALLS", FromQN: "p.Top", ToName: "helper", Confidence: 0.7},
	}
	if err := s.ReplacePendingEdgesForFile(pid, "x.go", in); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := s.LoadPendingEdges(pid, "CALLS")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	byTo := map[string]PendingEdge{}
	for _, e := range got {
		byTo[e.ToName] = e
	}
	if byTo["s.f.Write"].ReceiverType != "*S" {
		t.Errorf("method-body row ReceiverType = %q, want *S", byTo["s.f.Write"].ReceiverType)
	}
	if byTo["helper"].ReceiverType != "" {
		t.Errorf("plain-function row ReceiverType = %q, want empty", byTo["helper"].ReceiverType)
	}
}
