# J1 — the org admin with many repos

Test: `TestStoryJ1AdminManyRepos` in [j1_admin_test.go](j1_admin_test.go). Track: the bug hunt's journey 1 (an admin over copies of a real company's repos), replayed on generated repos of the same shapes.

## Who

Ada Admin (ada.admin@example.test, GitHub login ada-gh) runs the org acmeco on one laptop. Under `~/code` she has every kind of repo the org has: `app` (a code repo, no lore yet), `hub` (a lore 0.1.17 project with 5,500 rows: tasks, comments, commit links, memories, decisions, rules, hotfixes), `svc` (a small 0.1.17 project), `mono` (a monorepo of services and libraries), `docs` (markdown only) and `brick` (code with no lore data). All are clones of `github.com/acme-co/*`; the team store is `github.com/acme-co/lore-team`.

## Steps

1. Day 1: she clones every repo into `~/code`.
2. She runs the guided setup as the admin of a new org: identity, GitHub, a local personal store, the team step with the team store and the org's own repos (`github.com/acme-co/*`), tools, no secrets backend, folders `~/code/**`, then syncs. `lore setup list` shows her admin setup, `lore whoami` her org and email.
3. She runs `lore fix --all --yes` twice. Both runs list `hub` and `svc` as on the lore 0.1.17 format with the command that would upgrade each (`lore fix --migrate`), migrate nothing, and do not name `brick` or `docs`. The second run is as quick as the first.
4. She reads the big old repo (`lore task list --all` shows all 1,277 tasks in a few seconds), tries to add a task in `svc` (refused: the project is on the 0.1.17 format, run `lore migrate run`), checks `lore migrate status`, tries to add a task in `docs` (refused) and runs `lore whoami` in her home folder.
5. Day 2: she runs `lore init` in `app` and `mono`, adds and edits a task, a memory and a must rule in `app`, a task and a memory in `mono`, lists and searches in both; each repo sees only its own notes.
6. She renders `app` (LORE.md carries the must rule), looks at `lore store list`, `lore where` and `lore doctor` in every repo.
7. She adds a member (`lore member add --email … --name … --yes`), lists the members, syncs.
8. Day 3: she commits code in `app` through the lore hooks (the tree is clean afterwards), and in a brand-new empty repo runs `lore init`, adds a task and makes its very first commit.

## What must hold

- Nothing pushes without push or sync: no code repo's remote ref moves through the setup, `fix --all`, reads, refused writes, adds and edits, render, store list, where or doctor (live rows wait for `lore sync` or her own `git push`).
- A repo without lore data stays byte-identical: `brick` after the setup, both `fix --all` runs, reads and doctor; `docs` after `fix --all` and a refused write (no empty `.lore/` left behind).
- Old-format projects stay old: every row file and `_meta.json` of `hub` and `svc` stays `"_v": 1` through the setup, `fix --all` without `--migrate`, reads, a refused write and code commits.
- Named flags: every hint printed by `fix --all`, the refused write and doctor uses named flags.
- The shared journey invariants after every step: readable output, doctor in every clone, config, file modes.

## Linked journeys

- `tests/journeys/fix_test.go` (`TestJourneyUpgraderFixOldProjects`): `fix --all` over old projects with and without `--migrate`, folders outside home.
- `tests/journeys/nolore_untouched_test.go` (`TestJourneyNoLoreRepoUntouched`): every command in a repo only named by a store link.
- `tests/journeys/land*_test.go`: doctor `--all` budget on many repos.
- `tests/journeys/teamlead_test.go`: the team's first weeks (invites, roster, conflicts).

## Findings guarded

FP-2 (a write in an old-format project refused with one line), FP-4 (`fix` migrates only with `--migrate`; `fix --all` lists old projects), FP-7 (`fix --all` creates nothing in a repo with no lore data), S-1 (no command but push/sync moves a remote ref), S-2 (a repo with no lore data stays byte-identical), U-2 (live writes never push by themselves), U-4 (named flags in hints), B1-4 (whoami outside a repo does not say client repo), B1-7 (`fix --all` writes nothing into repos without lore), B1-11 (the second `fix --all` is clean), B1-16 (reads on the 5.5k-row repo take seconds, not a minute), B1-17 (a refused write leaves no empty `.lore/`), B1-24 (the guided admin setup reaches a working team).
