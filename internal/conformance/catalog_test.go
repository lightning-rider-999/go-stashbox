package conformance

import (
	"slices"
	"testing"

	"github.com/lightning-rider-999/go-stashbox/internal/exitcode"
	genops "github.com/trackness/graphql-opgen"
	"github.com/vektah/gqlparser/v2/ast"
)

// TestCatalogCoverage is the catalog gate: the catalog documents every operation,
// its $defs are exactly the reachable input/enum closure, its enum references
// resolve to faithful $defs enums, and deprecated fields carry the verbatim SDL
// reason.
//
//   - Every manifest operation has a Catalog.Commands[Name] entry, 1:1.
//   - catalog.Defs coincides EXACTLY with the InputObject/Enum closure reachable
//     from the query root fields' arguments (forward: no under-documentation;
//     reverse: no unreachable/stale entry), computed independently of BuildCatalog.
//   - Every enum referenced by a $defs input field's type resolves to a $defs
//     enum whose values are exactly the SDL enum's values, in the same order.
//   - A deprecated SDL root field surfaces in the catalog with its verbatim
//     @deprecated reason (the two deprecated searches, searchPerformer /
//     searchScene, exercise this).
func TestCatalogCoverage(t *testing.T) {
	f := load(t)

	t.Run("every_manifest_op_has_a_command", func(t *testing.T) {
		for _, e := range f.manifest.Operations {
			if _, ok := f.catalog.Commands[e.Name]; !ok {
				t.Errorf("manifest operation %q has no Catalog.Commands entry", e.Name)
			}
		}
		if len(f.catalog.Commands) != len(f.manifest.Operations) {
			t.Errorf("catalog commands = %d, manifest operations = %d; they must match 1:1",
				len(f.catalog.Commands), len(f.manifest.Operations))
		}
	})

	t.Run("enum_refs_resolve_and_values_match_SDL", func(t *testing.T) {
		for defName, def := range f.catalog.Defs {
			if def.Kind != "input" {
				continue
			}
			for _, fld := range def.Fields {
				base := genops.BaseTypeName(parseTypeRef(fld.Type))
				sdef, ok := f.querySchema.Types[base]
				if !ok || sdef.Kind != ast.Enum {
					continue // not an enum reference
				}
				enumDef, ok := f.catalog.Defs[base]
				if !ok {
					t.Errorf("input %q field %q references enum %q which has no $defs entry", defName, fld.Name, base)
					continue
				}
				if enumDef.Kind != "enum" {
					t.Errorf("$defs %q is referenced as an enum by %q.%q but its kind is %q", base, defName, fld.Name, enumDef.Kind)
					continue
				}
				assertEnumMatchesSDL(t, base, enumDef, sdef)
			}
		}
	})

	t.Run("defs_coincide_with_reachable_input_enum_closure", func(t *testing.T) {
		// Compute the reachable input/enum closure independently of the catalog,
		// mirroring BuildCatalog's rule (seed from each query root field's full
		// argument list, recurse through all input-object fields), so the two
		// derivations cross-check. Over the query-only view this client compiles
		// against, the closure and catalog.Defs must coincide EXACTLY:
		//   - Forward: every reachable input/enum has a $defs entry — a missing one
		//     means the catalog under-documents the input surface (a query input type
		//     dangling without a definition).
		//   - Reverse: every $defs entry is reachable — a stale/unreachable entry is a
		//     dangling definition for a type no operation argument names.
		want := reachableInputEnums(f.querySchema)

		for name := range want {
			if _, ok := f.catalog.Defs[name]; !ok {
				t.Errorf("type %q is reachable from a query root-field argument but has no catalog $defs entry", name)
			}
		}
		for name := range f.catalog.Defs {
			if !want[name] {
				t.Errorf("catalog $defs has %q but it is not reachable from any query root-field argument", name)
			}
		}
	})

	t.Run("deprecated_root_fields_carry_verbatim_reason", func(t *testing.T) {
		checked := 0
		for _, field := range genops.RootFields(f.querySchema, ast.Query) {
			want := genops.DeprecationReason(field)
			cmd, ok := f.catalog.Commands[exportNameLocal(field.Name)]
			if !ok {
				t.Errorf("root field %q has no catalog command", field.Name)
				continue
			}
			if cmd.Deprecated != want {
				t.Errorf("command %q deprecated reason = %q, want verbatim SDL reason %q",
					cmd.Field, cmd.Deprecated, want)
			}
			if want != "" {
				checked++
			}
		}
		if checked == 0 {
			t.Fatal("no deprecated query root field was found in the SDL; the fixture or schema is stale " +
				"(searchPerformer / searchScene should be deprecated)")
		}
	})
}

