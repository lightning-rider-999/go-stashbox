package conformance

import (
	"os"
	"slices"
	"strings"
	"testing"

	genops "github.com/trackness/graphql-opgen"
	"github.com/vektah/gqlparser/v2/ast"
)

// queriesBaseline is the committed, sorted set of Query root-field names. It pins
// the ENTIRE command surface: stash-box exposes only queries to this read-only
// client, so the Query set IS the surface. A schema refresh that adds, removes,
// or renames any query forces a human to notice (regenerate the baseline) rather
// than letting codegen absorb it silently.
const queriesBaseline = "testdata/queries.txt"

// TestQueryRootFieldDrift is the schema-drift gate: the set of Query root fields
// in the vendored SDL must match its committed baseline exactly. The Query root
// fields are the entire generated command surface for this read-only client;
// pinning the set makes any addition, removal, or rename a deliberate, reviewed
// change and never a silent codegen absorption.
//
// The Query set is read from the FULL on-disk schema — it is identical with or
// without the Mutation strip, and pinning the vendored view keeps the baseline
// honest about what was actually vendored.
func TestQueryRootFieldDrift(t *testing.T) {
	f := load(t)

	var current []string
	for _, fd := range genops.RootFields(f.fullSchema, ast.Query) {
		current = append(current, fd.Name)
	}
	slices.Sort(current)

	baseline := readBaseline(t, queriesBaseline)
	if slices.Equal(current, baseline) {
		return
	}

	added := difference(current, baseline)
	removed := difference(baseline, current)

	var b strings.Builder
	b.WriteString("Query root-field set drifted from the committed baseline " + queriesBaseline + ".\n")
	if len(added) > 0 {
		b.WriteString("  ADDED:   " + strings.Join(added, ", ") + "\n")
	}
	if len(removed) > 0 {
		b.WriteString("  REMOVED: " + strings.Join(removed, ", ") + "\n")
	}
	b.WriteString("\nTo resolve:\n")
	b.WriteString("  1. Confirm each ADDED query is wanted and routes to a sensible command path\n")
	b.WriteString("     (add a naming rule in internal/gen/driver if it lands in the misc fallback).\n")
	b.WriteString("  2. Confirm each REMOVED query is intentionally gone (a stash-box upgrade).\n")
	b.WriteString("  3. Regenerate the baseline so it matches the new SDL:\n")
	b.WriteString("     " + queriesBaseline + " = sorted Query root-field names, one per line.\n")
	t.Fatal(b.String())
}

// TestReadOnlyInvariant is the read-only gate: the generated surface this client
// ships must contain queries and ONLY queries. Two independent checks:
//
//   - Every compiled manifest operation has Kind "query" (the generator's own
//     backstop, re-asserted here against the committed config + staging).
//   - The Mutation-stripped staged schema the generator compiles against exposes
//     no Mutation and no Subscription root field, so no write or stream operation
//     can ever be emitted into this client.
//
// A stash-box upgrade that added a query-side subscription, or a regression in
// the Mutation strip, turns this red rather than silently shipping a mutating
// command in a read-only binary.
func TestReadOnlyInvariant(t *testing.T) {
	f := load(t)

	t.Run("every_manifest_op_is_a_query", func(t *testing.T) {
		for _, e := range f.manifest.Operations {
			if e.Kind != "query" {
				t.Errorf("manifest operation %q has kind %q, want query (read-only client)", e.Name, e.Kind)
			}
		}
	})

	t.Run("staged_schema_has_no_mutation_or_subscription", func(t *testing.T) {
		if got := genops.RootFields(f.querySchema, ast.Mutation); len(got) != 0 {
			names := fieldNames(got)
			t.Errorf("query-only staged schema still exposes %d Mutation root field(s): %v\n"+
				"The Mutation strip in internal/gen/driver leaked.", len(names), names)
		}
		if got := genops.RootFields(f.querySchema, ast.Subscription); len(got) != 0 {
			names := fieldNames(got)
			t.Errorf("query-only staged schema exposes %d Subscription root field(s): %v\n"+
				"stash-box has no subscriptions; a new one needs deliberate handling.", len(names), names)
		}
	})
}

// fieldNames projects the names out of an ast.FieldList.
func fieldNames(fields ast.FieldList) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Name)
	}
	slices.Sort(out)
	return out
}

// readBaseline reads and parses a committed baseline into a sorted slice of field
// names.
func readBaseline(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading baseline %s: %v", path, err)
	}
	var names []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	slices.Sort(names)
	return names
}

// difference returns the elements of a that are not in b (both assumed sorted).
func difference(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, s := range b {
		in[s] = true
	}
	var out []string
	for _, s := range a {
		if !in[s] {
			out = append(out, s)
		}
	}
	return out
}
