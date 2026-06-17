# AGENTS.md — the machine-facing contract for the `stashbox` CLI

This file is the stable contract an agent can rely on when driving the `stashbox`
binary. It covers the **read-only surface**, the **exit-code taxonomy**, the
**error-envelope shape**, the **input model** (raw-JSON variables and convenience
flags), **enum symbols**, the **query/filter model**, **paging**, the
**output formats**, **secret redaction**, and **idempotency**.

The CLI is agent-first: stdout carries the operation's JSON result; stderr
carries a single-line JSON error envelope on failure; the process exit status is
the integer paired with the failure's code name. The code name in the envelope
and the exit integer always agree.

## Read-only by construction

go-stashbox is a **read-only** stash-box client. stash-box exposes no mutation,
no subscription, and no upload to this client: every command is a **query**. The
typed surface is generated from a query-only view of the vendored SDL, so there
is, by construction:

- **no mutation** — nothing the CLI does changes server state;
- **no `--wait` job tracking** — no operation enqueues an async job;
- **no destructive-action gate** — there is no `--yes-i-understand` flag and no
  command that can destroy data.

The taxonomy below therefore lists, but never emits, the write-side codes
(`destructive-refused`, `job-failed`, `still-running`, `unconfirmed`). They stay
defined so the vocabulary matches the sibling read/write clients; this binary has
no command path that produces them.

## Exit-code taxonomy

The (name, integer) pairs are **frozen**, defined in `internal/exitcode`. The
name is the envelope's `code` field; the integer is the process exit status.
`schema/catalog.json` lists, per command, the subset of these names it can
produce in its `exitCodes` array, so the catalog and the runtime use the same
vocabulary (the running CLI serves a byte-identical `cmd/stashbox/catalog.json`
it `//go:embed`s; `schema/catalog.json` is the canonical source that embedded
copy is generated from).

| Code name             | Exit | Emitted | When it occurs                                                                                                                  |
| --------------------- | ---: | :-----: | ------------------------------------------------------------------------------------------------------------------------------ |
| `ok`                  |    0 |   yes   | The command succeeded. No envelope is written.                                                                                 |
| `internal`            |    1 |   yes   | An unexpected, internal failure that does not fit any class below. Reserved; treat as a bug.                                   |
| `usage`               |    2 |   yes   | Bad invocation: an unknown flag, a malformed flag value, the wrong argument count, an unknown command, an unreadable `--input`, or a missing URL (no `--url`/`STASHBOX_URL`). |
| `auth`                |    3 |   yes   | Authentication or authorisation failed (missing/invalid API key; HTTP 401/403; an auth-shaped GraphQL error).                  |
| `transport`           |    4 |   yes   | The request did not get a well-formed GraphQL answer: a network failure, a cancelled context, or a non-2xx HTTP status.        |
| `validation`          |    5 |   yes   | The server executed the request but rejected the input as invalid (a GraphQL error whose message reads like input validation). |
| `server-fault`        |    6 |   yes   | The server returned a GraphQL error that is not the caller's fault and not one of the more specific classes.                   |
| `not-found`           |    7 |   yes   | The requested object does not exist (a GraphQL error whose message reads like a missing object).                               |
| `destructive-refused` |    8 |  never  | A destructive operation invoked without confirmation. Defined for vocabulary parity; this read-only client has no such path.   |
| `job-failed`          |    9 |  never  | An async job finished failed. Defined for parity; no job-returning operation exists here.                                      |
| `still-running`       |   10 |  never  | A `--wait` timed out. Defined for parity; there is no `--wait` path here.                                                      |
| `unconfirmed`         |   11 |  never  | A confirmation could not be settled. Defined for parity; no path here produces it.                                            |

Notes:

- **`1` is reserved** for an internal/unexpected failure so a genuine taxonomy
  code is never confused with a crash. Map a generic internal error to `internal`
  (exit 1), not to any class above.
- A missing-URL configuration error (no `--url` and no `STASHBOX_URL`) is the
  caller's setup mistake and classifies as `usage` (exit 2), not `internal`.

### How a server error is classified

stash-box returns GraphQL errors as plain messages without a machine code, so the
CLI buckets them by message text. The split, in priority order:

1. `auth` — the error (or the HTTP status) signals an authentication/authorisation
   failure. Wins over any GraphQL-message bucket.
2. `not-found` — a message containing "not found", "does not exist", or "no such".
3. `validation` — a message containing "validation", "invalid", "must be",
   "is required", or "cannot be null". Kept narrow so a real server fault is not
   mislabelled.
