# Development

Internal build / release notes. End-user docs live in the top-level [README](../README.md).

## How it works

| Layer | Detail |
|-------|--------|
| Storage | SQLite cache `.lore/lore.db` (pure-Go `modernc.org/sqlite`) mirrored to committed JSON row files under `.lore/data/` |
| Sync | `saas/pkg/aicoder/lsync` reconciles both directions around every command; design in [LORE_SYNC_SPEC.md](../LORE_SYNC_SPEC.md) |
| Search | SQLite FTS5 (built into the binary) + hybrid ranking |
| Output | Compiles stored knowledge into `CLAUDE.md` / `AGENTS.md` / `.cursorrules` |
| Agent loop | Claude skill captures corrections & decisions back into the db |

## Layout

This is a Go **workspace** (`go.work`) stitching local modules:

| Module | Role |
|--------|------|
| `saas/cmd/cli` | the `lore` binary (package `main`) |
| `saas/pkg/aicoder/*` | core domain logic (capture, search, render, fts5) |
| `saas/pkg/aicoder/lsync` | git sync engine: table registry, change triggers, reconcile, bootstrap/adopt, merge driver, trash/conflicts |
| `saas/pkg/aicoder/{canonjson,merge3,gitsetup}` | generic helpers sync is built on: byte-stable JSON, field-level 3-way merge, idempotent git wiring + plumbing |
| `dbent` | Ent schema + generated client (local-only module) |
| `lace/db` | pure-Go SQLite open/registration |
| `github.com/khanakia/entx/enttui` | TUI engine (resolved from the module proxy) |

`vendor/` is **not** committed; `go.sum` is. A fresh clone builds from the module proxy. The build is pure-Go: `CGO_ENABLED=0`, `modernc.org/sqlite` (FTS5 is built in — no build tag).

## Build

Requires Go 1.26+ and [Task](https://taskfile.dev/).

```sh
task lore:build          # → tmp/bin/lore (version stamped from git describe)
task lore:build:debug    # with debug symbols
task lore:install:all    # build + install binary AND the Claude skill locally
task lore:skill:install  # (re)install the skill to ~/.claude/skills/lore
task lore:test           # unit + integration tests (race detector)
task lore:test:sync      # sync engine + CLI end-to-end suite (real git, real binary)
task lore:test:cover     # coverage of unit AND end-to-end tests; lists functions no test reaches
task lore:bench:sync     # sync performance at 1k / 10k rows
```

## The gate: `task check`, before every push

One command runs everything, in this order, and stops at the first failure:

| Step | What it proves |
|---|---|
| `lore:build` | the binary builds |
| `lore:lint` | `go vet`, `gofmt`, `staticcheck`, and a build for all six release targets (linux, darwin, windows × amd64, arm64) |
| `lore:test` | unit and integration tests with the race detector |
| `lore:test:all` | every Go test in `dbent`, `lace` and `saas`, including the CLI end-to-end suite (real git, real binary) and `TestDocsMatchCLI` (every `lore …` command in the README and `skills/*.md` exists with the flags shown) |
| `lore:check:tidy` | `go mod tidy` changes nothing |
| `lore:scenarios`, `lore:chaos` | the shell acceptance and failure-injection scripts in `tests/` (SC-21 needs root or sudo and skips otherwise) |
| `lore:check:docs` | every docsync citation in the docs still matches the code (`ds check`) |

```sh
task check               # alias of task lore:check
task lore:hooks:install  # once per clone: use the committed .githooks
```

`task lore:hooks:install` points git at `.githooks/`, whose `pre-push` hook enforces the gate. A passing `task check` records the tree of the tracked files it tested (`.git/lore-check-passed`). On `git push`, if every pushed commit has exactly that tree, the push goes through at once; otherwise the hook runs `task check` first and blocks the push if it fails, or if the code it tested is not the code being pushed (uncommitted changes while checking). `git push --no-verify` skips it, for emergencies only. The hook itself is tested in `saas/cmd/cli/prepush_hook_test.go`.

No gate step may modify tracked files: `task check` records the tracked tree when it starts (`check:begin`) and refuses to stamp if it changed by the end, naming the files. That is why `check:docs` puts `.ds/ledger.tsv` and `refs.tsv` back after `ds scan` (whose header records the commit and time of every scan): run `ds scan` yourself and commit when docs or citations change. Uncommitted changes to tracked files also make the stamp differ from the commit being pushed, so commit or `git stash` them before pushing.

Adding an ent table? `saas/pkg/aicoder/lsync/registry.go` must classify it as synced or local — `TestRegistry_EveryTableClassified` fails until it does. A new unique index on a synced table also needs an entry in `saas/cmd/cli/natural_ids.go` (`TestNaturalIDSpecs_MatchRegistry`).

Plain `go build`:

```sh
CGO_ENABLED=0 go build -o tmp/bin/lore ./saas/cmd/cli
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

Internal Go module/package names (`saas`, `dbent`, `lace`, `saas/pkg/aicoder/*`) intentionally keep their original identifiers — they are not user-visible and renaming them is pure churn. User-facing surfaces (binary `lore`, `.lore/` data dir, `lore.db`, `LORE_*` env vars, the skill) are all branded `lore`.
