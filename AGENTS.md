# Coding Agent Guidelines

lore is one Go module, `github.com/thesatellite-ai/lore`, at the repo root: no `go.work`, no code generation, pure-Go SQLite (`modernc.org/sqlite`, `CGO_ENABLED=0`). The v0.1.17 modules `saas/`, `dbent/`, `lace/` and `go.work` were deleted in phase E (42dbef7a). Build and release details: [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).

## Layout

The packages are layered; each imports only the layers below it, and `internal/layers` fails the build when a lower layer imports a higher one.

| Layer | Package | Holds |
|-------|---------|-------|
| L0 | `model/` | typed ids, HLC, the row envelope, the closed `Value` type, typed error codes |
| L0/L1 | `schema/` | table definitions (`schema.Table(...)`): fields, relations, on-delete rules |
| L1 | `logic/` | pure rules: merge, validate, references, policy |
| L2 | `store/` | the `Backend` port and the contract suite every backend passes |
| L3 | `adapters/` | store backends (`backend/git`, `backend/folder`, `backend/rowfs`), the run-time SQLite cache (`cache/sqlite`), scanner, identity |
| L3/L4 | `op/` | the operation registry types (`op.Define[In, Out]`) |
| L4 | `engine/` | the one read/write path |
| L5 | `ops/<noun>/` | one package per noun, defining its operations |
| L6 | `surface/` | `cli`, `rest` (local `lore api`), `tui`, `docs` |
| L7 | `wire/`, `cmd/lore` | the composition root and the `lore` binary (the only release binary) |

Tests live beside each package; the team end-to-end suite is in `tests/teame2e` (build tag `teame2e`).

## Where to Make Changes

| Task | Edit |
|------|------|
| A new table or field | `schema/` (`schema.Table(...)`); the cache, CLI flags, REST routes and docs follow from it |
| A command or operation | `ops/<noun>/` (`op.Define`); CLI and REST are built from the registry at startup |
| Choosing an adapter | `wire/` only |
| Binary-only flags and startup | `cmd/lore/` |

## Task Commands

| Command | Purpose |
|---------|---------|
| `task lore:build` | build `tmp/bin/lore` |
| `task lore:test:quick` | QUICK tier: every unit test, Go test cache on, no race, no e2e — run it all the time while building |
| `task lore:test` | every package plain + the concurrent packages (`RACE_PKGS`) under the race detector |
| `task lore:test:team` | team end-to-end suite (real binary, real git) |
| `task lore:lint` | vet, gofmt, staticcheck, cross-build of the six release targets |
| `task check` | FULL tier, the gate before every push: lint, cross-builds, unit + race and every e2e suite, scheduled under a load budget with per-step wall and CPU times (`docs/DEVELOPMENT.md`) |
| `task check:fast` | FAST tier for landing a small fix (~5 min): unit, lint, race on the touched packages and the e2e steps the changed paths need (`FAST_MAP`); then `task check:after-land` runs the full gate on the landed main (`docs/DEVELOPMENT.md` "Landing") |
| `task check:after-land` | the full gate on a fresh worktree of origin/main; the result in `LORE_GATE_REPORT` (first line PASS or FAIL) |

## Critical Rules

* **Never** add `any` / `interface{}` in hand-written code except where a stdlib signature forces it (`internal/layers/noany_test.go` enforces it)
* **Never** add code generation or a `go.work`
* **Never** commit secrets; test fixtures that look like keys are built at run time
* **Always** pass `context.Context` as the first parameter to I/O functions
* **Always** wrap errors with context, or return a typed error from `model/errs`