4. `server-fault` — any other server-executed GraphQL error.

A non-2xx HTTP status or a network/decode failure is `transport`, regardless of
body. This is a heuristic; if a future stash-box version adds an
`extensions.code`, the classifier can match it exactly without changing the
taxonomy.

## Error-envelope shape

On any failure the CLI writes **one compact, single-line, newline-terminated
JSON object to stderr** and exits with the code's integer. Single-line is
deliberate: read one line, parse one JSON value.

```json
{"code":"not-found","message":"stashbox: graphql error: scene not found","graphqlErrors":["scene not found"],"retryable":false}
```

Fields:

| Field           | Type     | Always present  | Meaning                                                                                                                                                              |
| --------------- | -------- | --------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `code`          | string   | yes             | The taxonomy code **name** (the table above). Equals the name of the exit integer.                                                                                   |
| `message`       | string   | yes             | Human-readable summary of the failure.                                                                                                                               |
| `graphqlErrors` | string[] | when applicable | The individual server GraphQL error messages, present only when the cause was a GraphQL error.                                                                       |
| `field`         | string   | when applicable | The offending input field, when one can be identified.                                                                                                               |
| `retryable`     | bool     | yes             | Whether retrying could plausibly succeed: `true` for a transient transport failure (network/timeout, 5xx/429), `false` for client, auth, and definite-server-answer errors. |

`graphqlErrors` and `field` are omitted when empty. `code`, `message`, and
`retryable` are always present. Every message (and each `graphqlErrors` entry) is
run through secret redaction before it is written (see **Secret redaction**).

## Input: variables and convenience flags

Operation variables come from `--input` (a JSON file path, or `-` for stdin):
a JSON object whose keys are the operation's own GraphQL variable names. stash-box
query fields take one of two shapes:

- a single `input` object — e.g. `queryPerformers(input: PerformerQueryInput!)`,
  `queryScenes(input: SceneQueryInput!)`. Pass `{"input": { ... }}`.
- a handful of scalar arguments — e.g. `findScene(id: ID!)`,
  `findTagOrAlias(name: String!)`,
  `searchPerformers(term: String!, limit: Int, page: Int, per_page: Int, filter: PerformerSearchFilter)`.
  Pass `{"id": "..."}`, `{"name": "..."}`, etc.

The CLI forwards these values as **raw JSON, never decoded through a typed Go
struct**, so an input object round-trips byte-for-byte to the wire: a present
field stays, an omitted field stays omitted, and an explicit `null` stays `null`.
The raw JSON is always authoritative.

A small set of **read-only convenience flags** is offered on a leaf, but only for
a scalar argument the operation actually declares (looked up in the embedded
catalog), so a flag can never inject an argument the operation does not accept.
They merge **under** `--input` — any key `--input` already supplies wins:

- `--id <id>` — sets `id: "<id>"` for an op declaring a scalar `id`.
- `--name <name>` — sets `name: "<name>"` for an op declaring `name`.
- `--username <username>` — sets `username: "<username>"`.
- `--term <term>` — sets `term: "<term>"` for a search op.
- `--limit <n>` — sets `limit: <n>` (a bare JSON number) for an op declaring
  `limit`; a non-integer value is a usage error (exit 2).

The dominant `input` object argument is supplied through `--input`, never a flag.

## Enum values are SDL symbols

Every GraphQL enum is sent and received as its **SDL symbol**, in upper
snake-case, never a display label or an integer. `GenderEnum`, for instance, is
one of `MALE`, `FEMALE`, `TRANSGENDER_MALE`, `TRANSGENDER_FEMALE`, `INTERSEX`,
`NON_BINARY`. A sort direction (`SortDirectionEnum`) is `ASC` or `DESC`. A filter
modifier (`CriterionModifier`) is one of `EQUALS`, `NOT_EQUALS`, `GREATER_THAN`,
`LESS_THAN`, `IS_NULL`, `NOT_NULL`, `INCLUDES_ALL`, `INCLUDES`, `EXCLUDES` — note
this is stash-box's set, which is narrower than some other GraphQL APIs (there is
no `MATCHES_REGEX`, `BETWEEN`, etc.).

The authoritative list for any enum is the embedded catalog. Run
`stashbox catalog` for the whole document, or `stashbox catalog <OperationName>`
for one operation; the `$defs` section lists every enum as

```json
"GenderEnum": {"kind": "enum", "values": [{"value": "MALE"}, {"value": "FEMALE"}, ...]}
```

and every input type's fields with their GraphQL types, so an agent can build a
valid `--input` from the catalog alone without a live server.

