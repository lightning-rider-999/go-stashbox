package conformance

import (
	"slices"
	"testing"

	"github.com/lightning-rider-999/go-stashbox/internal/gen/driver"
	genops "github.com/trackness/graphql-opgen"
)

// TestPathNamedAllowlistZeroDrift is the path-named gate: the audited allowlist
// (driver.Config().PathNamedAllowlist) must EXACTLY account for the set of
// path-named types the fragment generator emits over the query-only schema view —
// no more, no less.
//
// genops emits a small number of types as path-named inline selections rather
// than canonical fragment-derived named structs (junction wrappers with no id,
// union-typed fields, a result-wrapper container, terminated value-type cycles).
// Each is individually audited and recorded in the allowlist with a reason. The
// gate has two directions:
//
//   - No unlisted drift: UnlistedPathNamed is empty. A new path-named shape (a
//     stash-box upgrade that adds a junction edge, or a generator change) appears
//     here as a drift to audit and either fix or allowlist with a reason.
//   - No stale entries: every allowlist entry corresponds to a type the generator
//     actually emits path-named, so a removed shape cannot leave a dangling,
//     unexplained exemption behind.
//
// The fragment set is built from the SAME query-only schema view the generator
// compiles against (driver.StageQueryOnly), so the audited set matches exactly
// what ships — auditing the full schema would flag mutation-only shapes this
// read-only client never emits.
func TestPathNamedAllowlistZeroDrift(t *testing.T) {
	f := load(t)
	cfg := driver.Config()

	fs := genops.BuildFragments(f.querySchema)

	t.Run("no_unlisted_path_named_drift", func(t *testing.T) {
		unlisted := genops.UnlistedPathNamed(cfg, fs)
		if len(unlisted) != 0 {
			t.Errorf("UnlistedPathNamed is non-empty (generation drift): %v\n"+
				"A new path-named shape appeared in the fragment set. Audit it, then either fix the "+
				"generator or add it to driver.Config().PathNamedAllowlist with a reason.\n\n"+
				"Full audit report:\n%s", unlisted, formatLines(genops.AuditPathNamed(cfg, fs)))
		}
	})

	t.Run("every_path_named_type_is_allowlisted", func(t *testing.T) {
		allowed := make(map[string]bool)
		for _, name := range genops.AllowedPathNamed(cfg) {
			allowed[name] = true
		}
		for _, name := range fs.PathNamedTypes() {
			if !allowed[name] {
				t.Errorf("fragment set emits path-named type %q absent from the allowlist", name)
			}
		}
	})

	t.Run("no_stale_allowlist_entries", func(t *testing.T) {
		emitted := make(map[string]bool)
		for _, name := range fs.PathNamedTypes() {
			emitted[name] = true
		}
		var stale []string
		for _, name := range genops.AllowedPathNamed(cfg) {
			if !emitted[name] {
				stale = append(stale, name)
			}
		}
		slices.Sort(stale)
		if len(stale) > 0 {
			t.Errorf("%d allowlist entr(ies) name a type the generator no longer emits path-named: %v\n"+
				"Prune the stale entry from driver.Config().PathNamedAllowlist so the exemption set stays "+
				"an exact account of what the generator actually emits.", len(stale), stale)
		}
	})
}

// formatLines joins audit lines with leading indentation for a readable failure
// message.
func formatLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += "  " + l + "\n"
	}
	return out
}
