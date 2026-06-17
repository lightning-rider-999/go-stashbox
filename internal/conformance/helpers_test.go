package conformance

import (
	"sync"
	"testing"

	"github.com/lightning-rider-999/go-stashbox/internal/gen/driver"
	"github.com/lightning-rider-999/go-stashbox/schema"
	genops "github.com/trackness/graphql-opgen"
	"github.com/vektah/gqlparser/v2/ast"
)

// Paths to the vendored SDL and curated overlay, relative to this package's
// directory (internal/conformance).
const (
	schemaDir   = "../../schema"
	overlayPath = "../../internal/gen/overlay.yaml"
)

// fixture bundles the once-loaded schemas, overlay, and compiled surface so each
// gate can reach for whichever artefact it needs without re-parsing the SDL.
//
// There are TWO schema views, because go-stashbox is a read-only client:
//
//   - fullSchema is the vendored SDL as it sits on disk (Query + Mutation roots).
//     The root-field drift gate pins the Query root against this — the Query
//     surface is identical with or without the Mutation strip, and pinning the
//     on-disk view keeps the baseline honest about what was vendored.
//   - querySchema is the Mutation-stripped staged view the generator actually
//     compiles against (driver.StageQueryOnly). The compiled surface, manifest,
//     and catalog all derive from THIS view, so reproducing the committed
//     artefacts requires loading it.
type fixture struct {
	fullSchema  *ast.Schema
	querySchema *ast.Schema
	overlay     *genops.Overlay
	manifest    *genops.Manifest
	catalog     *genops.Catalog
	compiled    *genops.Compiled
}

var (
	loadOnce sync.Once
	loaded   *fixture
	loadErr  error
)

// load returns the shared fixture, parsing both schema views and compiling the
// query-only surface on first use. It fails the test (rather than returning a
// partial fixture) on any error, so every gate starts from a known-good baseline.
//
// The compile uses driver.Config() and driver.StageQueryOnly() — the SAME
// configuration and query-only staging the `go run ./internal/gen` generator
// ships with — so the artefacts validated here are exactly the artefacts that
// were committed, not a re-derivation that could quietly diverge.
func load(t *testing.T) *fixture {
	t.Helper()
	loadOnce.Do(func() {
		f := &fixture{}
		f.fullSchema, loadErr = genops.LoadSchema(schemaDir)
		if loadErr != nil {
			return
		}
		f.overlay, loadErr = genops.LoadOverlay(overlayPath)
		if loadErr != nil {
			return
		}

		staged, cleanup, err := driver.StageQueryOnly(schemaDir)
		if err != nil {
			loadErr = err
			return
		}
		defer cleanup()

		f.querySchema, loadErr = genops.LoadSchema(staged)
		if loadErr != nil {
			return
		}
		f.compiled, loadErr = genops.Compile(staged, overlayPath, schema.SchemaVersion, driver.Config())
		if loadErr != nil {
			return
		}
		f.manifest = f.compiled.Manifest
		f.catalog = f.compiled.Catalog
		loaded = f
	})
	if loadErr != nil {
		t.Fatalf("loading conformance fixture: %v", loadErr)
	}
	return loaded
}
