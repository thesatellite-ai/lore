# W — every read, from every place a person stands

**Who.** Ada, the admin of the org `acme` (made-up names), on one laptop with the org's team store, a client store, and her repos under `~/code`.

**Why.** Reads were fixed one place at a time: a read from the home folder said "git init", a read in a store clone committed a row, a read in a repo that gitignores `.lore/` or links `CLAUDE.md` out of the repo wrote tracked files. The owner asked for one test that tries every read from every place at once, so a read added tomorrow is tried everywhere the day it lands.

**Test.** `TestStoryWWhereYouStand` (tests/stories/W_where_you_stand_test.go). The reads are every operation of the real registry whose spec only reads (`op.Spec.OnlyReads`, not hidden); each gets its required inputs generated (a row id seeded for its noun when there is one), with a short override table where a generated value cannot work. The places run in parallel on one binary.

## Steps

1. Day 1: Ada starts the org with a team store, inits her own repo `platform`, adds a store folder `web` with a memory, a task, a decision and a rule in it.
2. She links the repo `web` to that folder, adds a client store and links the client's monorepo `shop` to it, inits a repo that gitignores `.lore/` and one whose `CLAUDE.md` is a link to `~/notes/CLAUDE.md`, clones a lore 0.1.17 repo she never migrates, and runs `lore sync` so every store is sent.
3. Day 2: from each of eight places — the home folder, the team store's clone, its folder `web`, `web`, `shop`, the `.lore/`-ignored repo, the linked-`CLAUDE.md` repo, the 0.1.17 repo — she runs every read once.

## What must hold

- (a) Each read answers, or refuses with an error line; a refusal never says "git init", and a not-here refusal (`E_NOT_A_STORE`) has a hint naming `--store` and `--setup`, or, for a read that lives in each code repo (missions, the inbox, migrate status), a hint saying to cd into the code repo and that `--store` does not apply. A read never refuses only because the place has no project row (a read never makes one, so it reads the rows there).
- (b) Every tracked file of the place's repo is byte-identical (a link by its target), `git status` is unchanged, the file a link points at outside the repo is unchanged, and the home folder never becomes a git repo.
- (c) No ref of any remote moves (`lore sync` is not a read and is not run).
- (d) Exit 0 comes with an answer and no error line; a non-zero exit with an error line. `lore check leaks` is a git hook and passes silently.
- Findings another branch fixes are logged as waiting with their id, never asserted the buggy way, until the fix lands.

## Findings it guards

- `devco:D-503` (REGRESSIONS.md): `lore why-context show` in the team store's clone committed the folder's project row; a Local read now runs as a read.
- `devco:D-504` (REGRESSIONS.md): `lore team invite list` with no invites exited 0 with no output; an answer that is only empty lists says `no <list>`.
- `devco:D-510` (REGRESSIONS.md): from the home folder every read but the roster's refused with a hint naming no `--setup`; the hint now names `--store <org>[/<folder>]` and `--setup <setup>`.
- `devco:D-505` (REGRESSIONS.md): 35 kind lists and searches refused at the store clone's root for want of a project row.
- `devco:D-522` (REGRESSIONS.md, `TestStoryWXFixExitAndStoreClone`): `lore fix` exited 1 after "Everything is current." with warning lines only; warnings alone now exit 0.
- `devco:D-524` (REGRESSIONS.md, `TestStoryWXFixExitAndStoreClone`): `lore fix` in a store clone said "not inside a git repo here"; it now says the clone is a lore store clone, not a code repo, and names `lore store doctor`, `lore sync` and the code repo to cd into.
- `devco:D-526` (REGRESSIONS.md, `TestJourneySetupOwnOffer`): an admin's guided setup in the org's own repo with no own: pattern left it a client repo silently; the team and This folder steps now offer the remote's owner as an own: pattern (asked at a terminal, added with --yes, never written without either: the line names `lore org config set --org <org> --key own --add <pattern> --yes`); members are never offered it.
- Waiting: `devco:D-509` (open: repo-only reads refuse with hints naming neither flag nor saying they are repo-only; `sync trash list` in a store clone).
- `devco:D-525` (REGRESSIONS.md, `TestStoryWXOwnRepoNeverListed`): in the org's own repo set up with no `own:` patterns, `lore fix` and `whoami` said "a client repo" with no cause; they now say it is treated as a client repo because no `own:` pattern matches its remote, and name `lore org config set --org <org> --key own --add <remote> --yes`.
- `devco:D-534`, `devco:D-535`, `devco:D-536` (REGRESSIONS.md, `TestStoryWXUpPaths`): a lore home copied from `~/.lore` (a preview home) fetched in the old home's store clone through a `why: moved` record entry, its store clones' hooks named the old home and a build that may be gone, and `lore sync` in a linked own repo silently wrote `CLAUDE.local.md` importing `~/.lore/render/`; the copy now warns once and uses its own clone, the hooks find their home from where the clone sits and fall back to `lore` on PATH, and sync names each local file it wrote, importing the render under the home in use.
