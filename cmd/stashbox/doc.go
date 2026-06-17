// Command stashbox is an agent-first, read-only command-line client for a
// stash-box GraphQL server (StashDB and any other instance). Every stash-box
// query root field is exposed as a resource-and-verb command (for example
// "stashbox scene get", "stashbox performer query"), generated from the
// vendored SDL; the command table in gen_commands.go is produced by the genops
// compiler from a query-only view of the schema.
//
// stash-box is a read-only target for this client: there are no mutations, no
// subscriptions, and no uploads, so the CLI carries none of the write-side
// machinery a read/write client would — no --wait job tracking and no
// destructive-action confirmation gate. Every command is a query.
//
// Output is machine-readable JSON by default (-o table for an aligned text
// view); variables are supplied as raw JSON through --input, so an input object
// round-trips byte-for-byte to the wire. Failures print a single-line structured
// JSON envelope on stderr and exit with a code from the frozen taxonomy in
// internal/exitcode. The embedded operation catalog is served, offline, by
// "stashbox catalog".
//
// Configuration comes from --url/--api-key or the STASHBOX_URL and
// STASHBOX_API_KEY environment variables; the URL is normalised to address the
// GraphQL endpoint (a bare base URL gains a /graphql suffix).
package main