`stashbox catalog` is the authoritative listing of the **complete** query surface,
including any deprecated operations. Deprecated commands are omitted from the
generated per-command reference under `docs/cli/`, but they stay invokable, so the
catalog — not `docs/cli/` — is the exhaustive index of what the CLI can do.

## The query / filter model

stash-box's list queries do **not** use the two-argument page-envelope + filter
shape some GraphQL APIs do. Each list query takes **one** flat `input` object
(e.g. `PerformerQueryInput`, `SceneQueryInput`) that carries both the search
criteria and the paging/sort fields together. There is **no** `AND`/`OR`/`NOT`
boolean nesting and **no** hierarchical `depth`.

Within that input:

- A **scalar criterion** is a typed criterion input with a `value` and a
  `modifier` (a `CriterionModifier` symbol): `birth_year` is an
  `IntCriterionInput`, `country` a `StringCriterionInput`, `birthdate` a
  `DateCriterionInput`.
- A **relationship criterion** is a `MultiIDCriterionInput`: `value` (a list of
  IDs) plus a `modifier` (e.g. `tags`, `studios`, `performers` on
  `SceneQueryInput`).
- The remaining fields are plain scalars/enums applied directly (e.g.
  `gender: GenderFilterEnum`, `is_favorite: Boolean`, `studio_id: ID`).

Example: scenes tagged with any of two tags, by a given studio, sorted by date
descending, second page of 50. Save as `scene-query.json`:

```json
{
  "input": {
    "tags": { "value": ["5", "9"], "modifier": "INCLUDES" },
    "studios": { "value": ["123"], "modifier": "INCLUDES_ALL" },
    "page": 2,
    "per_page": 50,
    "sort": "DATE",
    "direction": "DESC"
  }
}
```

```sh
stashbox scene query --input scene-query.json
```

The variable name (`input`) is the operation's own GraphQL argument name;
`stashbox catalog QueryScenes` lists it and the `SceneQueryInput` field set.
Because `--input` is forwarded as raw JSON, the shape reaches the server verbatim.

## Paging and result-set size

`page` and `per_page` live on the query `input` object. In the vendored SDL they
are non-null with server defaults: **`page: Int! = 1`** and
**`per_page: Int! = 25`**. So:

- Omit them in `--input` and the server applies its defaults (page 1, 25 rows).
  A list query is paginated unless you say otherwise.
- Set `per_page` higher to pull a larger page; the field is a required `Int!`,
  and there is **no all-results sentinel** in this schema — page through with
  `page`/`per_page`.

A list query's result wrapper carries a **`count`** field (the total matching
rows, e.g. `QueryScenesResultType { count, scenes }`,
`QueryPerformersResultType { count, performers }`), so an agent can size its
paging from the first page rather than pulling everything to learn the total.

## Output formats

`--output`/`-o` selects the rendering; the set is small and machine-first:

- **`json`** (default) — the GraphQL response data, pretty-printed, always
  emitted regardless of whether stdout is a terminal. This is the agent format.
- **`table`** — a best-effort aligned text table for human eyes: one row per item
  for a list result (a column per scalar key), or a key/value table for a single
  object. Nested objects/arrays collapse to a placeholder. The machine format is
  `json`.

There is no `ndjson`/`yaml` here (this read-only surface keeps the format set
small). An unknown `--output` value is a usage error (exit 2), caught before any
request is sent.

## Secret redaction

A stash-box `User` exposes the owner's own `api_key` as a plain string
(`User.api_key`, gated by `@isUserOwner`), so a `stashbox user me` /
`stashbox user get` / `stashbox user query` response can carry the caller's own
credential. The CLI scrubs it before anything is written: the `api_key` field is
replaced with `REDACTED` in every output format, and a credential carried in a
URL query parameter (`apikey`, `api_key`, `token`, `access_token`) or in a
`user:password@host` userinfo is masked too. The same redaction runs over the
error envelope, so a credential cannot leak to stdout **or** stderr.

The `--api-key` flag is a convenience fallback; prefer the `STASHBOX_API_KEY`
environment variable, since a value passed on the command line is visible in the
process listing and shell history. cobra never echoes a string flag's value in a
usage/error message, so a malformed invocation will not leak the key.

## Idempotency

Every command is a read query, so the whole surface is **side-effect-free and
safe to repeat**: `stashbox scene get`, `stashbox performer query`,
`stashbox catalog`, and the rest can be re-run freely. There are no setter,
enqueue, or destructive operations to reason about — that machinery does not
exist in this client.
