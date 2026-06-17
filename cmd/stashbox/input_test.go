package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newSpecCmd builds a bare leaf command for a spec with the global --input flag
// and the spec's convenience flags registered, for input-resolution tests.
func newSpecCmd(spec commandSpec) *cobra.Command {
	cmd := &cobra.Command{Use: "leaf"}
	cmd.Flags().String("input", "", "")
	cmd.Flags().String("output", "json", "")
	addConvenienceFlags(cmd, spec)
	return cmd
}

func TestResolveVariablesFromInputStdin(t *testing.T) {
	spec := commandSpec{OpName: "QueryPerformers", Kind: "query"}
	cmd := newSpecCmd(spec)
	cmd.SetIn(strings.NewReader(`{"input":{"names":"Ann","page":2}}`))
	if err := cmd.Flags().Set("input", "-"); err != nil {
		t.Fatal(err)
	}

	vars, err := resolveVariables(cmd, spec)
	if err != nil {
		t.Fatalf("resolveVariables: %v", err)
	}
	raw, ok := vars["input"]
	if !ok {
		t.Fatalf("input variable missing: %v", vars)
	}
	// The raw JSON round-trips verbatim (three-state fidelity).
	if !strings.Contains(string(raw), `"page":2`) {
		t.Errorf("input value did not round-trip: %s", raw)
	}
}

func TestConvenienceFlagBindsDeclaredArg(t *testing.T) {
	// FindScene declares an `id` argument, so --id is registered and binds.
	spec := commandSpec{OpName: "FindScene", Kind: "query"}
	cmd := newSpecCmd(spec)
	if cmd.Flags().Lookup("id") == nil {
		t.Fatal("--id should be registered for FindScene (declares id)")
	}
	if err := cmd.Flags().Set("id", "42"); err != nil {
		t.Fatal(err)
	}

	vars, err := resolveVariables(cmd, spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(vars["id"]) != `"42"` {
		t.Errorf("id var = %s, want \"42\"", vars["id"])
	}
}

func TestConvenienceFlagNotRegisteredForUndeclaredArg(t *testing.T) {
	// QueryPerformers takes only `input`; none of the scalar shorthands apply.
	spec := commandSpec{OpName: "QueryPerformers", Kind: "query"}
	cmd := newSpecCmd(spec)
	for _, f := range []string{"id", "name", "term", "limit"} {
		if cmd.Flags().Lookup(f) != nil {
			t.Errorf("--%s should not be registered for QueryPerformers (no such arg)", f)
		}
	}
}

func TestNumericConvenienceFlag(t *testing.T) {
	// SearchPerformers declares limit: Int — emitted as a bare JSON number.
	spec := commandSpec{OpName: "SearchPerformers", Kind: "query"}
	cmd := newSpecCmd(spec)
	if err := cmd.Flags().Set("limit", "10"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("term", "anal"); err != nil {
		t.Fatal(err)
	}
	vars, err := resolveVariables(cmd, spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(vars["limit"]) != "10" {
		t.Errorf("limit var = %s, want bare number 10", vars["limit"])
	}
	if string(vars["term"]) != `"anal"` {
		t.Errorf("term var = %s, want quoted string", vars["term"])
	}
}

func TestNumericConvenienceFlagRejectsNonInteger(t *testing.T) {
	spec := commandSpec{OpName: "SearchPerformers", Kind: "query"}
	cmd := newSpecCmd(spec)
	if err := cmd.Flags().Set("limit", "lots"); err != nil {
		t.Fatal(err)
	}
	_, err := resolveVariables(cmd, spec)
	if err == nil {
		t.Fatal("a non-integer --limit should be a usage error")
	}
	if classifyExit(err) != ExitUsage {
		t.Errorf("non-integer --limit should classify as usage, got %+v", classifyExit(err))
	}
}

func TestInputKeyWinsOverConvenienceFlag(t *testing.T) {
	spec := commandSpec{OpName: "FindScene", Kind: "query"}
	cmd := newSpecCmd(spec)
	cmd.SetIn(strings.NewReader(`{"id":"from-input"}`))
	if err := cmd.Flags().Set("input", "-"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("id", "from-flag"); err != nil {
		t.Fatal(err)
	}
	vars, err := resolveVariables(cmd, spec)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := json.Unmarshal(vars["id"], &got); err != nil {
		t.Fatal(err)
	}
	if got != "from-input" {
		t.Errorf("id = %q, want from-input (--input must win over --id)", got)
	}
}

func TestBadInputFileIsUsageError(t *testing.T) {
	spec := commandSpec{OpName: "FindScene", Kind: "query"}
	cmd := newSpecCmd(spec)
	if err := cmd.Flags().Set("input", "/no/such/file.json"); err != nil {
		t.Fatal(err)
	}
	_, err := resolveVariables(cmd, spec)
	if err == nil {
		t.Fatal("a missing --input file should error")
	}
	if classifyExit(err) != ExitUsage {
		t.Errorf("missing --input file should classify as usage, got %+v", classifyExit(err))
	}
}

func TestMalformedInputJSONIsUsageError(t *testing.T) {
	spec := commandSpec{OpName: "QueryPerformers", Kind: "query"}
	cmd := newSpecCmd(spec)
	cmd.SetIn(strings.NewReader(`{not json`))
	if err := cmd.Flags().Set("input", "-"); err != nil {
		t.Fatal(err)
	}
	_, err := resolveVariables(cmd, spec)
	if err == nil {
		t.Fatal("malformed --input JSON should error")
	}
	if classifyExit(err) != ExitUsage {
		t.Errorf("malformed --input should classify as usage, got %+v", classifyExit(err))
	}
}
