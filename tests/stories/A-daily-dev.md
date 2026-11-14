# Story A — daily development

Test: `tests/stories/a_daily_dev_test.go` (`TestStoryAWeek1`, `TestStoryAWork`, `TestStoryABranches`, `TestStoryAAgents`). Source track: dev company track A (daily development, weeks 1-3).

## Who

A small product team with an org `acmedev` and a team store: Cleo (cto, the org admin), Liam (lead), Dana and Omar (developers), plus the coding-agent sessions they run. They work on two service repos: `sync-svc`, a project lore 0.1.17 wrote for weeks (generated with the 0.1.17 row shape, `SmallShape`), and `auth-svc`, a code repo that has no lore data until the cto runs `lore init`. A third repo, `brick`, never gets lore data. Every repo is the team's own (`github.com/acme-dev/*` is in the org's own patterns), so notes live in the repo.

## Steps

1. Day 1, before the migration: Cleo reads in `sync-svc` (task list, migrate status). Every task of the old project is there. A write is refused with the one line "is on the lore 0.1.17 format; run lore migrate run to upgrade it".
2. Cleo runs `lore migrate run` in `sync-svc`. lore upgrades the rows and leaves them for her commit; she commits and pushes with plain git.
3. Cleo runs the guided setup as admin (identity, GitHub, local personal store, the team store and the org's own repos, tools, folders `~/code/**`), runs `lore init` in `auth-svc` and pushes it.
4. Cleo invites Liam and Dana (`lore team invite --email`); each runs the guided setup as a member with the invite line, then `lore whoami` names the org and their email. Both see every migrated task of `sync-svc`.
5. Week 2: Cleo adds a mission and a high-priority task in it, then `lore sync` shares them; a second task in the mission stays open, and `lore mission done` names it. Dana syncs and sees the task. `lore task claim --id` without `--push` is refused and names `--push`; with `--push` the claim reaches the remote's signals ref. Dana starts the task, commits code whose message names the task id, marks it done and syncs; Cleo sees it done.
6. Cleo adds a rule, a memory and a decision, finds the memory with `lore search`, runs `lore render`: `.lore/LORE.md` holds the rule and `CLAUDE.md` (the tool chosen in the setup) points at it, every line of lore's pointer a whole paragraph.
7. Week 3, git life: Dana syncs, Cleo adds a rule and a memory, renders and pushes. Dana's first `lore sync` there leaves nothing for git to show (lore's wiring stays in her clone's `.git/info` files; A-1), and her plain `git pull` takes the notes commit, which carries the `.gitignore` and `.gitattributes` lines from Cleo's clone, the one that ran the migration. Dana adds a note on a feature branch and pushes it; switching back to main leaves no tracked file changed; Cleo merges the branch into main the way a PR merge does; both read the note. Cleo edits one memory differently on two branches and merges both: git stops on the second merge, lore says the row needs a manual merge and keeps both sides inside the JSON value (the file stays valid JSON), `lore sync conflicts` lists it, `git commit` is refused while the markers are there, a normal `lore memory edit` settles it and the merge commit goes through. A plain `git push` that carries rows finished in live may stop once with E_PUSH_AGAIN and goes through when run again (documented).
8. The trash: Cleo deletes the memory, finds it in `lore trash list`, restores it with `lore trash restore --id`; deletes a rule and purges it with `lore trash purge --id --confirm`, after which `lore rule show` says not found.
9. Agents: Omar's four coding-agent sessions write five notes each (memories and tasks, each task with `--commitment accepted`) into `auth-svc` at the same moment. Every write succeeds, the counts are exact, doctor finds no broken row file.

## What must hold

- Nothing pushes without push or sync: `migrate run`, reads, refused writes, mission/task/note adds, render, delete/restore/purge, a claim without `--push` and the agents' writes leave every ref of the code remote as it was; only `lore sync`, `task claim --push` and the people's own `git push` move it.
- A repo without lore data stays byte-identical: `brick` after the team's week 1 and a read in it, which names `lore init` as the next step.
- Old-format projects stay old until `lore migrate run`: reads and a refused write leave every row file of `sync-svc` on format 1; after the migration commit every clone reads the new format.
- Every hint lore printed in these steps uses named flags.
- The shared journey invariants (readable output, doctor, config, file modes) hold after every step.

## Covered elsewhere (not repeated here)

- Moving a note to another repo (A-15): `tests/journeys/crossrepo_test.go` (`TestJourneyCrossRepoRow`).
- A renamed store clone folder (A-16): `tests/journeys/strayclone_test.go`.
- Positional ids and named flags on every command (A-11): `tests/journeys/argshapes_test.go`.
- Setup tools wired into render and init (A-9): `tests/journeys/setuptools_test.go`.
- Killed agent writes and `lore api` next to the CLI: `tests/journeys/agents_test.go`.

## Findings guarded

D-8 (A-7), D-9 (A-6), D-20 (A-9), D-26 (A-12), D-29 (A-13), D-36 (A-17), D-37 (A-18), D-40 (A-19), D-42 (A-20), D-83; the exact test of each is in the REGRESSION lines of the st1 status file (tests/stories/REGRESSIONS.md once it exists).
