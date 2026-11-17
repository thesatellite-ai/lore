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
- Waiting: `devco:D-509` (open: repo-only reads refuse with hints naming neither flag nor saying they are repo-only; `sync trash list` in a store clone).
