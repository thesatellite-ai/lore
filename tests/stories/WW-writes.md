# WW — every write, from every place a person stands

**Who.** Ada, on one laptop with a personal git identity (`Ada Home <ada@home.test>`), the admin of two orgs (made-up names): `acme` (org identity `Ada Admin <ada@acme.test>`, a team store and a client store) and `acmeops` (org identity `Ada Ops <ada@acmeops.test>`, a team store of the same shape), with her repos under `~/code` and `~/ops`.

**Why.** The read matrix (story W) tried every read from every place; the writes were still found one by one by the owner: `store set-url` set one store clone's git user to the personal identity and the other's to the org identity, moved one clone and not the other, `store push --store <org>` refused from the home folder, and `store push` said "sent 0" after sending a commit. The owner asked for the write sibling so these are caught before the owner meets them.

**Test.** `TestStoryWWWrites` (tests/stories/WW_writes_test.go). The writes are every operation of the real registry that is not a pure read (not `op.Spec.OnlyReads`, not hidden), with required inputs generated (a row id seeded for its noun, or made by the place's own `add`) and a short per-op table where a generated value cannot work. Each place gets its own fresh world built the same way (writes mutate), the places run in parallel on one binary, and within a place adds run first, deletes last, and the ops that take a store, org or setup away at the very end.

## Steps

1. Day 1: Ada starts `acme` with a team store from `~/code/platform` and `acmeops` with its team store from `~/ops/tools`, each with a folder `web` and seeded rows (a memory, task, decision, rule, pattern, playbook, prompt, hotfix, incident, snapshot, mission, plan, tag).
2. She links `~/code/web` to `acme/web`, adds a client store and links the client's monorepo `shop`, inits a repo that gitignores `.lore/` and one whose `CLAUDE.md` links to `~/notes/CLAUDE.md`, clones a lore 0.1.17 repo, and syncs both orgs.
3. Day 2: from each of twelve places — the home folder (plain, naming `--to acme/web`, naming `--to acmeops/web`), each team store's clone root and its folder `web` (acme and acmeops), `web`, `shop`, the `.lore/`-ignored repo, the linked-`CLAUDE.md` repo, the 0.1.17 repo — she runs every write once.

## What must hold

- W's rules: each write answers or refuses with an error line (never "git init"; a not-here refusal names `--store` and `--setup`, or says it is repo-only); exit 0 never comes with an error line, a non-zero exit always does.
- (a) Every commit a write makes in a store clone is authored and committed by the org identity of that store's org, and the clone's git `user.name`/`user.email` equal it (checked for every store clone before the first write and after every write that commits).
- (b) A commit count the write prints ("sent N", "N commits") matches the commits that reached the remotes or were made.
- (c) The twins — `home-to-acme`/`home-to-acmeops`, `store-root`/`ops-root`, `store-folder`/`ops-folder` — get the same treatment for the same write: the same exit and the same output once org names, ids, hashes and numbers are masked.
- (d) Nothing outside the target changes: no commit, tracked file or git status of another store clone or another repo (a place of org `acme` may write the acme stores, never the acmeops one), not the file a link points at, and the home folder never becomes a repo.
- (e) No remote's refs move unless the write is a push or sync by design (`store push`, `store set-url`, `land`, `branch share`, `sync …`, store creation and folder changes, invites, and claims, releases and holds, which reach the remote at once by design).
- (g) Every next-step command a write prints (`restore: lore …`, `push it with: lore …`, `hint: lore …`) is run exactly as printed in the same folder, and must not fail with an unknown flag or command, `E_INVALID_INPUT`, or "this folder has no lore project" (the owner's guard after use pass 1); a not-here refusal names the store flag the command takes (`--store` for a read, `--to` for a write) and `--setup`, and never a flag the command lacks.
- Findings another branch fixes are logged as waiting with their id, never asserted the buggy way, until the fix lands.

## Findings it guards

- `devco:D-538`, `devco:D-539`, `devco:D-543`..`devco:D-549` (REGRESSIONS.md, owner use pass 1): a write from the home folder names its store with `--to <org>/<folder>` alone (the org's one setup is used as `--setup` would be), an edit, delete or `trash restore` by id from the home folder finds the row's org, hints name only flags their command takes, the restore line a delete prints and the push line an org-file change prints work where printed, `lore sql`'s hint names `--query`, `lore repo link` points at `lore org link`, a bare `lore setup check` names the setups, the steps and the health check, and `setup fix` given an unknown check lists the check ids; `TestStoryWUPrintedCommandsWork` replays the use pass, and rule (g) runs every printed command from every place.
- `devco:D-520` (REGRESSIONS.md): from the home folder every write that needs a store refused with a hint naming `--to` but no `--setup`; the hint now names `--to <org>/<folder>` (or `--store`) and `--setup <setup>`.
- `devco:D-523` (REGRESSIONS.md): repo-only writes (store add/init/remove/set-url, org link, fix, migrate run/rollback/unlock, directive install, sync install-action; memory/meta/row move in a store clone) refused in a store clone or at home with hints that were neither the `--store`/`--setup` pair nor the repo-only class, and roster writes from home named `--setup` but not `--store`; each now says to cd into the code repo and that `--store` does not apply, and a roster change names `--setup` and says `--store` is the reads'.
- `devco:D-516`, `devco:D-517` (REGRESSIONS.md): `store push --store <org>` from the home folder and the sent count, checked from every place by rules (b) and (c).
- `devco:D-514`, `devco:D-515` (REGRESSIONS.md): `store set-url` from the home folder set the moved clone's git user to the personal identity (on both orgs here); checked from every place by rule (a) on the moved clone, with the twins by rule (c).
- Waiting, left for the coordinator: `devco:D-521` (`migrate run` in a code repo says "Nothing to migrate" and pushes the org store's waiting commits), `devco:D-522` (`fix run` exits 1 with no error line after "Everything is current."), `devco:D-524` (`fix run` in a store clone says "not inside a git repo here").
