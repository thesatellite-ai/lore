# Development

**In short:** this page is for people who change lore itself. It shows how the code is laid out, how to build and test it, and how a new version is released. If you only use lore, you do not need it.

Internal build / release notes. End-user docs live in the top-level [README](../README.md).

## How it works

| Layer | Detail |
|-------|--------|
| Storage | committed `_v: 2` JSON row files under `.lore/data/` are the truth; a SQLite cache (pure-Go `modernc.org/sqlite`, `adapters/cache/sqlite`) is built from them at run time; machine-local tables live in `~/.lore/state/machine.db` |
| Sync | `adapters/live`, `adapters/reposync` and `ops/sync` carry rows between the working tree, the live ref and the store; row merge in `logic/merge`, `logic/merge3`, `logic/canonjson`; design in [LORE_SYNC_SPEC.md](../LORE_SYNC_SPEC.md) |
| Search | SQLite FTS5 (built into the binary) + hybrid ranking |
| Output | Compiles stored knowledge into `CLAUDE.md` / `AGENTS.md` / `.cursorrules` |
| Agent loop | The lore skill (an instruction file the coding agent loads) captures corrections & decisions back into the db |

## Layout

One Go module, `github.com/thesatellite-ai/lore`, at the repo root (no `go.work`; a clean clone builds from the module proxy). The packages are layered (`model`, `schema`, `logic`, `store`, `adapters`, `op`, `engine`, `ops`, `surface`, `wire`, `cmd`); `internal/layers` fails the build when a lower layer imports a higher one.

| Path | Role |
|------|------|
| `cmd/lore` | the `lore` binary and the only release binary (package `main`, built by `task lore:build` and `.goreleaser.yml`) |
| `wire/` | the composition root: picks the concrete backends, cache, scanner and identity |
| `surface/cli`, `surface/rest`, `surface/tui`, `surface/docs` | CLI, local REST + OpenAPI (`lore api`), the TUI, and the generated reference |
| `ops/<noun>` | one package per noun, each defining its operations |
| `adapters/backend/{git,folder,rowfs}`, `adapters/cache/sqlite` | store backends and the run-time SQLite cache |
| `tests/teame2e` | the team end-to-end suite (`task lore:test:team`, build tag `teame2e`) |

The v0.1.17 modules `saas/`, `dbent/`, `lace/` and `go.work` were deleted in phase E (42dbef7a).

## Engine notes (0.1.18 core)

Every row write in the core goes through one path in `engine/`: scan, validate, policy, backend, cache, audit. Two context helpers in `engine/batch.go` change how a group of writes lands; an operation that writes many rows as one action uses them instead of its own loop of commits.

| Helper | What it does | Who uses it |
|---|---|---|
| `engine.Staging(ctx)` then `engine.Commit(ctx)` | Every engine write made with the staged `ctx` is held, not written. `Commit` writes all of them as one backend batch (one git commit on a git store), then refreshes the cache and records the audit. All or none: a failed write leaves the store as it was. A stage that touches two stores is refused with `E_INVALID_INPUT` and nothing written. A second write of the same row inside one stage is `E_INVALID_INPUT` (the stage cannot read its own rows back); a repeated create of the same row (an actor row two writes both ensure) is dropped. After `Commit` the stage is closed: a later write with the same `ctx` goes straight to the store. | `lore resource import` (FB-RES-1): the whole file is one batch, one commit |
| `engine.WithCreateID(ctx, id)` | The next `Create` of a row of `id`'s table made with `ctx` uses `id` instead of a fresh one. It runs through the ordinary `Create` path, so every guard a surface adds around `Create` still runs; creates of other tables (an actor row the write ensures) are not affected; the row must not exist yet (`E_STALE`). | `lore resource import`: each new row's id is pinned first, so references between rows of the same file resolve before any row is written (no second pass) |

`vendor/` is **not** committed; `go.sum` is. A fresh clone builds from the module proxy. The build is pure-Go: `CGO_ENABLED=0`, `modernc.org/sqlite` (FTS5 is built in — no build tag).

## Build

