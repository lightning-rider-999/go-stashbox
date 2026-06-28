---
name: stashbox-query
description: >-
  Drives the read-only `stashbox` CLI to query a stash-box GraphQL instance (StashDB by default; any instance via `--url`). Use for any StashDB / stash-box lookup: resolve or dedupe a performer/scene/studio/tag, get a record by ID, search or filter+page by name, match scenes by phash/MD5 fingerprint or read fingerprint clusters, read edit/draft moderation history or the mod-audit log, check notifications, or confirm the authed user — and any time you are about to build an `--input` body, pick an enum value, or guess a field name. Catalog-first: run `stashbox catalog <Op>` before constructing input.
---

# stashbox-query

## What this is

`stashbox` is a read-only, machine-first CLI over a stash-box GraphQL instance — StashDB (`https://stashdb.org`) by default, any stash-box (public or self-hosted) via `--url`/`STASHBOX_URL`. It catalogues hardcore-porn metadata: scenes, performers, studios, tags, external sites, fingerprints, and the community moderation surface (edits, drafts, mod-audit, notifications). Every command is a GraphQL **query**, shaped `resource verb` (`scene get`, `performer search`, `edit query`). stdout carries the pretty-printed JSON result; a failure prints one compact JSON envelope to stderr and exits with a named code.

Trigger for any StashDB/stash-box lookup — resolve/dedupe a performer or scene, get a record by ID, search or filter+page by name, fingerprint matching or clusters, edit/draft/mod-audit history, notifications, "who am I" against an instance — and any time you are about to write an `--input` body or pick an enum.

The metadata is literal data: performer names, scene titles, and tag values (sex acts, body parts) are passed and reported verbatim — you can't match, log, or query a title or tag you won't name. Don't soften, abbreviate, or euphemise a value you query or surface.

**Binary:** `stashbox` on `PATH` (Homebrew), else the local build `./bin/stashbox`.

## Catalog-first — always

Before you build any `--input`, pick an enum, or assume a field exists, run the catalog. It is the build-time embedded, **offline**, self-describing contract — no network, no key needed:

```sh
stashbox catalog                  # all 37 ops: field, args (required/default), returnType, exitCodes + $defs dictionary
stashbox catalog QueryPerformers  # just one op's entry
stashbox <resource> <verb> --help # a leaf's flags + positionals
```

The catalog is the exhaustive index of the invokable surface (37 operations); `$defs` carries every input object and enum with exact field names and verbatim upper-SNAKE_CASE symbols (`FEMALE`, `INCLUDES_ALL`, `PENDING`, `PHASH`, `DATE`). If a field or symbol isn't in the catalog, it does not exist — never invent one. (The generated `docs/cli/` Markdown is non-exhaustive: it omits the two deprecated `search-one` leaves, so it is not authoritative.)

## Read-only & good guest

The typed surface is generated from a query-only view of the schema, so there is no mutation, upload, or async-job verb to find — every call is a query, idempotent, safe to repeat. In particular the `edit`/`draft` verbs READ the moderation queue; they do not write to it.

StashDB is a shared, community-run service. Keep concurrency bounded, don't hammer it, and don't wrap a failure in a blind retry — the envelope's `retryable` (true only for transient `transport` faults) tells you when a retry could even help. Failure codes are `usage`/`auth`/`transport`/`validation`/`server-fault`/`not-found`, with the exit status matching the name; the full taxonomy, the error-envelope fields, and the classification order are in `references/contract.md`.

## Variables: `--input` + convenience flags

Two ways to supply GraphQL variables, both with top-level keys = the variable names:

- **`--input`** — a JSON file path, or `-` for stdin. Verbatim passthrough: a present field stays, an omitted field stays omitted, an explicit `null` stays `null`, never round-tripped through a typed struct. Top level must be a JSON object. For a `*Query*` op it is `{"input": {…}}`; for `scene get` it is `{"id": …}`; for `list-by-fingerprints` it is `{"fingerprints": …}`.
- **Convenience flags** — `--id`, `--name`, `--username`, `--term`, `--limit`, each registered only on a leaf whose op declares that scalar. `--input` keys win; a flag only fills an arg `--input` didn't set.

