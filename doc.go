// Package gostashbox is the module root for go-stashbox, a Go client library
// and command-line interface for stash-box's GraphQL API.
//
// stash-box (https://github.com/stashapp/stash-box) is the community metadata
// server behind StashDB (https://stashdb.org); its API is served at
// <base>/graphql and authenticated with an ApiKey header. go-stashbox works
// against StashDB (https://stashdb.org) or any other stash-box instance; the
// target URL is required and set via --url/WithURL or the STASHBOX_URL
// environment variable (there is no built-in default —
// [github.com/lightning-rider-999/go-stashbox/stashbox.NewClient] returns
// [github.com/lightning-rider-999/go-stashbox/stashbox.ErrNoURL] when none is
// configured).
//
// The reusable SDK lives in the stashbox package
// (github.com/lightning-rider-999/go-stashbox/stashbox); the agent-first CLI is
// under cmd/stashbox. The typed GraphQL surface is generated from stash-box's own
// vendored SDL (the schema/ directory, stamped with the version it came from) by
// the internal gen command, which drives the external genops compiler
// (github.com/trackness/graphql-opgen), together with genqlient, so a server
// upgrade that drifts a field is a red build rather than a silent nil.
//
// # Read-only by construction
//
// stash-box is a read-only target for this client. The codegen compiles a
// query-only view of the schema, so the generated surface carries no mutation,
// no subscription, and no upload: every operation is a query. The CLI inherits
// this — there is no --wait job tracking and no destructive-action confirmation
// gate, because no command mutates or enqueues a job. The shared exit-code
// taxonomy (internal/exitcode) keeps the write-side code names defined so the
// vocabulary matches the sibling read/write clients, but this client never emits
// them.
//
// # Generated names mirror the SDL verbatim
//
// Field and argument names in the generated surface follow stash-box's GraphQL
// SDL exactly, including its casing. genqlient maps a GraphQL name to an exported
// Go identifier by upper-casing only the first rune, so an SDL field such as
// per_page, created, or api_key becomes Per_page, Created, or Api_key rather than
// the Go-idiomatic PerPage, Created, or APIKey. This is a deliberate fidelity
// choice: keeping the generated names one-to-one with the SDL makes a server-side
// rename a compile error instead of a silent mismatch, so consumers should expect
// the non-idiomatic casing and not mistake it for an oversight.
//
// # The generated surface is public API
//
// Everything the codegen emits is part of this module's public API surface: the
// operation functions (for example
// [github.com/lightning-rider-999/go-stashbox/stashbox.FindScene]), the input and
// response types and their nested fragment types, the per-operation query-string
// constants (the *_Operation values), and the generated Get* accessor methods.
// Because that surface is regenerated from the vendored SDL, an operation that
// drifts when stash-box is upgraded — a renamed field, a changed argument, a
// removed root operation — can change or remove an exported symbol, which is a
// breaking change for code that imported it. Pinning the module pins the SDL it
// was generated against; reviewing the regenerated diff after a schema refresh is
// how such breaks are caught before release.
package gostashbox
