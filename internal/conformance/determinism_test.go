package conformance

import (
	"bytes"
	"os"
	"strconv"
	"testing"

	"github.com/lightning-rider-999/go-stashbox/internal/gen/driver"
	"github.com/lightning-rider-999/go-stashbox/schema"
	genops "github.com/trackness/graphql-opgen"
)

// Committed generated artefacts, relative to this package (internal/conformance).
// These are the exact paths driver.Run writes; the byte-for-byte gate below pins
// each one to a fresh compile so `go test` fails on a stale or hand-edited
// artefact — catching what the Taskfile's git-diff step would, but inside the
// test binary, so a contributor running plain `go test` is covered too.
const (
	committedFragmentsPath   = "../../internal/gen/generated/fragments.graphql"
	committedOperationsPath  = "../../internal/gen/generated/operations.graphql"
	committedManifestPath    = "../../internal/gen/generated/manifest.json"
	committedCatalogPath     = "../../schema/catalog.json"
	committedCLICatalogPath  = "../../cmd/stashbox/catalog.json"
	committedGenCommandsPath = "../../cmd/stashbox/gen_commands.go"
)

// compileStaged compiles the query-only surface with the shipped config and
// staging, returning the Compiled plus the EmitCommands output (the generated CLI
// command table), so both the artefact bytes and the command table can be diffed.
func compileStaged(t *testing.T) (*genops.Compiled, []byte) {
	t.Helper()
	staged, cleanup, err := driver.StageQueryOnly(schemaDir)
	if err != nil {
		t.Fatalf("stage query-only schema: %v", err)
	}
	t.Cleanup(cleanup)

	c, err := genops.Compile(staged, overlayPath, schema.SchemaVersion, driver.Config())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	commandsGo, err := genops.EmitCommands(c.Manifest, driver.Config())
	if err != nil {
		t.Fatalf("emit commands: %v", err)
	}
	return c, commandsGo
}

// TestDeterminism is the determinism gate: compiling the surface twice must yield
// byte-identical artefacts. Fragments, operations, the manifest JSON, the catalog
// JSON, and the generated command table are all checked into the repo; if
// generation were non-deterministic (map iteration order leaking into output,
// say), the committed files would churn and the freshness gate would become a
// coin flip. This pins determinism at the source.
func TestDeterminism(t *testing.T) {
	cfg := driver.Config()

	staged, cleanup, err := driver.StageQueryOnly(schemaDir)
	if err != nil {
		t.Fatalf("stage query-only schema: %v", err)
	}
	t.Cleanup(cleanup)

	first, err := genops.Compile(staged, overlayPath, schema.SchemaVersion, cfg)
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	second, err := genops.Compile(staged, overlayPath, schema.SchemaVersion, cfg)
	if err != nil {
		t.Fatalf("second compile: %v", err)
	}

	if first.Fragments != second.Fragments {
		t.Error("Fragments differ between two compiles")
	}
	if first.Operations != second.Operations {
		t.Error("Operations differ between two compiles")
	}

	if !bytes.Equal(mustJSON(t, first.Manifest), mustJSON(t, second.Manifest)) {
		t.Error("Manifest JSON differs between two compiles")
	}
	if !bytes.Equal(mustJSON(t, first.Catalog), mustJSON(t, second.Catalog)) {
		t.Error("Catalog JSON differs between two compiles")
	}

	firstCmds, err := genops.EmitCommands(first.Manifest, cfg)
	if err != nil {
		t.Fatalf("first EmitCommands: %v", err)
	}
	secondCmds, err := genops.EmitCommands(second.Manifest, cfg)
	if err != nil {
		t.Fatalf("second EmitCommands: %v", err)
	}
	if !bytes.Equal(firstCmds, secondCmds) {
		t.Error("generated command table differs between two compiles")
	}
}

// TestCommittedArtefactsAreFresh is the freshness gate: a fresh compile must
// reproduce the committed generated artefacts byte-for-byte. Determinism (above)
// proves a compile is reproducible in-process, but says nothing about whether the
// files checked into the repo actually match the current schema + overlay. This
// reads each committed file from disk and diffs it against a fresh compile, so a
// stale artefact (someone changed the SDL/overlay but forgot `task generate`) or
// a hand-edited one turns `go test` red — not only the Taskfile's git-diff step,
// which a contributor running plain `go test` would never trigger.
//
// All six committed artefacts driver.Run writes are covered, including the
// generated CLI command table (cmd/stashbox/gen_commands.go) and the embedded CLI
// catalog copy (cmd/stashbox/catalog.json).
func TestCommittedArtefactsAreFresh(t *testing.T) {
	c, commandsGo := compileStaged(t)

	manifestJSON := mustJSON(t, c.Manifest)
	catalogJSON := mustJSON(t, c.Catalog)

	cases := []struct {
		path string
		want []byte
	}{
		{committedFragmentsPath, []byte(driver.Header + c.Fragments)},
		{committedOperationsPath, []byte(driver.Header + c.Operations)},
		{committedManifestPath, manifestJSON},
		{committedCatalogPath, catalogJSON},
		// cmd/stashbox/catalog.json is a byte-identical copy of schema/catalog.json
		// (the CLI embeds it); compiling once and diffing both pins that too.
		{committedCLICatalogPath, catalogJSON},
		{committedGenCommandsPath, commandsGo},
	}

	for _, tc := range cases {
		got, err := os.ReadFile(tc.path)
		if err != nil {
			t.Errorf("reading committed artefact %s: %v", tc.path, err)
			continue
		}
		if !bytes.Equal(got, tc.want) {
			t.Errorf("committed artefact %s is stale or hand-edited: it does not match a fresh compile.\n"+
				"  committed: %d bytes\n  generated: %d bytes\n%s\n"+
				"Run `task generate` to regenerate it from the current schema + overlay.",
				tc.path, len(got), len(tc.want), firstDiff(got, tc.want))
		}
	}
}

// jsonMarshaler is the common shape of the generated artefacts that render
// themselves to deterministic JSON (Manifest, Catalog).
type jsonMarshaler interface {
	JSON() ([]byte, error)
}

// mustJSON renders a JSON artefact, failing the test on a marshal error.
func mustJSON(t *testing.T, m jsonMarshaler) []byte {
	t.Helper()
	b, err := m.JSON()
	if err != nil {
		t.Fatalf("marshalling JSON artefact: %v", err)
	}
	return b
}

// firstDiff returns a short, human-readable description of the first byte where a
// and b differ, with a little surrounding context, so a stale-artefact failure
// points at the divergence instead of dumping two large files.
func firstDiff(a, b []byte) string {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return "  first difference at byte " + strconv.Itoa(i) + ":\n" +
				"    committed: " + quoteWindow(a, i) + "\n" +
				"    generated: " + quoteWindow(b, i)
		}
	}
	if len(a) != len(b) {
		return "  one file is a prefix of the other (length differs); the shorter is truncated"
	}
	return ""
}

// quoteWindow renders up to 40 bytes of s starting at i as a quoted Go string,
// for diff context.
func quoteWindow(s []byte, i int) string {
	end := min(i+40, len(s))
	return strconv.Quote(string(s[i:end]))
}