Four globals inherit everywhere: `--url`/`STASHBOX_URL` (base UI URL; `/graphql` appended automatically; unset → `usage`, exit 2), `--api-key`/`STASHBOX_API_KEY` (sent in the `ApiKey` header — **prefer the env var**, a `--api-key` value leaks into `ps`/shell history), `-o/--output` (`json` default, or best-effort `table`). `-v/--version` is root-only.

Filters are **flat — one input object, an implicit AND across fields**; there is no `AND`/`OR`/`NOT` envelope and no nesting. `page`/`per_page` (default `1`/`25`) and a flat `sort` + `direction` page every `*QueryInput`. The full criterion shapes, the `CriterionModifier` set, per-entity sort defaults, and every enum live in `references/contract.md`.

## A few recipes

Shapes from the catalog (`schemaVersion v0.10.0`); not executed.

```sh
stashbox user me                                          # Me — auth check, zero args
stashbox scene get --id 8d8a4f7e-1111-2222-3333-444455556666   # FindScene
stashbox performer search --term "Riley Reid" --limit 5   # SearchPerformers
```

Scenes with all of several tags, paged — `QueryScenes` (`stashbox scene query --input f.json`):
```json
{ "input": {
  "tags": { "value": ["<tag-uuid-1>", "<tag-uuid-2>"], "modifier": "INCLUDES_ALL" },
  "page": 1, "per_page": 25, "sort": "DATE", "direction": "DESC"
} }
```
(`INCLUDES` for "any of these tags", `INCLUDES_ALL` for "all of".) For fingerprint matching, edits, studios, and performers, see the recipes in `references/contract.md`; for any op not shown, run `stashbox catalog <Op>` and build the `--input` from its `args` + `$defs` — don't guess.

## Gotchas

- **Zero-arg ops ignore `--input`** — `site query`, `site-category query`, `tag-category query`, `draft list`, `user me`, `misc version`, `config get`, `notification unread-count` take no variables. A well-formed `--input` object is silently ignored (its keys are never sent), NOT rejected; only a malformed/non-object body errors as `usage` (exit 2), and that is true of every op. Just don't pass it.
- **A `get` that matches nothing is `null` at exit 0 — not an error.** Single-entity finds are nullable, so a valid-but-missing id returns `{"find…": null}` and exits 0 (verified live), NOT `not-found`. `not-found` (exit 7) fires only when the server emits a not-found-*shaped* GraphQL error. Detect absence by testing for `null` data, never a non-zero exit.
- **`config get` is a server query** (`GetConfig` — the connected instance's public config), NOT a dump of your local CLI settings. There is no command to print the resolved URL/key.
- **`site-category get --id` is an `Int!`**, not a UUID — the only find that isn't `ID!`/`ID`.
- **`studio get` / `tag get` / `user get` are "or" lookups** — supply exactly one of `--id` / `--name` (or `--username`).
- **`scene query`'s `text` field is deprecated** — use `title`.
- **The two deprecated ops have hidden leaves** — `performer search-one` (`SearchPerformer`) and `scene search-one` (`SearchScene`) are invokable with `--term`/`--limit` but marked deprecated and hidden from the help tree (and from `docs/cli/`); the plural `search` verbs (`SearchPerformers`/`SearchScenes`) are the current path. So 37 reachable leaves in all.

## Pointers

- `references/contract.md` — the self-contained deep contract (full exit-code taxonomy, error-envelope fields + classification order, complete input/flag model, the criterion/paging/enum model, output formats, redaction, idempotency). Relies on nothing else existing.
- `stashbox catalog` / `stashbox --help` — the live, offline source of truth; outrank any doc when they disagree.
- In a repo checkout: `docs/AGENTS.md` (the machine contract) and `docs/cli/` (per-command Markdown — non-exhaustive; missing the deprecated `search-one` leaves).