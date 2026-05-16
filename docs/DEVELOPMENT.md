# Development

Internal build / release notes. End-user docs live in the top-level
[README](../README.md).

## How it works

| Layer | Detail |
|-------|--------|
| Storage | One SQLite file under `.lore/`, pure-Go `modernc.org/sqlite` |
| Search | SQLite FTS5 (built into the binary) + hybrid ranking |
| Output | Compiles stored knowledge into `CLAUDE.md` / `AGENTS.md` / `.cursorrules` |
| Agent loop | Claude skill captures corrections & decisions back into the db |

## Layout

This is a Go **workspace** (`go.work`) stitching local modules:

| Module | Role |
|--------|------|
| `saas/cmd/cli` | the `lore` binary (package `main`) |
| `saas/pkg/aicoder/*` | core domain logic (capture, search, render, fts5) |
| `dbent` | Ent schema + generated client (local-only module) |
| `lace/db` | pure-Go SQLite open/registration |
| `github.com/khanakia/entx/enttui` | TUI engine (resolved from the module proxy) |

`vendor/` is **not** committed; `go.sum` is. A fresh clone builds from the
module proxy. The build is pure-Go: `CGO_ENABLED=0`, `modernc.org/sqlite`
(FTS5 is built in — no build tag).

## Build

Requires Go 1.26+ and [Task](https://taskfile.dev/).

```sh
task lore:build          # → tmp/bin/lore (version stamped from git describe)
task lore:build:debug    # with debug symbols
task lore:install:all    # build + install binary AND the Claude skill locally
task lore:skill:install  # (re)install the skill to ~/.claude/skills/lore
task lore:test           # unit + integration tests
```

Plain `go build`:

```sh
CGO_ENABLED=0 go build -o tmp/bin/lore ./saas/cmd/cli
```

## Releases

Driven by GoReleaser (`.goreleaser.yml`) via `.github/workflows/release.yml`.
Four trigger paths:

1. **push to `main`** → auto-bump patch, tag, release. Add `[skip release]` to
   the commit **subject** to opt out.
2. **push tag `vX.Y.Z`** → release that tag (use for minor/major bumps).
3. **workflow_dispatch with `tag=`** → create+push that tag, then release.
4. **workflow_dispatch, empty tag** → snapshot build, artifacts on the run page.

Each release cross-compiles linux/darwin/windows × amd64/arm64, bundles the
binary + `README.md` + `skills/` into per-platform archives, and publishes a
GitHub Release with checksums.

Validate locally before pushing:

```sh
goreleaser check
goreleaser release --snapshot --clean --skip=publish
```

## Homebrew tap

Formulae for all `thesatellite-ai` tools live in one shared tap repo:
**`thesatellite-ai/homebrew-tap`**. GoReleaser writes `Formula/lore.rb` there on
every release; users run `brew install thesatellite-ai/tap/lore`.

One-time setup (already done for the tap repo itself):

1. The tap repo `thesatellite-ai/homebrew-tap` exists and is public.
2. A Personal Access Token with `repo` scope (or a fine-grained token with
   contents:write on the tap repo) is stored as the **`HOMEBREW_TAP_GITHUB_TOKEN`**
   repository secret on `thesatellite-ai/lore`. The default `GITHUB_TOKEN`
   cannot push to a *different* repo, so this separate token is required.

To create the secret:

```sh
gh secret set HOMEBREW_TAP_GITHUB_TOKEN \
  --repo thesatellite-ai/lore \
  --body "<PAT with repo scope>"
```

Future projects reuse the same tap — just add a `brews:` block pointing at
`thesatellite-ai/homebrew-tap` in their own `.goreleaser.yml`.

## Rebranding note

Internal Go module/package names (`saas`, `dbent`, `lace`,
`saas/pkg/aicoder/*`) intentionally keep their original identifiers — they are
not user-visible and renaming them is pure churn. User-facing surfaces (binary
`lore`, `.lore/` data dir, `lore.db`, `LORE_*` env vars, the skill) are all
branded `lore`.
