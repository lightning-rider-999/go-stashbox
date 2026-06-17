package conformance

import (
	"testing"

	genops "github.com/trackness/graphql-opgen"
	"github.com/vektah/gqlparser/v2/ast"
)

// wantQueryRootFields is the expected number of Query root fields (and, since
// this is a read-only client, of generated operations). It is asserted directly
// so a schema refresh that changes the count fails loudly here as well as in the
// drift gate — a second, blunt tripwire on the surface size.
const wantQueryRootFields = 37

// TestCompleteness is the coverage gate: every Query root field in the staged
// (query-only) SDL must be covered by exactly one manifest operation, and every
// manifest operation must name a real Query root field. A new query on a
// stash-box upgrade that the generator does not pick up shows up here as an
// uncovered field; a stale manifest entry shows up as one naming a field that no
// longer exists.
func TestCompleteness(t *testing.T) {
	f := load(t)

	// Count how many manifest operations claim each schema root field.
	covered := make(map[string]int)
	for _, e := range f.manifest.Operations {
		covered[e.Field]++
	}

	// Collect every Query root field the staged SDL defines. The staged view has
	// no Mutation/Subscription (TestReadOnlyInvariant pins that), so the Query
	// fields are the whole surface.
	var rootFields []string
	for _, fd := range genops.RootFields(f.querySchema, ast.Query) {
		rootFields = append(rootFields, fd.Name)
	}

	if len(rootFields) != wantQueryRootFields {
		t.Errorf("staged Query root fields = %d, want %d (schema drift: a query was added or removed)",
			len(rootFields), wantQueryRootFields)
	}

	var uncovered, doubled []string
	for _, name := range rootFields {
		switch covered[name] {
		case 1:
			// Exactly one manifest operation — the required state.
		case 0:
			uncovered = append(uncovered, name)
		default:
			doubled = append(doubled, name)
		}
	}

	if len(uncovered) > 0 {
		t.Errorf("%d Query root field(s) have no manifest operation: %v", len(uncovered), uncovered)
	}
	if len(doubled) > 0 {
		t.Errorf("%d Query root field(s) are covered by more than one manifest operation: %v", len(doubled), doubled)
	}

	// Inverse direction: every manifest operation must name a real root field, so
	// a stale entry referencing a removed field cannot linger.
	rootSet := make(map[string]bool, len(rootFields))
	for _, name := range rootFields {
		rootSet[name] = true
	}
	for _, e := range f.manifest.Operations {
		if !rootSet[e.Field] {
			t.Errorf("manifest operation %q names field %q which is not a Query root field", e.Name, e.Field)
		}
	}

	if len(f.manifest.Operations) != wantQueryRootFields {
		t.Errorf("manifest operations = %d, want %d", len(f.manifest.Operations), wantQueryRootFields)
	}
}
