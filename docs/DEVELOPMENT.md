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

One Go module, `github.com/thesatellite-ai/lore`, at the repo root (no `go.work`; a clean clone builds from the module proxy). The packages are layered (`model`, `schema`, `logic`, `store`, `adapters`, `op`, `engine`, `ops`, `surface`, `wire`, `app`, `cmd`); `internal/layers` fails the build when a lower layer imports a higher one.

| Path | Role |
|------|------|
| `app/` | the `lore` command itself: `app.Main` / `app.Run(ctx, args, ...Option)`, every host command and its tests. Importable, so another build composes the same command with options (`WithVersion`, `WithSetupUI`) instead of swapping files; only `cmd` may import it (`internal/layers`) |
| `cmd/lore` | the `lore` binary's `main`: stamps the version and calls `app.Main` with no setup UI, so `lore setup web` serves the plain page (`setupweb.DefaultUI`) |
| `wire/` | the composition root: picks the concrete backends, cache, scanner and identity |
| `surface/cli`, `surface/rest`, `surface/tui`, `surface/docs` | CLI, local REST + OpenAPI (`lore api`), the TUI, and the generated reference |
| `ops/<noun>` | one package per noun, each defining its operations |
| `adapters/backend/{git,folder,rowfs}`, `adapters/cache/sqlite` | store backends and the run-time SQLite cache |
| `tests/teame2e` | the team end-to-end suite (`task lore:test:team`, build tag `teame2e`) |
| `tests/setupe2e` | the setup page end-to-end suite (`task lore:test:setupe2e`, build tag `setupe2e`): fake machines and the real `lore setup web` |
| `tests/journeys` | the real-user journeys (`task lore:test:journeys`, build tag `journeys`): personas living with lore over months, shared invariants after every step ([tests/journeys/README.md](../tests/journeys/README.md)) |

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
task lore:test:quick     # QUICK tier: every unit test, Go test cache on, no race, no e2e (run it all the time)
task lore:test           # every package plain + the concurrent packages under the race detector
task lore:test:race      # only the race set (RACE_PKGS in Taskfile.lore.yml)
task lore:test:sync      # git-sync tests: row merge, the live ref, branch sync, sync operations
task lore:test:team      # team end-to-end suite (real binary, real git, many actors)
task lore:test:setupe2e  # setup page e2e: every machine shape via the page's HTTP API
task lore:test:cover     # coverage of unit AND end-to-end tests; lists functions no test reaches
task lore:check:gocache  # empty Go's build cache when it is over LORE_GOCACHE_MAX_GB (default 50)
```

### Test builds are not version-stamped, and the Go cache is bounded

Every task in `Taskfile.lore.yml` runs Go with `GOFLAGS=-trimpath -buildvcs=false` (its top-level `env:`; a `GOFLAGS` you export yourself wins), and the test helpers that `go build` the binary (`tests/machines`, `tests/journeys`, `tests/setupe2e`, `tests/teame2e`, `tests/userscen`) pass the same two flags. Why: a package compiled without `-trimpath` records the folder it was built in, and `-buildvcs` stamps the commit and dirty state into `cmd/lore`, so each of the ~30 worktrees on one build server compiled every package again and every commit added new cache entries; the shared Go build cache (`go env GOCACHE`, one per user) reached 1.2 TB and filled the disk. With the flags, the same code in two worktrees is one cache entry: building `cmd/lore` in a second copy of the tree added 208 MB to the cache without them and 3 MB with them (measured 2026-11-10). Test binaries carry the version `dev` (cmd/lore's default; `formatgate.DevBuild` treats it as a source build). The gate's build stage builds `tmp/bin/lore` with `lore:build:test` (version `dev`, no VCS stamp). Only `task lore:build` (and so `lore:install`) stamps the real version from `git describe` and keeps the VCS stamp, and the binary release stamps the release version through the same ldflag.

Nothing else bounds the cache (Go drops entries unused for 5 days, which does not help when every worktree builds every day), so `task check` starts with `lore:check:gocache` (`tests/lib/gocache-trim.sh`): it measures `go env GOCACHE` with `du` (about 4 s on a 570 GB cache) and runs `go clean -cache` only when the cache is over `LORE_GOCACHE_MAX_GB` (default 50). `LORE_GOCACHE_TRIM=dry` only says what it would do, `LORE_GOCACHE_TRIM=off` skips the step. It touches nothing but the cache folder, never a tracked file, and runs alone before the build stage because emptying the cache under a running build fails that build (a quick-tier `go test` of another session at the same moment may fail once with a missing cache file and passes on its next run). `go clean -cache` is all or nothing, so the first build after a trim compiles from scratch.

## The setup page (`surface/setupweb`)

- **Server.** `surface/setupweb` owns the local page server: one-time token, session cookie, CSRF header, Host and Origin checks, the CSP and the allowlisted `/v1` routes. The page's files come from a `setupweb.UI` (`FS()` with `index.html` and `assets/`, plus a content-hash `Version()`). This build hands in no UI, so the server serves `setupweb.DefaultUI`, a plain page that lists `lore setup plan|check|apply` and `lore doctor --all`. Another UI can be handed in through `setupweb.Options.UI` without changing the server's security rules; open packages never import code outside this module's layers (`internal/layers` enforces it).
- **Serving.** `GET /setup/assets/{file...}` serves `.js`, `.css`, `.svg`, `.woff2`, `.png` and `.txt` files from the UI's `assets/` (anything else is 404); every other `GET /setup/...` path except `assets`, `session` and `close` answers `index.html`, so a UI can route with history URLs under `/setup/`. The CSP allows `'self'` only, with no `'unsafe-inline'`, so `index.html` must not carry an inline script or style.

## Two test tiers

| Tier | Command | When | What runs |
|---|---|---|---|
| quick | `task lore:test:quick` | all the time while building: after every edit, before asking for review | `go test ./...`: every unit test of the root module with Go's test cache, no `-race`, no end-to-end suite except the fast machine shapes (`tests/machines` `TestForeign|Env|Links|Shapes`, about 45 s, skipped when arguments narrow the run). Narrow it with `task lore:test:quick -- ./ops/task/...`. Not behind `testlock`, so it never queues behind someone's full gate |
| fast | `task check:fast` (= `task lore:check:fast`) | landing a small fix (see "Landing" below) | build, vet, gofmt, staticcheck, cross-builds, tidy, docsync, every unit test, the race detector on the touched race-set packages, and the end-to-end steps the changed paths need (`FAST_MAP` in `Taskfile.lore.yml`); about 5 minutes or less |
| full | `task check` (= `task lore:check`) | once, before a commit is pushed (the pre-push hook enforces it; a small fix may land on `check:fast` instead, then the full gate runs on main) | everything below, scheduled under a load budget |

Measured on a 48-core Linux machine: the quick tier takes about 48 s with an empty test cache (slowest packages wire 38 s, ops/settings 29 s, app 26 s, ops/task 21 s; 2026-11-07) and less when most packages are cached. The full gate took 1526 s on 2026-11-11 with every end-to-end suite started at once (a load average of 161, peaks above 300, 42 % of the CPU spent in the kernel switching between processes); the same steps scheduled under the load budget, with the docs, scenario and chaos runners in parallel and `wire`'s race tests in shards, take about 15 minutes (below).

The quick tier stays fast only while no unit package is slow: a package that takes minutes is a test-design bug, not a reason to drop it from the tier: time it with `go test -json`, then make it fast without weakening it (independent tests `t.Parallel()` with their own temp dirs, fixtures and binaries built once per package, events or short deadline polls instead of sleeps). The end-to-end suites are behind build tags (`userscen`, `teame2e`, `setupe2e`, `machines`, `journeys`) or are shell/node runners, so `go test ./...` never starts them.

## Journeys: every feature adds its steps

A feature that changes what a person does over time (a new command, a new kind of store, a new sync mode, a new refusal, a new setup step) adds, in the same change, the journey steps where a real person would meet it, in the persona who would (`tests/journeys/<persona>_test.go`). A rule that must hold everywhere becomes a shared invariant instead. How: [tests/journeys/README.md](../tests/journeys/README.md).

## The gate: `task check`, before every push

`task check` holds the machine-wide `testlock` for its whole run, first bounds the Go build cache (stage cache: `lore:check:gocache`, above), compiles the module once (stage warm: `lore:build:warm`, plain and `-race`, so the steps that follow find every package in the cache instead of each compiling the same code at the same time), builds the test binary (stage build), then runs every check in one stage, test. Each step writes its own log to `tmp/check/<step>.log`; when a stage ends it prints every step's wall seconds and CPU seconds so far and each stage's wall time with its peak and mean load average (`tmp/check/timings.tsv`, `stages.tsv`). A failed step fails the stage, is named, and the tail of its log is printed. A stage starts only when the one before it passed.

**The load budget.** The steps of stage test are listed once, in `GATE_STEPS` in `Taskfile.lore.yml`: each with the cores it keeps busy on average (its CPU seconds over its wall seconds in a measured gate, at the `-parallel`, `JOBS` or `SHARDS` it is given) and heaviest first. `tests/lib/gate-parallel.sh` starts them in that order while their cores fit `GATE_SLOTS` (default 40 of the server's 48; `GATE_SLOTS=12 task check` on a smaller machine), so the load stays near the core count instead of every suite fighting for the CPU at once. Every lore process the end-to-end suites start gets the Go runtime settings of `LORE_E2E_GOENV` (default `GOMAXPROCS=2 GOGC=off GOMEMLIMIT=512MiB`; the harnesses add them through `tests/e2eenv`, the docs runners through `envfor`): a lore command lives a fraction of a second, and sized by Go for all 48 cores it spent about 40 % of its CPU on garbage-collector workers and idle scheduler threads. The settings change what a run costs, never what lore does; only lore processes get them (the Go toolchain and the test binaries keep Go's defaults), and a person's lore is untouched. Each suite names its `-parallel`.

**Where the time goes.** The gate is bound by CPU, not by any one step: the end-to-end suites start about 100,000 lore processes, each about 0.2 CPU seconds (building the operation registry by reflection, opening the SQLite cache, 40 to 60 git subprocesses), about 28,000 CPU seconds in all; at 40 cores that is close to 12 minutes however the steps are arranged. Making the gate faster now means making a lore command cheaper or a suite run fewer of them without testing less; the timings table names the heaviest steps (userscen, setupe2e, machines, sweep-rest, teame2e).

| Stage | Step | What it proves |
|---|---|---|
| cache | `lore:check:gocache` | the shared Go build cache is emptied when it is over `LORE_GOCACHE_MAX_GB` (default 50 GB) |
| warm | `lore:build:warm` | every package of the module compiles, plain and in the race set's `-race` variant (next to free when Go's cache has them) |
| build | `lore:build:test` | the binary the shell scenarios and chaos runs use builds (version `dev`, no VCS stamp; `lore:build:cross` still builds every release target) |
| test | `lore:lint:vet`, `lore:lint:fmt`, `lore:lint:staticcheck` | `go vet` (root module and every tagged suite), `gofmt`, `staticcheck` |
| test | `lore:build:cross` | a build for all six release targets (linux, darwin, windows × amd64, arm64), in parallel |
| test | `lore:check:tidy` | `go mod tidy -diff` is empty (it never rewrites the files, so it can run beside the builds) |
| test | `lore:check:docs` | every docsync citation in the docs still matches the code (`ds check`) |
| test | `lore:test:all` | every Go test of the root module without `-race`, including `app`'s docs drift test `TestDocsMatchCLI` (every `lore …` command in the README and `skills/*.md` exists with the flags shown) |
| test | `lore:test:race` | the race detector on the packages with real concurrency (goroutines, locks, HTTP servers): `RACE_PKGS` in `Taskfile.lore.yml` lists them and why each is there. `app` is not in it: its own code starts no goroutine and its tests drive `lore api` one request at a time (the comment above `RACE_PKGS` says what would bring it back). Every other package runs plain in `lore:test:all`; a package that gains a goroutine, a channel or a lock joins `RACE_PKGS` in the same commit. `./wire` runs as its own step, `lore:test:race:wire`, in six processes of one test binary (`tests/lib/gotest-shards.sh`): 123 of its tests are serial (`t.Setenv`), so one process took 200 s while the rest of the set took 25 s; the script fails unless every listed test reported a result |
| test | `lore:test:scen` | the user-scenario suite (`tests/userscen`): every user-facing feature through the real binary |
| test | `lore:test:team:run` | the team end-to-end suite (`tests/teame2e`): real binary, real git, many actors |
| test | `lore:test:setupe2e:run` | the setup page end to end through its HTTP API (below) |
| test | `lore:test:machines:core` | the real-machine suite (`tests/machines`): homes shaped like real users' machines (a fresh one, a v0.1.17 install, a migrated one with its `*.migrated` backups and a flow-style `config.yaml`, an owner-like one with three gh logins and the wrong one active, an ssh alias per account, a key without `.pub`, an alias whose key is gone, a control socket in `~/.ssh`, an org member, and setups disabled, trashed and purged with and without a tombstone), lore init in every kind of repo folder, and every valid 4-step setup lifecycle sequence (create, edit, disable, enable, pin, trash, restore, purge, restart; 83 sequences, the full set on the migrated, owner-like and graveyard shapes, a spread quarter on the rest, `MACHINES_FULL=1` for all). After every step: nothing purged or trashed is offered by import, a disabled setup never applies to its folder, `config check` knows every key, `doctor --all` reports, committed repo files are 0644 and folders 0755, and nothing in `~/.lore` is readable by another user. Also the shapes real machines grow (symlinked skill dirs and chains, dangling links, other tools' CLAUDE.md/AGENTS.md/.cursor files, a hand-edited `config.yaml` with comments, CRLF, tabs and unknown keys, v0.1.17 leftovers, overlapping folders, repos without a remote, with `GIT_DIR` set, worktrees, bare repos and submodules, read-only files, a missing `~/.lore`, two lore binaries on PATH, a non-ASCII home and spaces in paths); new shapes register in their own `tests/machines/shape_<area>.go` with `RegisterShape`. About 2 min; `task lore:test:machines` runs the whole package alone |
| test | `lore:test:sweep:tasks`, `lore:test:sweep:rest` | the command sweep: every user command of the inventory (and `TestSweepCoversEveryCommand` fails on a command that is neither swept nor listed as not user-run) on every shape, then disk state (block-style `config.yaml`, nothing written through a link into a foreign folder, modes 0644/0755) and `doctor --all`. About 30 s and 105 s alone (measured 2026-11-10), under the 3-minute budget of a gate step, so every gate runs the full sweep; a sweep that outgrows it runs a fixed seeded subset per gate and the full set in a separate task, with `TestSweepCoversEveryCommand` still checking that every command is in the full set |
| test | `lore:test:life:setups`, `lore:test:life:teams`, `lore:test:life:objects`, `lore:test:life:rest` | lifecycles through the built binary over kinds × shapes: every setup kind (solo, join, start team) create, edit every step, rename, change default, purge, re-add, import, edit again, remove; stores, tools, identity, personal store, org membership, profiles, live and trash/restore/purge; the same lifecycle over REST (`TestRestLifecycle`). Disk and doctor after every transition. 20 to 50 s each |
| test | `lore:test:journeys:frame`, `lore:test:journeys:<persona>` | the real-user journeys (`tests/journeys`), one step per persona (solo, agency, teamlead, joiner, agents, upgrader, and `land` for the `TestJourneyLand…` journeys of a landing round) plus the harness self-tests, and `lore:test:journeys:rest`, the catch-all that runs every journey no named part runs (`tests/journeys/gate_parts_test.go` checks each journey runs in exactly one part): each persona lives with lore over months through the real binary with the `LORE_NOW` clock, and after every step the shared invariants run on every machine (readable output, doctor in HOME and every clone, `config check`, file modes) plus the journey's own facts. Each under about 3 min; `task lore:test:journeys` runs them all alone |
| test | `lore:scenarios:run`, `lore:chaos:run` | the shell acceptance and failure-injection scripts in `tests/`, several at once, slowest first (`tests/lib/run-scripts.sh`; each already has its own temp folder and HOME). SC-21 needs root or sudo and skips otherwise |
| test | `lore:docs:examples:verify` | every docs example (`tests/docs`, `tests/team/docs`) re-run against the real binary gives the committed transcripts, and every pasted page block and the commands index are current. Both runners run at once, each several sections at a time (`tests/lib/parallel-sections.sh`: every section has its own world, HOMEs and transcript); about 2 minutes instead of 22 |

Run one step on its own with its task name (`task lore:test:team`, `task lore:test:setupe2e`, `task lore:scenarios`, …); the public names of the heavy suites take `testlock` themselves.

```sh
task check               # alias of task lore:check
task lore:hooks:install  # once per clone: use the committed .githooks
```

`task lore:hooks:install` points git at `.githooks/`, whose `pre-push` hook enforces the gate. A passing `task check` records the tree of the tracked files it tested (`.git/lore-check-passed`). On `git push`, if every pushed commit has exactly that tree, the push goes through at once; otherwise the hook runs `task check` first and blocks the push if it fails, or if the code it tested is not the code being pushed (uncommitted changes while checking). `git push --no-verify` skips it, for emergencies only. The hook itself is tested in `internal/repoguard/prepush_hook_test.go`.

### Landing: `check:fast` for a small fix, the full gate on main

A small fix (a policy tweak, a message, one function and its test) need not wait for the full gate. The rule:

1. Run `task check:fast` on the fix. It runs build, vet, gofmt, staticcheck, cross-builds, `go mod tidy -diff`, docsync, every unit test, the race detector on the race-set packages the fix touches, and the end-to-end steps its changed paths need. The mapping is `FAST_MAP` in `Taskfile.lore.yml` (`tests/lib/fast-plan.sh` reads it): a path rule names the steps (a docs page → the docs examples, `tests/machines/` → the machine suites, product code → the docs examples, scenarios, chaos, userscen and the team suite, the setup and doctor code → also the suites built around them), and a path no rule names runs every step: when unsure, run it. It writes its own stamp, `.git/lore-check-fast-passed`, and the pre-push hook accepts either stamp for the exact tree being pushed.
2. Push. Then run the full gate on what landed: `task check:after-land` fetches origin, makes a fresh worktree of `origin/main` in a temp folder, runs `task check` there and writes the result to `LORE_GATE_REPORT` (default `~/.local/state/lore-gate/last.md`): first line `PASS <commit> <subject>` or `FAIL …`, then the step timings, with the step logs beside it.
3. A `FAIL` there is fixed at once, before anything else lands.

A release is never cut on `check:fast`: the release commit itself passes the full `task check` ("Releases" below; the binary release has the same rule).

No gate step may modify tracked files: `task check` records the tracked tree when it starts (`check:begin`) and refuses to stamp if it changed by the end, naming the files. That is why `check:docs` puts `.ds/ledger.tsv` and `refs.tsv` back after `ds scan` (whose header records the commit and time of every scan): run `ds scan` yourself and commit when docs or citations change. Uncommitted changes to tracked files also make the stamp differ from the commit being pushed, so commit or `git stash` them before pushing.

### Setup page end to end: `task lore:test:setupe2e`

`tests/setupe2e` (build tag `setupe2e`) builds a fake machine per shape: gh logins (0, 1 or 2, which one active), `~/.ssh` (none, the default key only, several Host aliases, an alias whose key is missing; keys with or without `.pub`), `~/.gitconfig` includeIf identities, the state of `~/.lore` (empty, a v0.1.17 install, saved, removed, disabled or trashed setups, an invalid `config.yaml`) and the start folder (a lore repo, a git repo, a plain folder). Fake `gh`, `ssh` and `git` log every call with the account it acted as, a fake GitHub API answers `LORE_GITHUB_API`, and bare repos stand in for GitHub; logins, emails and keys are placeholders made at run time. `TestAPIMatrix` runs all 1764 valid shapes through the real binary's HTTP API (token, cookie, CSRF), saving every step with the page's own defaults, and fails when a refusal names a value lore filled in, when Save is refused for anything but a value only the person can give, when a gh or ssh call for a GitHub account's repo runs as another account, or when `config.yaml` lacks the setup; an invalid `config.yaml` must come back as typed problems instead. A world or server that cannot start fails the shape; nothing skips.

One world by hand, for a debugger: `go run -tags setupe2e ./tests/setupe2e/cmd/setupworld up --shape '{"gh_accounts":2,"gh_active":"second","ssh":"several","keys":"with_pub","include_if":1,"lore_home":"setups","start":"plain"}' --dir <empty dir> --port <port>` prints one JSON line (the setup link, HOME, config and log paths, accounts) and serves until Ctrl-C. `SETUPE2E_ONLY=<part of a shape name>` narrows the matrix.

Adding a table? Declare it once with `schema.Table(...)` in `schema/` (a synced row table or a machine-local one); that one definition drives the cache tables, foreign keys, indexes, CLI flags, REST routes and the docs. There is no code generation.

Plain `go build`:

```sh
CGO_ENABLED=0 go build -o tmp/bin/lore ./cmd/lore
```

## Composing the binary (package app)

All of the `lore` command lives in package `app`; `cmd/lore/main.go` is three lines that stamp the version and call `app.Main`. Another build composes the same command by importing this module and passing options, never by copying or swapping files:

```go
import "github.com/thesatellite-ai/lore/app"

func main() { app.Main(app.WithVersion(version), app.WithSetupUI(myUI)) }
```

| API | What it does |
|-----|--------------|
| `app.Main(opts ...Option)` | the whole process: the session-end hook fast path, an interrupt-cancelled context, `Run` over `os.Args`, `os.Exit` |
| `app.Run(ctx, args, opts ...Option) int` | builds the app and the command tree, runs `args`, returns the exit status |
| `app.WithVersion(v)` | the version the binary reports (empty keeps `dev`) |
| `app.WithSetupUI(ui)` | the static files `lore setup web` serves (`setupweb.UI`: files only; routes, headers and operations stay the core's). Without it the page is `setupweb.DefaultUI`, the plain page naming the CLI commands |

An enterprise build can add a setup page through `WithSetupUI`. Only `cmd` may import `app` (`internal/layers`), so no core package depends on how the binary is composed. `TestRunAppliesOptions` (app) checks the options and `TestBinaryServesDefaultUI` (tests/setupe2e, in `task lore:test:setupe2e`) checks that this module's binary serves the fallback page.

The test harnesses under `tests/` are ordinary packages, so a build that imports this module can reuse them: `tests/setupe2e` (build tag `setupe2e`; `Build`, `World.Start`, `World.HangProfile`, `PairwiseShapes`) and its world tool `go build -tags setupe2e github.com/thesatellite-ai/lore/tests/setupe2e/cmd/setupworld`.

Changing this module and an importing build together: keep both clones side by side and put a `go.work` in a separate folder above them (`go work init ./lore ./<other>`), or add a local `replace github.com/thesatellite-ai/lore => ../lore` that is never committed. This repo has no `go.work`: a clean clone builds from the module proxy. Land the change here first, then move the importing build's `require` to the new version.

## Releases

This repository publishes no binaries. A release of the core is a version tag, made by `.github/workflows/release.yml`:

1. **push to `main`** → the next patch tag (`vX.Y.Z`) is created and pushed. Add `[skip release]` to the commit **subject** to opt out.
2. **workflow_dispatch with `tag=`** → that tag on the current ref (use for minor/major bumps).

The commit a release tags passes the full `task check` first, never only `task check:fast` ("Landing" above).

No binary is built here and the workflow creates no GitHub Release. The tag is also the Go module version an importing build requires, so a tag is permanent: never move or reuse one, fix forward with the next version.

The released `lore` binary (this core plus proprietary components such as the guided setup page) is built and published separately, at the same version, after the tag exists: it cross-compiles linux/darwin/windows × amd64/arm64 and uploads per-platform archives named `lore_<os>_<arch>` (`.tar.gz`, `.zip` on Windows) with `checksums.txt` to this repository's GitHub Release for the tag, so `lore upgrade` (`adapters/selfupdate`), `install.sh` and `install.ps1` keep reading releases from here. Every archive holds `lore` (`lore.exe`), this repository's `README.md` and `skills/`, `BINARY-LICENSE.txt` (the binary's terms: free to use, commercial use included), `LICENSE-core-Apache-2.0` and `NOTICE-core` (this repository's `LICENSE` and `NOTICE`). The source here stays Apache-2.0; a binary built from it (`task lore:build`) is Apache-2.0 alone and serves the plain setup page.

## Homebrew tap

Formulae for all `thesatellite-ai` tools live in one shared tap repo: **`thesatellite-ai/homebrew-tap`**. The binary release writes `Formula/lore.rb` there, pointing at the archives on this repository's Releases, with `license :cannot_represent` (the binary is under `BINARY-LICENSE.txt`, which no SPDX id names); users run `brew install thesatellite-ai/tap/lore`. Nothing in this repository writes to the tap.

## Rebranding note

The v0.1.17 internal module names (`saas`, `dbent`, `lace`) are gone since phase E; the code is the one module `github.com/thesatellite-ai/lore`. User-facing surfaces (binary `lore`, `.lore/` data dir, `lore.db`, `LORE_*` env vars, the skill) are all branded `lore`.
