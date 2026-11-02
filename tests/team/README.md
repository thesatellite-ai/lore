# Team e2e suite (LORE_TEAM_SPEC.md)

Real-user scenarios for lore for teams: live rows, landing, ride-along, compaction, orgs, stores, links, leak guards, render, signals, claims and agent coordination. Every test drives the real lore binary with real git, the way people and agents use it, and asserts only what a user can see: CLI output and exit codes, files in `.lore/data`, refs on the remote, and what a client clone can fetch.

The suite runs ahead of the code. A test whose commands are not in the binary yet skips as `PENDING: not built yet: lore <cmd> --<flag>`; once `lore <cmd> --help` lists the command and flag, the test runs for real and stays red until the behaviour matches the spec.

A regression test for a known open bug (a `th.Bug` with `Open: true`, e.g. BUG-D1 owned by lore-stores) skips as `PENDING: BUG-D1 open (lore-stores): …` only at the point where today's binary shows that exact defect; any other failure is still red, and once the fix lands the test passes on its own. Set `Open: false` when the fix is merged, so a later regression fails instead of hiding as pending.

## Run it

```bash
task lore:test:team                       # whole suite through testlock, prints the matrix
task lore:test:team:race -- -count=10     # only the race tests, repeated, with -race

# by hand, one test
go build -tags loretest -o /tmp/lore-team ./saas/cmd/cli
LORE_TEAM_BIN=/tmp/lore-team go test -tags teame2e -run 'TestF07_ClaimRenewReleaseNeverMakeHistory' -v ./saas/cmd/cli/
```

| Variable | Effect |
|---|---|
| `LORE_TEAM_BIN` | lore binary to drive (built with `-tags loretest` so `LORE_NOW` works); default: the plain e2e build |
| `LORE_TEAM_STRICT=1` | a pending test fails instead of skipping |
| `LORE_TEAM_FORCE=1` | skip the capability probe and run everything (a feature whose help text is not final yet; checking fixtures) |

## Where things are

| Path | What |
|---|---|
| `saas/cmd/cli/internal/teamharness/` | the harness: World, Remote (bare repos), Actor (HOME, config, clock, offline), Clone, capability probe, same-instant barrier, leak scanner, git shim |
| `saas/cmd/cli/internal/teamharness/matrix/` | reads `go test -json`, prints the pass / FAIL / pending matrix, exits 1 only on real failures; a suite that does not compile prints `BUILD FAILED` with the compiler output, a package failing outside any test prints `PACKAGE FAILED`, and a run where no test matched prints `NO TESTS RAN` (all exit 1, never a 0/0 matrix) |
| `saas/cmd/cli/team_e2e_fixtures_test.go` | shared worlds: one-repo team, the agency (Northwind store, Acme client, Betalabs), helpers |
| `saas/cmd/cli/team_e2e_live_test.go` | P2: F1–F6, F8, E1–E4, E6, cookbook 1, 7, 16, side effects 11, 14, 22, 23, 27, 28, 34, crashes |
| `saas/cmd/cli/team_e2e_org_test.go` | P1: F10, F17–F19, E8, E12, E13, cookbook 5 |
| `saas/cmd/cli/team_e2e_store_test.go` | P3: F11–F16, F20, E9–E11, §6, cookbook 3, 4, 8, 10, 12, 13, 14, side effects 4, 20, 32 |
| `saas/cmd/cli/team_e2e_agents_test.go` | P4: F7, F9, §7.3, §7.4, §7.5 worked example, E5, E7, cookbook 2, 9, 15 |

All `team_e2e_*_test.go` files carry the build tag `teame2e`, so the normal suite (`task lore:test`, `task lore:check`) never compiles them.

## How the harness fakes the world

- **GitHub:** bare repos under `<tmp>/hosts/github.com/<owner>/<repo>.git`. Each actor's `~/.gitconfig` rewrites `https://github.com/` and `git@github.com:` to that folder with `url.<path>.insteadOf`, so clones keep real-looking URLs and org resolution sees `github.com/acme/app`. Lore must therefore read the raw `remote.<name>.url`, not `git remote get-url`.
- **Protected main:** a `pre-receive` hook refusing `refs/heads/main` with GitHub's GH006 text; `AdminPush` (the merge button) passes it.
- **Offline:** `Actor.Offline()` points the rewrite at a folder that does not exist, for every session on that machine.
- **Clock:** `Actor.SetNow` / `Advance` set `LORE_NOW` per actor (clock skew between machines is two different clocks).
- **Agents:** `startAgent` runs `lore agent start` on the owner's machine and copies the printed `LORE_ACTOR` / `LORE_SESSION` into an agent session sharing the owner's HOME, exactly like tmux sessions on one server.
- **Crashes:** a `git` shim first on lore's PATH logs every git call (used to count pushes) and, with `LORE_TEAM_KILL_ON=<part of the git command line>` and `LORE_TEAM_KILL_WHEN=before|after`, SIGKILLs lore at that step.
- **Races:** `World.Simultaneously` starts several prepared lore processes behind one barrier.
- **Leaks:** `Clone.FetchEverything` fetches every ref a client can (`refs/*`), and `Clone.LeakScan` searches the whole checkout, ignored files, `.git` and every git object.

## Adding a test

Name it after the spec item it proves (`TestF05_…`, `TestE01_…`, `TestRecipe12_…`, `TestSideEffect28_…`, `TestLeak_…`, `TestNext_…`), list every command and flag it uses in `newRepoTeam` / `newAgency` / `newCrew` / `newTeamWorld`, assert what a user would see, and add a positive control wherever an absence check could pass vacuously.
