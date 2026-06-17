// Command gen drives the schema-agnostic graphql-opgen generator
// (github.com/trackness/graphql-opgen) over the vendored stash-box SDL, emitting
// genqlient operations and fragments, an operation manifest, and a machine-facing
// catalog. It runs as the first step of `go generate` (see generate.go), ahead of
// genqlient itself.
//
// The reusable core — the stash-box-specific genops Config, the query-only schema
// staging, and the emit pipeline — lives in the importable internal/gen/driver
// package, so the internal/conformance gate can reproduce the committed artefacts
// byte-for-byte against the exact same configuration this generator ships with.
// This main is a thin wrapper that parses flags and calls driver.Run.
//
// go-stashbox is a READ-ONLY client: genops is compiled against a
// Mutation-stripped VIEW of the vendored schema so the generated surface is
// query-only. See the driver package for the full rationale.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lightning-rider-999/go-stashbox/internal/gen/driver"
)

func main() {
	schemaDir := flag.String("schema", "schema", "vendored SDL directory")
	overlayPath := flag.String("overlay", "internal/gen/overlay.yaml", "curated overlay file")
	flag.Parse()

	if err := driver.Run(*schemaDir, *overlayPath); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "genops:", err)
		os.Exit(1)
	}
}
