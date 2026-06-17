package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCatalogLoadsAndCoversEveryCommand(t *testing.T) {
	for _, spec := range generatedCommands {
		if _, ok := catalogEntry(spec.OpName); !ok {
			t.Errorf("catalog has no entry for generated command %q", spec.OpName)
		}
	}
}

func TestCatalogCommandVerbatim(t *testing.T) {
	cmd := newCatalogCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("catalog RunE: %v", err)
	}
	// The whole catalog is valid JSON with the expected top-level shape.
	var top struct {
		SchemaVersion string                     `json:"schemaVersion"`
		Commands      map[string]json.RawMessage `json:"commands"`
		Defs          map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(out.Bytes(), &top); err != nil {
		t.Fatalf("catalog output is not valid JSON: %v", err)
	}
	if top.SchemaVersion == "" {
		t.Error("catalog schemaVersion is empty")
	}
	if len(top.Commands) != len(generatedCommands) {
		t.Errorf("catalog has %d commands, command table has %d", len(top.Commands), len(generatedCommands))
	}
}

func TestCatalogSingleEntry(t *testing.T) {
	cmd := newCatalogCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{"Version"}); err != nil {
		t.Fatalf("catalog Version: %v", err)
	}
	var entry struct {
		Field string `json:"field"`
		Kind  string `json:"kind"`
	}
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatalf("entry not valid JSON: %v", err)
	}
	if entry.Field != "version" || entry.Kind != "query" {
		t.Errorf("Version entry = %+v, want field=version kind=query", entry)
	}
}

func TestCatalogUnknownEntryErrors(t *testing.T) {
	cmd := newCatalogCommand()
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.RunE(cmd, []string{"NoSuchOp"})
	if err == nil {
		t.Fatal("an unknown operation name should error")
	}
	// A mistyped op name is a caller mistake: it must classify as usage (exit 2),
	// not land on the reserved internal catch-all (exit 1).
	if got := classifyExit(err); got != ExitUsage {
		t.Errorf("classifyExit(unknown-op err) = %+v, want %+v", got, ExitUsage)
	}
}