// TestExitCodeConsistency is the exit-code gate: the catalog's exitCodes
// vocabulary must agree with internal/exitcode, and — because this is a read-only
// client — no command may advertise a write-side exit code.
//
//   - Every code name appearing in any command's exitCodes is a real
//     internal/exitcode name. A typo or a renamed code that did not propagate
//     turns this red.
//   - Every command advertises the full Base set in order as a prefix (ok, usage,
//     auth, transport, validation, server-fault), since every operation can
//     return any of those. The optional not-found tail follows for single-entity
//     lookups.
//   - No command advertises destructive-refused, job-failed, still-running, or
//     unconfirmed — the write-side codes a read-only client never emits.
func TestExitCodeConsistency(t *testing.T) {
	f := load(t)

	known := knownExitCodeNames()
	baseNames := exitCodeNames(exitcode.Base)
	writeSide := map[string]bool{
		exitcode.DestructiveRefused.Name: true,
		exitcode.JobFailed.Name:          true,
		exitcode.StillRunning.Name:       true,
		exitcode.Unconfirmed.Name:        true,
	}

	for name, cmd := range f.catalog.Commands {
		codes := cmd.ExitCodes

		// All advertised codes are real taxonomy names.
		for _, c := range codes {
			if !known[c] {
				t.Errorf("command %q advertises exit code %q which is not an internal/exitcode name", name, c)
			}
		}

		// The Base set is the in-order prefix of every command's codes.
		if len(codes) < len(baseNames) || !slices.Equal(codes[:len(baseNames)], baseNames) {
			t.Errorf("command %q exitCodes = %v, want the Base set %v as an in-order prefix", name, codes, baseNames)
		}

		// No write-side code on any read-only command.
		for _, c := range codes {
			if writeSide[c] {
				t.Errorf("command %q advertises write-side exit code %q; this read-only client never emits it", name, c)
			}
		}

		// The only code beyond Base that may appear is not-found (a nullable
		// single-entity lookup). Anything else is unexpected for a read-only query.
		for _, c := range codes[len(baseNames):] {
			if c != exitcode.NotFound.Name {
				t.Errorf("command %q advertises unexpected non-base exit code %q (only %q may follow Base for a read-only query)",
					name, c, exitcode.NotFound.Name)
			}
		}
	}
}

// reachableInputEnums returns the names of every InputObject and Enum reachable
// from the query root fields' input surface. It mirrors BuildCatalog's
// reachability rule exactly, so the two derivations cross-check:
//
//   - The closure is SEEDED from each query root field's FULL argument list,
//     deprecated arguments included — the catalog's argDocs document every
//     argument (with a deprecation note), so $defs must resolve every type an
//     argument names, even one referenced only by a deprecated argument. Seeding
//     from non-deprecated args alone would leave that a dangling $defs reference.
//   - The closure then RECURSES through ALL input-object fields, deprecated
//     included — a deprecated input field's type is still part of that input's
//     wire shape.
//
// This is a READ-ONLY client, so only the Query root contributes fields; the
// query-only invariant (no Mutation/Subscription root) is owned by the drift gate.
func reachableInputEnums(s *ast.Schema) map[string]bool {
	seen := make(map[string]bool)

	var visit func(typeName string)
	visit = func(typeName string) {
		if seen[typeName] {
			return
		}
		def, ok := s.Types[typeName]
		if !ok {
			return
		}
		switch def.Kind {
		case ast.Enum:
			seen[typeName] = true
		case ast.InputObject:
			seen[typeName] = true
			for _, fld := range def.Fields {
				// All input-object fields, deprecated included.
				visit(genops.BaseTypeName(fld.Type))
			}
		}
	}

	for _, field := range genops.RootFields(s, ast.Query) {
		for _, arg := range field.Arguments {
			// Full argument list, deprecated included: the catalog documents and
			// resolves every argument's type.
			visit(genops.BaseTypeName(arg.Type))
		}
	}
	return seen
}

// knownExitCodeNames returns the set of every name in the frozen taxonomy.
func knownExitCodeNames() map[string]bool {
	all := []exitcode.Code{
		exitcode.OK, exitcode.Internal, exitcode.Usage, exitcode.Auth,
		exitcode.Transport, exitcode.Validation, exitcode.ServerFault,
		exitcode.NotFound, exitcode.DestructiveRefused, exitcode.JobFailed,
		exitcode.StillRunning, exitcode.Unconfirmed,
	}
	out := make(map[string]bool, len(all))
	for _, c := range all {
		out[c.Name] = true
	}
	return out
}

// exitCodeNames projects the names out of a slice of taxonomy codes, in order.
func exitCodeNames(codes []exitcode.Code) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = c.Name
	}
	return out
}

// exportNameLocal upper-cases the first rune of a camelCase root-field name to
// the exported operation name the catalog keys commands by (queryScenes ->
// QueryScenes). It mirrors genops' internal exportName, which is unexported.
func exportNameLocal(field string) string {
	if field == "" {
		return ""
	}
	r := []rune(field)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 'a' + 'A'
	}
	return string(r)
}

// assertEnumMatchesSDL fails the test unless the catalog enum's values are
// exactly the SDL enum's values, in order, with each catalog value equal to the
// SDL value name (symbol == wire value).
func assertEnumMatchesSDL(t *testing.T, name string, catEnum genops.TypeDef, sdef *ast.Definition) {
	t.Helper()
	if len(catEnum.Values) != len(sdef.EnumValues) {
		t.Errorf("enum %q: catalog has %d values, SDL has %d", name, len(catEnum.Values), len(sdef.EnumValues))
		return
	}
	for i, cv := range catEnum.Values {
		sv := sdef.EnumValues[i]
		if cv.Value != sv.Name {
			t.Errorf("enum %q value[%d] = %q, want SDL %q (symbol must equal wire value, in order)", name, i, cv.Value, sv.Name)
		}
	}
}

// parseTypeRef parses a GraphQL type reference string (as produced by
// ast.Type.String(), e.g. "[GenderEnum!]") back into an ast.Type so BaseTypeName
// can unwrap it.
func parseTypeRef(s string) *ast.Type {
	nonNull := false
	if len(s) > 0 && s[len(s)-1] == '!' {
		nonNull = true
		s = s[:len(s)-1]
	}
	if len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']' {
		return &ast.Type{Elem: parseTypeRef(s[1 : len(s)-1]), NonNull: nonNull}
	}
	return &ast.Type{NamedType: s, NonNull: nonNull}
}
