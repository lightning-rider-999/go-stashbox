package main

import (
	"strings"
	"testing"
)

// TestGeneratedCommandsAreReadOnly is the CLI-side read-only gate: every entry in
// the generated command table is a query, and none is flagged destructive or
// job-returning. go-stashbox is compiled from a query-only schema view, so this
// is the binary's own assertion that the surface it routes is read-only — a
// regression that emitted a mutation spec into the table turns this red.
func TestGeneratedCommandsAreReadOnly(t *testing.T) {
	if len(generatedCommands) == 0 {
		t.Fatal("generatedCommands is empty; the generated command table did not load")
	}
	for _, spec := range generatedCommands {
		if spec.Kind != "query" {
			t.Errorf("command %q has kind %q, want query (read-only client)", spec.OpName, spec.Kind)
		}
		if spec.Destructive {
			t.Errorf("command %q is flagged Destructive; a read-only client has no destructive ops", spec.OpName)
		}
		if spec.JobReturning {
			t.Errorf("command %q is flagged JobReturning; a read-only client has no async jobs", spec.OpName)
		}
	}
}

// TestGeneratedCommandsBindToCatalog ties the generated command table to the
// embedded catalog: every commandSpec names an operation the catalog documents,
// with a matching field and kind, and the Query const is non-empty. The two
// generated surfaces come from one genops compile, so a mismatch here would mean
// the embedded catalog drifted from the command table.
func TestGeneratedCommandsBindToCatalog(t *testing.T) {
	for _, spec := range generatedCommands {
		entry, ok := catalogEntry(spec.OpName)
		if !ok {
			t.Errorf("command %q has no catalog entry", spec.OpName)
			continue
		}
		if entry.Kind != spec.Kind {
			t.Errorf("command %q kind = %q, catalog kind = %q", spec.OpName, spec.Kind, entry.Kind)
		}
		if spec.Query == "" {
			t.Errorf("command %q has an empty Query document", spec.OpName)
		}
		// The query document names the operation, so the OpName must appear in it.
		if !strings.Contains(spec.Query, spec.OpName) {
			t.Errorf("command %q Query document does not mention the operation name", spec.OpName)
		}
	}
}

// TestGeneratedCommandPathsAreUnique asserts no two specs resolve to the same
// cobra command path. genops.BuildCommands already fails generation on a
// collision, but a hand-edit or a future generator change is caught here too.
func TestGeneratedCommandPathsAreUnique(t *testing.T) {
	seen := make(map[string]string, len(generatedCommands))
	for _, spec := range generatedCommands {
		key := strings.Join(spec.Path, " ")
		if prev, ok := seen[key]; ok {
			t.Errorf("command path %q is shared by %s and %s", key, prev, spec.OpName)
		}
		seen[key] = spec.OpName
		if len(spec.Path) < 2 {
			t.Errorf("command %q has a too-short path %v (want at least [group, leaf])", spec.OpName, spec.Path)
		}
	}
}

// TestRootCommandTreeBuilds asserts buildRootCommand assembles a tree with a
// group per Path prefix and a leaf per spec, plus the built-in catalog command.
func TestRootCommandTreeBuilds(t *testing.T) {
	root := buildRootCommand()

	// The catalog command is present.
	if _, _, err := root.Find([]string{"catalog"}); err != nil {
		t.Errorf("root tree is missing the catalog command: %v", err)
	}

	// Every generated spec resolves to a leaf at its path.
	for _, spec := range generatedCommands {
		cmd, _, err := root.Find(spec.Path)
		if err != nil {
			t.Errorf("command path %v does not resolve: %v", spec.Path, err)
			continue
		}
		if cmd.Use != spec.Path[len(spec.Path)-1] {
			t.Errorf("resolved command for %v has Use %q, want %q", spec.Path, cmd.Use, spec.Path[len(spec.Path)-1])
		}
	}
}
