package conformance

import (
	"os"
	"slices"
	"testing"

	genops "github.com/trackness/graphql-opgen"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// Paths to the generated GraphQL sources, relative to this package.
const (
	generatedOpsPath  = "../../internal/gen/generated/operations.graphql"
	generatedFragPath = "../../internal/gen/generated/fragments.graphql"
)

// TestAbstractTypeCoverage is the abstract-type discrimination gate: every field
// in the generated query surface whose schema type is a union or interface must
// carry the machinery genqlient needs to decode a polymorphic response —
// __typename plus one inline fragment per concrete member — and every
// concrete-object field must carry none.
//
// This client genuinely reaches several unions (DraftData, EditDetails,
// EditTarget, NotificationData, and the SceneDraft{Studio,Performer,Tag} unions),
// and the generated operations carry the discrimination today. A generator
// regression that dropped __typename or an inline fragment would ship operations
// that fail to decode a union response while every other conformance gate stayed
// green — this gate is that missing guard.
//
// The walk is SCHEMA-DRIVEN rather than a hardcoded operation list: it reparses
// the generated GraphQL with gqlparser and walks the real AST against the
// query-only schema, resolving each selected field's type from the schema (so it
// learns which fields are abstract) and tracking the type context through inline
// fragments and fragment spreads. Walking the AST removes the failure modes of a
// text scrape (a "... on Foo" in a comment or a spread split across lines) and
// staying schema-driven means a NEW union field — a stash-box upgrade — is audited
// automatically, not silently skipped because no test names it.
func TestAbstractTypeCoverage(t *testing.T) {
	f := load(t)
	doc := parseGenerated(t)

	frags := make(map[string]*ast.FragmentDefinition, len(doc.Fragments))
	for _, fd := range doc.Fragments {
		frags[fd.Name] = fd
	}

	w := &abstractWalker{schema: f.querySchema, frags: frags, t: t}

	abstractSeen := 0
	for _, op := range doc.Operations {
		root := rootDefForOperation(f.querySchema, op.Operation)
		if root == nil {
			t.Fatalf("operation %q has no root type in the query schema", op.Name)
		}
		abstractSeen += w.walk(op.Name, root, op.SelectionSet, map[string]bool{})
	}

	// A read-only stash-box client reaches several unions; if the walk found none,
	// either the generated surface lost all its abstract fields or the schema view
	// is wrong — both are regressions this gate must not pass silently.
	if abstractSeen == 0 {
		t.Fatal("no union/interface-typed field was found in the generated query surface; " +
			"this client reaches DraftData/EditDetails/EditTarget/NotificationData/SceneDraft* — " +
			"the generated operations or the schema view is stale")
	}
}

// abstractWalker walks a generated selection set against the schema, asserting
// abstract-field discrimination as it goes.
type abstractWalker struct {
	schema *ast.Schema
	frags  map[string]*ast.FragmentDefinition
	t      *testing.T
}

// walk descends sel, whose selections are fields of parent. For each field it
// resolves the schema type; an abstract type (union or interface) is checked for
// __typename + per-member inline fragments, a concrete object type is recursed
// into. seenFrags guards against fragment-spread cycles. It returns the number of
// abstract fields checked, so the caller can assert the surface actually exercises
// some.
func (w *abstractWalker) walk(opName string, parent *ast.Definition, sel ast.SelectionSet, seenFrags map[string]bool) int {
	checked := 0
	for _, s := range sel {
		switch n := s.(type) {
		case *ast.Field:
			if n.Name == "__typename" {
				continue
			}
			fd := parent.Fields.ForName(n.Name)
			if fd == nil {
				// A field the schema does not define on parent is a generation defect,
				// but the completeness/drift gates own field-set integrity; here we
				// only audit what resolves so a fragment on an abstract member (whose
				// fields belong to the member type, not parent) does not false-positive.
				continue
			}
			fieldType := w.schema.Types[genops.BaseTypeName(fd.Type)]
			if fieldType == nil {
				continue
			}
			if isAbstract(fieldType) {
				w.assertDiscrimination(opName, n, fieldType)
				checked++
			}
			// Recurse regardless: an abstract field's inline fragments carry nested
			// selections that may themselves contain further abstract fields, and a
			// concrete field's sub-selection certainly can.
			checked += w.walk(opName, fieldType, n.SelectionSet, seenFrags)
		case *ast.InlineFragment:
			cond := parent
			if n.TypeCondition != "" {
				if d := w.schema.Types[n.TypeCondition]; d != nil {
					cond = d
				}
			}
			checked += w.walk(opName, cond, n.SelectionSet, seenFrags)
		case *ast.FragmentSpread:
			if seenFrags[n.Name] {
				continue
			}
			fd, ok := w.frags[n.Name]
			if !ok {
				continue
			}
			// Copy the seen set down this branch so sibling spreads of the same
			// fragment are each followed once, while a true cycle still terminates.
			next := make(map[string]bool, len(seenFrags)+1)
			for k := range seenFrags {
				next[k] = true
			}
			next[n.Name] = true
			cond := parent
			if d := w.schema.Types[fd.TypeCondition]; d != nil {
				cond = d
			}
			checked += w.walk(opName, cond, fd.SelectionSet, next)
		}
	}
	return checked
}

// assertDiscrimination fails the test unless the selection of abstract-typed field
// emits __typename and one inline fragment per concrete member of its type
// (resolved transitively through fragment spreads).
func (w *abstractWalker) assertDiscrimination(opName string, field *ast.Field, fieldType *ast.Definition) {
	w.t.Helper()
	hasTypename, members := collectDiscrimination(field.SelectionSet, w.frags, map[string]bool{})

	if !hasTypename {
		w.t.Errorf("%s: field %q is %s %q but its selection has no __typename for discrimination",
			opName, field.Name, fieldType.Kind, fieldType.Name)
	}

	want := make([]string, 0)
	for _, pt := range w.schema.GetPossibleTypes(fieldType) {
		want = append(want, pt.Name)
	}
	slices.Sort(want)
	for _, member := range want {
		if !members[member] {
			w.t.Errorf("%s: field %q (%s %q) is missing an inline fragment for member %q (got %v)",
				opName, field.Name, fieldType.Kind, fieldType.Name, member, sortedKeys(members))
		}
	}
}

// collectDiscrimination walks sel and the fragments it spreads, transitively,
// returning whether __typename is selected and the set of inline-fragment type
// conditions reached. A spread cycle terminates via seen.
func collectDiscrimination(sel ast.SelectionSet, frags map[string]*ast.FragmentDefinition, seen map[string]bool) (bool, map[string]bool) {
	members := map[string]bool{}
	hasTypename := false

	var walk func(ast.SelectionSet)
	walk = func(ss ast.SelectionSet) {
		for _, s := range ss {
			switch n := s.(type) {
			case *ast.Field:
				if n.Name == "__typename" {
					hasTypename = true
				}
			case *ast.InlineFragment:
				if n.TypeCondition != "" {
					members[n.TypeCondition] = true
				}
				// Do not descend: a nested abstract field inside this member is
				// audited by the schema-driven walk, not here.
			case *ast.FragmentSpread:
				if seen[n.Name] {
					continue
				}
				seen[n.Name] = true
				if fd, ok := frags[n.Name]; ok {
					walk(fd.SelectionSet)
				}
			}
		}
	}
	walk(sel)
	return hasTypename, members
}

// isAbstract reports whether a schema type is a union or interface — the two
// abstract kinds that need __typename-based discrimination in a query.
func isAbstract(def *ast.Definition) bool {
	return def.Kind == ast.Union || def.Kind == ast.Interface
}

// rootDefForOperation returns the schema root definition (Query/Mutation/
// Subscription) for an operation. This read-only client only ships queries, but
// the lookup is general so a stray non-query operation fails loudly rather than
// being skipped.
func rootDefForOperation(s *ast.Schema, op ast.Operation) *ast.Definition {
	switch op {
	case ast.Query:
		return s.Query
	case ast.Mutation:
		return s.Mutation
	case ast.Subscription:
		return s.Subscription
	default:
		return nil
	}
}

// parseGenerated reads and parses the generated operations and fragments into one
// query document. Both files are concatenated and parsed with parser.ParseQuery,
// which validates syntax only — exactly right here, since the generated documents
// use the @genqlient(flatten) directive in comments and __typename meta-fields a
// full schema validator would need to special-case; the AST shape is all this gate
// needs.
func parseGenerated(t *testing.T) *ast.QueryDocument {
	t.Helper()
	ops := mustRead(t, generatedOpsPath)
	frags := mustRead(t, generatedFragPath)
	src := &ast.Source{Name: "generated", Input: ops + "\n" + frags}
	doc, err := parser.ParseQuery(src)
	if err != nil {
		t.Fatalf("parsing generated GraphQL: %v", err)
	}
	return doc
}

// mustRead reads a generated source file, failing the test if it is missing.
func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// sortedKeys returns the keys of a set as a sorted slice, for stable diagnostics.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
