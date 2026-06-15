# go-stashbox — a Go client/library & CLI for stash-box's GraphQL API

StashDB is the community-driven metadata database for Stash — a public **stash-box** instance cataloguing an adult-video universe of scenes, performers, studios, tags, and their fingerprints. This repo builds a reusable Go **client/library** for the stash-box GraphQL API (StashDB and any other instance), and a **CLI** on top. Read this whole file before touching anything here.

Refer to these as a default, instead of inferring or guessing:
- **Server software (stash-box)**: https://github.com/stashapp/stash-box
- **Public instance (StashDB)**: https://stashdb.org — GraphQL at `https://stashdb.org/graphql`, schema playground at `/playground`
- **Docs**: https://guidelines.stashdb.org/ and https://docs.stashapp.cc/

## Behaviour

@../claude-refs/behaviour.md

## The material

@../claude-refs/adult-material.md

## Voice

@../claude-refs/adult-voice.md

## Go

The bar is the most modern Go there is: `go.mod` pins the **latest stable release** (`GOTOOLCHAIN=auto` fetches it regardless of the host install; raise it when a new one lands) and the code is written in that release's current idiom, not folklore — `log/slog` for diagnostics, `slices`/`maps`/`cmp` and range-over-func iterators over hand-rolled loops, error chains with `%w`, generics where they pay their way, `tool` directives in `go.mod` (never a tools.go), `testing/synctest` for timing-dependent tests. Every change passes `go build ./...`, `go vet ./...`, `go test -race ./...`, and `golangci-lint run`; `gofmt` is law; doc comments on every package and anything exported or non-obvious.

## Engineering

- **Reach for the right library; don't hand-roll out of habit.** When a mature, well-fitting tool exists — `genqlient` for typed GraphQL, an established client, a generator off the service's own schema — default to it, and justify *not* using it rather than the reverse. Runtime dependencies have real cost in a long-lived binary, so weigh them honestly — but "fewer deps" is a tradeoff to argue out loud, never an axiom to hide behind.
- **Generate from the source of truth.** Vendor stash-box's own GraphQL SDL (from `stashapp/stash-box`, `graphql/schema/**`, stamped with the commit/version it came from) and generate the typed surface from it with `genqlient`, so a server upgrade that drifts a field is a red build, not a silent nil. Hand-written islands (write-input contracts, recursive shapes) get conformance-tested against the vendored schema.
- **Taskfile-driven gates.** `Taskfile.yml` carries the rituals — run `task --list` for the full set. The umbrella is `check` (gofmt, build, vet, `test -race`, lint, tidy, codegen-freshness); `schema` refreshes the vendored SDL after a stash-box upgrade; `vuln` runs govulncheck (stdlib CVEs matter for a static binary).

## Service specifics

- **GraphQL** at `<base>/graphql` — `STASHBOX_URL` (the base UI URL, defaults to `https://stashdb.org`) is normalised: posting GraphQL to the base returns the SPA's HTML, so append `/graphql` when the URL has no path. The schema playground lives at `<base>/playground`.
- Auth: the `STASHBOX_API_KEY` credential is sent in the `ApiKey` header — found on your user page in the stash-box instance.
- **StashDB is a shared, community-run public service, not a self-hosted box you own.** Be a scrupulously well-behaved client: respect rate limits, keep concurrency bounded, never hammer it, and no retry that masks a failure. The community pays to host it — treat it as a guest, not a private endpoint. (The same client must still work against any other stash-box, public or self-hosted.)

## Source control

@../claude-refs/adult-github-account.md
