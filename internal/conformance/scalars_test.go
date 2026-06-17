package conformance

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/lightning-rider-999/go-stashbox/stashbox"
	genops "github.com/trackness/graphql-opgen"
	"github.com/vektah/gqlparser/v2/ast"
)

// TestScalarBindings is the scalar-binding gate: every custom scalar this
// read-only surface relies on must be bound by genqlient to the exact Go type
// genqlient.yaml promises, and that type must JSON round-trip. The shipped
// bindings are correct today; this gate is the regression guard a silent
// genqlient.yaml re-binding would otherwise slip past, because a re-bind still
// compiles and still passes the other conformance gates.
//
// The four custom scalars the vendored SDL declares (schema/types/misc.graphql)
// are Date, DateTime, Time, and FingerprintHash. Three of them are actually
// selected or passed by the generated query surface and are bound in
// genqlient.yaml; the fourth (DateTime) is declared but reaches no field, so it
// has no generated binding to anchor — that fact is itself asserted, so the day a
// DateTime field is introduced without a binding turns this red rather than
// shipping an unbound scalar.
//
// Each binding subtest does two things, mirroring the sibling read/write client's
// scalar gate:
//
//  1. ANCHORS the assertion to a REAL generated field whose GraphQL type is that
//     scalar, and asserts the Go type genqlient chose. This is the load-bearing
//     check: re-binding the scalar in genqlient.yaml changes the field's type and
//     fails the reflect assertion. A round-trip of a literal stdlib value alone
//     could not catch that, because it never touches the generated surface.
//  2. ROUND-TRIPS a value of the bound type through encoding/json, proving the
//     binding is JSON-marshalable in both directions.
//
// The anchor fields are named here so a reader can see which SDL field pins each
// scalar:
//
//	Time            -> EditCommentFields.Date    (EditComment.date: Time!)
//	Date            -> DateCriterionInput.Value   (DateCriterionInput.value: Date!)
//	FingerprintHash -> FingerprintFields.Hash     (Fingerprint.hash: FingerprintHash!)
func TestScalarBindings(t *testing.T) {
	t.Run("Time_binds_time.Time", func(t *testing.T) {
		assertFieldType(t, reflect.TypeOf(stashbox.EditCommentFields{}), "Date", reflect.TypeOf(time.Time{}))

		in := time.Date(2026, 6, 15, 12, 34, 56, 0, time.UTC)
		var out time.Time
		roundTrip(t, in, &out)
		if !in.Equal(out) {
			t.Errorf("Time round-trip: got %v, want %v", out, in)
		}
	})

	t.Run("Date_binds_string", func(t *testing.T) {
		// DateCriterionInput.value: Date! is non-nullable, so the field is a plain
		// string (no optional pointer). genqlient.yaml binds Date -> string so a
		// partial/zero date round-trips verbatim without timezone coercion.
		assertFieldType(t, reflect.TypeOf(stashbox.DateCriterionInput{}), "Value", reflect.TypeOf(""))

		in := "2026-06-15"
		var out string
		roundTrip(t, in, &out)
		if in != out {
			t.Errorf("Date round-trip: got %q, want %q", out, in)
		}
	})

	t.Run("FingerprintHash_binds_string", func(t *testing.T) {
		// Fingerprint.hash: FingerprintHash! is non-nullable, so the field is a
		// plain string. genqlient.yaml binds FingerprintHash -> string (an opaque
		// md5/oshash/phash hash).
		assertFieldType(t, reflect.TypeOf(stashbox.FingerprintFields{}), "Hash", reflect.TypeOf(""))

		in := "8a1b2c3d4e5f60718293a4b5c6d7e8f9"
		var out string
		roundTrip(t, in, &out)
		if in != out {
			t.Errorf("FingerprintHash round-trip: got %q, want %q", out, in)
		}
	})

	t.Run("DateTime_is_declared_but_unused", func(t *testing.T) {
		// DateTime is declared as a custom scalar in the SDL but no field, argument,
		// or input field anywhere in the vendored schema is typed DateTime, so the
		// generated surface never names it and genqlient.yaml leaves it unbound. If a
		// future schema introduces a DateTime field, this assertion flips red — the
		// signal to add an explicit genqlient.yaml binding before an unbound scalar
		// ships. (Asserting against fullSchema, the on-disk view, is the strictest
		// check: it would also catch a mutation-only DateTime field.)
		f := load(t)

		def, ok := f.fullSchema.Types["DateTime"]
		if !ok {
			t.Fatal("the SDL no longer declares the DateTime scalar; schema/types/misc.graphql is stale")
		}
		if def.Kind != ast.Scalar {
			t.Fatalf("DateTime is %s in the SDL, want a custom Scalar", def.Kind)
		}
		if users := typesReferencing(f.fullSchema, "DateTime"); len(users) != 0 {
			t.Errorf("DateTime is now used by %v; add an explicit genqlient.yaml binding "+
				"and an anchored binding subtest before an unbound scalar ships", users)
		}
	})
}

// typesReferencing returns the names of every schema type that has a field,
// argument, or input field whose base type is scalarName. It is the cross-check
// behind the "declared but unused" DateTime assertion: an empty result is the
// proof that no generated field can name the scalar.
func typesReferencing(s *ast.Schema, scalarName string) []string {
	var users []string
	for name, def := range s.Types {
		if referencesScalar(def, scalarName) {
			users = append(users, name)
		}
	}
	return users
}

// referencesScalar reports whether def has any field (or field argument, or input
// field) whose base type name is scalarName.
func referencesScalar(def *ast.Definition, scalarName string) bool {
	for _, fld := range def.Fields {
		if genops.BaseTypeName(fld.Type) == scalarName {
			return true
		}
		for _, arg := range fld.Arguments {
			if genops.BaseTypeName(arg.Type) == scalarName {
				return true
			}
		}
	}
	return false
}

// assertFieldType fails the test unless struct type st declares a field named
// field whose Go type is exactly want. The error names the actual type so a
// re-binding is obvious.
func assertFieldType(t *testing.T, st reflect.Type, field string, want reflect.Type) {
	t.Helper()
	sf, ok := st.FieldByName(field)
	if !ok {
		t.Fatalf("%s has no field %q; the generated surface changed", st.Name(), field)
	}
	if sf.Type != want {
		t.Errorf("%s.%s is bound to %s, want %s (a scalar binding changed in genqlient.yaml)", st.Name(), field, sf.Type, want)
	}
}

// roundTrip marshals in, unmarshals the bytes into out, and fails the test on any
// encoding error.
func roundTrip[T any](t *testing.T, in T, out *T) {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal %T: %v", in, err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("unmarshal into %T: %v", out, err)
	}
}
