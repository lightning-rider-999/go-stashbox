# go-stashbox

A Go client library and an agent-first command-line client for
[stash-box](https://github.com/stashapp/stash-box)'s GraphQL API —
[StashDB](https://stashdb.org) and any other stash-box instance.

This is a **read-only** client. The generated surface is query-only (the
Mutation root is stripped before code generation), there are no subscriptions,
and no operation sends an upload — it queries scenes, performers, studios, tags,
sites, edits, drafts, notifications, and their fingerprints, and reports the
server's version.

The repository ships two things from one typed surface:

- **`stashbox`** — a reusable SDK
  (`github.com/lightning-rider-999/go-stashbox/stashbox`): a configured `Client`
  that injects the `ApiKey` header and normalises the endpoint, generated typed
  operations for every stash-box query root field, a typed error model, and a
  bounded-concurrency batching helper.
- **`cmd/stashbox`** — a CLI that exposes every query as a resource-and-verb
  command (`stashbox scene get`, `stashbox performer query`), with
  machine-readable output and a frozen exit-code taxonomy.

The whole typed surface is generated from stash-box's own GraphQL SDL, vendored
under `schema/` and stamped with the release it came from (currently
**v0.10.0**), so a server upgrade that drifts a field is a red build rather than
a silent nil.

## Install

**With Go** (the recommended path for a Go CLI):

```sh
go install github.com/lightning-rider-999/go-stashbox/cmd/stashbox@latest
```

**Linux/macOS without Go** — download and install a prebuilt binary. The
installer detects your OS/arch, verifies the release's sha256 checksum, and
installs to a directory on your PATH:

```sh
curl -sSL https://raw.githubusercontent.com/lightning-rider-999/go-stashbox/main/install.sh | sh
```

Override the target directory or pin a version with environment variables:

```sh
# Install into ~/.local/bin instead of the default (/usr/local/bin):
curl -sSL https://raw.githubusercontent.com/lightning-rider-999/go-stashbox/main/install.sh | INSTALL_DIR="$HOME/.local/bin" sh

# Pin a specific release tag instead of the latest:
curl -sSL https://raw.githubusercontent.com/lightning-rider-999/go-stashbox/main/install.sh | VERSION=v1.2.3 sh
```

**Homebrew (macOS / Linux):**

```sh
brew install lightning-rider-999/tap/stashbox
```

The fully-qualified `owner/tap/name` auto-taps
[`lightning-rider-999/homebrew-tap`](https://github.com/lightning-rider-999/homebrew-tap)
and trusts that one cask, so no separate `brew tap` is needed. (Homebrew 6.0.0 —
or 5.2.0, whichever lands first — will require explicit trust for non-official
taps; a fully-qualified install grants it for this cask only.) The cask is
published automatically by the release pipeline.

**Manual** — download the archive for your platform from the
[Releases page](https://github.com/lightning-rider-999/go-stashbox/releases),
verify it against `checksums.txt`, then extract the `stashbox` binary onto your
PATH:

```sh
tar -xzf stashbox_<version>_<os>_<arch>.tar.gz
install -m 0755 stashbox /usr/local/bin/stashbox
```

**From a checkout:**

```sh
go build -o bin/stashbox ./cmd/stashbox
```

**As a library** in another project:

```sh
go get github.com/lightning-rider-999/go-stashbox/stashbox
```

## Configure

The CLI and the SDK both read two environment variables:

| Variable            | Purpose                                                                                                                            |
|---------------------|----------------------------------------------------------------------------------------------------------------------------------|
| `STASHBOX_URL`      | Base UI URL of the stash-box instance, e.g. `https://stashdb.org`. GraphQL is served at `<url>/graphql`; the URL is normalised, so the base URL is enough. Required — the client errors if neither this nor `--url` is set. |
| `STASHBOX_API_KEY`  | API key, sent in the `ApiKey` header — found on your user page in the stash-box instance. Optional for an instance that allows unauthenticated read access. |

```sh
export STASHBOX_URL="https://stashdb.org"
export STASHBOX_API_KEY="your-api-key"
```

The CLI's `--url` and `--api-key` flags override the variables per invocation.
Prefer the environment variable for the key: a `--api-key` value is visible to
other users via the process listing and is written into shell history.

> **StashDB is a shared, community-run public service.** Be a well-behaved
> client: respect rate limits, keep concurrency bounded, and don't hammer it.

## CLI quickstart

```sh
# Look up one performer by id (a stash-box UUID), via the --id convenience flag.
stashbox performer get --id <uuid>

# Query scenes, reading the operation's variables from a JSON file.
stashbox scene query --input scene-query.json

# Ask the server for its build identity.
stashbox misc version

# Inspect an operation's inputs and return type without a server.
stashbox catalog QueryScenes
```

Output defaults to JSON; `-o table` selects a compact tabular view. Operation
variables come from `--input` (a JSON file path, or `-` for stdin) and are
forwarded as raw JSON, which preserves the present / absent / null distinction.
Convenience flags fill in an operation's scalar arguments (such as `--id`)
without writing a JSON file. On any failure the CLI writes a single-line JSON
error envelope to stderr and exits with the matching taxonomy integer.

`stashbox catalog` lists the complete operation surface — every command, its
inputs, its return type, and whether it is deprecated. Deprecated commands stay
invokable (with a deprecation warning).

This CLI is read-only by construction: there are no mutations, no async jobs,
and no destructive-confirmation gate.

## Driving it from Claude Code

The repository ships a [Claude Code](https://claude.com/claude-code) skill at
`.claude/skills/stashbox-query/` so an agent drives the CLI correctly instead of
guessing field names, enum symbols, or a mutation that does not exist. It is
**self-contained and binary-first**: it treats the installed binary's own
self-description (`stashbox catalog`, `stashbox <cmd> --help`) as the source of
truth, so it works whether `stashbox` is on `PATH` (Homebrew/`go install`) or
built locally to `./bin/stashbox`.

- `SKILL.md` — the orientation: the catalog-first discipline (consult
  `stashbox catalog <Op>` before building any `--input` or choosing an enum), the
  read-only model, the `--input` + convenience-flag and flat-filter/paging model,
  worked recipes, and the gotchas that bite.
- `references/contract.md` — the self-contained deep contract: the full exit-code
  taxonomy and single-line error-envelope shape, the criterion/enum model with
  every symbol verbatim, the output formats, and secret redaction. It depends on
  nothing else existing.

It triggers on any StashDB / stash-box lookup. For an install that has the CLI
but not this checkout, the sibling skill-only plugin in
[lightning-rider-plugins](https://github.com/lightning-rider-999/lightning-rider-plugins)
serves the same contract from the marketplace.

## Library quickstart

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/lightning-rider-999/go-stashbox/stashbox"
)

func main() {
	// URL and API key fall back to STASHBOX_URL / STASHBOX_API_KEY. The base
	// UI URL is enough; it is normalised to address <url>/graphql.
	c, err := stashbox.NewClient(stashbox.WithURL("https://stashdb.org"))
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	// The version handshake: ask the server for its build identity.
	info, err := c.Version(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("stash-box %s (%s)\n", info.Version, info.Hash)

	// A generated operation. Pass c.GraphQL() as the client; the id is the
	// operation's own typed input (a stash-box UUID).
	resp, err := stashbox.FindPerformer(ctx, c.GraphQL(), "performer-uuid")
	if err != nil {
		log.Fatal(err)
	}
	if resp.FindPerformer != nil {
		fmt.Println(resp.FindPerformer.Id, resp.FindPerformer.Name)
	}
}
```

The SDK also offers `stashbox.Batch` / `stashbox.BatchResults` for
bounded-concurrency fan-out (no retry, by design — a well-behaved client against
a shared service must not mask a failure), `c.CheckCompatibility` to compare the
server's release against the schema this library was generated against, and a
typed error model (`*stashbox.GraphQLError`, `*stashbox.TransportError`,
`stashbox.ErrUnauthorized`, classified by `stashbox.Classify`). See the runnable
examples in `stashbox/example_test.go` and the package documentation
(`go doc github.com/lightning-rider-999/go-stashbox/stashbox`).

## How generation works

The typed surface is built by two steps, run in order by `go generate`:

1. **`internal/gen`** drives the schema-agnostic `graphql-opgen` GraphQL
   operation generator over the vendored SDL under `schema/`, supplying the
   stash-box-specific config. It emits the genqlient operations and fragments,
   the operation manifest, the CLI command table
   (`cmd/stashbox/gen_commands.go`), and the machine-facing catalog. The schema
   is reduced to its query-only view here, which is what makes this client
   read-only.
2. **genqlient** turns those operations and fragments into the typed Go client
   (`stashbox/operations_gen.go`).

Regenerate everything:

```sh
task generate    # runs `go generate ./...`
```

After a stash-box upgrade, refresh the vendored SDL to a new pinned release and
re-stamp the version. `task schema` regenerates the typed surface as its final
step, so no separate `task generate` is needed:

```sh
task schema      # refresh schema/ at the tag in schema/version.txt, then regenerate
```

Hand-written islands (the recursive filter shapes and scalar bindings) are
conformance-tested against the vendored schema under `internal/conformance`, so
drift between the hand-written and generated surfaces turns a test red.

## Quality gates

Every change is expected to pass the gates in the `Taskfile.yml` (run
`task --list` for the full set):

```sh
task check    # gofmt, build, vet, test -race, lint, tidy, codegen freshness
task vuln     # govulncheck (stdlib + dependency CVEs)
```

`task check`'s codegen-freshness step regenerates the typed surface and fails if
any committed generated artifact changed, so the vendored schema and the typed
client can never silently drift apart.

## Related projects

- [**go-stash**](https://github.com/lightning-rider-999/go-stash) — the sibling
  client/library and CLI for [Stash](https://github.com/stashapp/stash)'s own
  GraphQL API. Same agent-first shape (typed surface generated from the upstream
  SDL, machine-readable output, frozen exit codes); where go-stashbox talks to a
  stash-box metadata instance, go-stash drives your local Stash server.
- [**lightning-rider-plugins**](https://github.com/lightning-rider-999/lightning-rider-plugins)
  — a Claude Code plugin marketplace. Its stashbox plugin is a thin, skill-only
  contract that drives this CLI from an agent: it discovers the live operation
  surface from the installed `stashbox` binary (`stashbox catalog`) rather than a
  snapshot that drifts.

## License

[MIT](LICENSE).