Requires Go 1.26+ and [Task](https://taskfile.dev/).

```sh
task lore:build          # → tmp/bin/lore (version stamped from git describe)
task lore:build:debug    # with debug symbols
task lore:install:all    # build + install binary AND the lore skill locally
task lore:skill:install  # (re)install the skill to ~/.claude/skills/lore
task lore:test           # unit + integration tests (race detector)
task lore:test:sync      # git-sync tests: row merge, the live ref, branch sync, sync operations
task lore:test:team      # team end-to-end suite (real binary, real git, many actors)
task lore:test:cover     # coverage of unit AND end-to-end tests; lists functions no test reaches
```

## The gate: `task check`, before every push

One command runs everything, in this order, and stops at the first failure:

| Step | What it proves |
|---|---|
| `lore:build` | the binary builds |
| `lore:lint` | `go vet`, `gofmt`, `staticcheck`, and a build for all six release targets (linux, darwin, windows × amd64, arm64) |
| `lore:test` | unit and integration tests with the race detector |
| `lore:test:all` | every Go test of the root module, including `cmd/lore`'s docs drift test `TestDocsMatchCLI` (every `lore …` command in the README and `skills/*.md` exists with the flags shown) |
| `lore:check:tidy` | `go mod tidy` changes nothing |
| `lore:scenarios`, `lore:chaos` | the shell acceptance and failure-injection scripts in `tests/` (SC-21 needs root or sudo and skips otherwise) |
| `lore:check:docs` | every docsync citation in the docs still matches the code (`ds check`) |

```sh
task check               # alias of task lore:check
task lore:hooks:install  # once per clone: use the committed .githooks
```

`task lore:hooks:install` points git at `.githooks/`, whose `pre-push` hook enforces the gate. A passing `task check` records the tree of the tracked files it tested (`.git/lore-check-passed`). On `git push`, if every pushed commit has exactly that tree, the push goes through at once; otherwise the hook runs `task check` first and blocks the push if it fails, or if the code it tested is not the code being pushed (uncommitted changes while checking). `git push --no-verify` skips it, for emergencies only. The hook itself is tested in `internal/repoguard/prepush_hook_test.go`.

No gate step may modify tracked files: `task check` records the tracked tree when it starts (`check:begin`) and refuses to stamp if it changed by the end, naming the files. That is why `check:docs` puts `.ds/ledger.tsv` and `refs.tsv` back after `ds scan` (whose header records the commit and time of every scan): run `ds scan` yourself and commit when docs or citations change. Uncommitted changes to tracked files also make the stamp differ from the commit being pushed, so commit or `git stash` them before pushing.

Adding a table? Declare it once with `schema.Table(...)` in `schema/` (a synced row table or a machine-local one); that one definition drives the cache tables, foreign keys, indexes, CLI flags, REST routes and the docs. There is no code generation.

Plain `go build`:

```sh
CGO_ENABLED=0 go build -o tmp/bin/lore ./cmd/lore
```

## Releases

Driven by GoReleaser (`.goreleaser.yml`) via `.github/workflows/release.yml`. Four trigger paths:

1. **push to `main`** → auto-bump patch, tag, release. Add `[skip release]` to the commit **subject** to opt out.
2. **push tag `vX.Y.Z`** → release that tag (use for minor/major bumps).
3. **workflow_dispatch with `tag=`** → create+push that tag, then release.
4. **workflow_dispatch, empty tag** → snapshot build, artifacts on the run page.

Each release cross-compiles linux/darwin/windows × amd64/arm64, bundles the binary + `README.md` + `skills/` into per-platform archives, and publishes a GitHub Release with checksums.

Validate locally before pushing:

```sh
goreleaser check
goreleaser release --snapshot --clean --skip=publish
```

GoReleaser is pinned to v2.9.0 (`.github/workflows/release.yml`, and the gate uses the same version): v2.10.0 and later deprecate `brews`, and `goreleaser check` then fails on this config. `brews` is kept on purpose (decision GR-1): a formula also serves Linux Homebrew, and the shared tap `thesatellite-ai/homebrew-tap` holds every org tool's formula. Revisit the pin when the shared tap moves to casks or another layout: migrate `.goreleaser.yml` then, and unpin.

## Homebrew tap

Formulae for all `thesatellite-ai` tools live in one shared tap repo: **`thesatellite-ai/homebrew-tap`**. GoReleaser writes `Formula/lore.rb` there on every release; users run `brew install thesatellite-ai/tap/lore`.

One-time setup (already done for the tap repo itself):

1. The tap repo `thesatellite-ai/homebrew-tap` exists and is public.
2. A Personal Access Token with `repo` scope (or a fine-grained token with contents:write on the tap repo) is stored as the **`HOMEBREW_TAP_GITHUB_TOKEN`** repository secret on `thesatellite-ai/lore`. The default `GITHUB_TOKEN` cannot push to a *different* repo, so this separate token is required.

To create the secret:

```sh
gh secret set HOMEBREW_TAP_GITHUB_TOKEN \
  --repo thesatellite-ai/lore \
  --body "<PAT with repo scope>"
```

Future projects reuse the same tap — just add a `brews:` block pointing at `thesatellite-ai/homebrew-tap` in their own `.goreleaser.yml`.

## Rebranding note

The v0.1.17 internal module names (`saas`, `dbent`, `lace`) are gone since phase E; the code is the one module `github.com/thesatellite-ai/lore`. User-facing surfaces (binary `lore`, `.lore/` data dir, `lore.db`, `LORE_*` env vars, the skill) are all branded `lore`.
