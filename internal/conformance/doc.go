// Package conformance is the project's schema-drift gate: it asserts that the
// generated surface (operations, fragments, manifest, catalog, typed Go
// bindings, and the CLI command table) stays faithful to the vendored stash-box
// SDL. Any drift in the generated artefacts — a new query root field, a renamed
// type, a dropped operation, a newly path-named entity — must turn one of these
// tests red rather than slip through silently. A schema refresh that changes the
// surface is then a deliberate, reviewed step (regenerate the baselines), never a
// silent codegen absorption.
//
// go-stashbox is a READ-ONLY client, so the gates here are the read-only subset
// of the sibling read/write client's suite: completeness against the schema,
// root-field drift against a committed baseline, generation determinism,
// committed-artefact freshness, catalog coverage (including the $defs reachability
// cross-check), the audited path-named allowlist (zero drift), the query-only
// invariant (no mutation or subscription ever leaks into the generated surface),
// exit-code consistency, scalar-binding fidelity (every custom scalar binds to the
// Go type genqlient.yaml promises), and abstract-type discrimination (every
// union/interface-typed query field carries __typename plus a per-member inline
// fragment). There is no destructive-gate test and no upload/subscription coverage
// — this client has no such surface.
//
// Credential redaction is NOT a conformance gate here: it was relocated to the CLI
// (cmd/stashbox/redact.go), where it is covered by cmd/stashbox/redact_test.go —
// including a realistic queryScenes-shaped integration case. The conformance suite
// owns the generated-surface contract; redaction is a CLI output concern.
//
// All behaviour lives in the package's tests; this file carries only the package
// doc. The shared fixture (helpers_test.go) loads the schema views, overlay, and
// compiled surface once per test binary, using the same driver.Config() and
// query-only staging the generator ships with, so new gates are cheap to add and
// validate exactly what ships.
package conformance
