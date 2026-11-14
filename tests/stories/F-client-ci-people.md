# Story F — a client repo, a contractor, CI variants and people changes

Dev-company round 2, track F (plus the client-repo findings of track D). Replayed by `TestStoryFClientCIPeople` in `F_client_ci_people_test.go` (build tag `stories`).

## Who

- An org with a CTO (admin), a lead, a newhire who joins in week 2 and a leaver who leaves in week 3.
- A contractor who works only in the client's repo and sees only the client store.
- CI: a fresh clone every run.
- A second org on the CTO's laptop.
- Repos, generated: a client's TypeScript monorepo with no lore data (the client repo), a PHP app with no lore data, and an internal Go repo in format 2.

## Steps

1. The CTO adds a client store on an empty bare remote (`lore org store add`), then links the client repo (`lore org link`); the link works on the empty remote (no "no branch main").
2. The contractor clones the client repo and writes there: tasks, comments on internal tasks, plans, decisions, reminders, all from inside the client repo. Each write lands in the linked internal project or the client store, never in the client repo's tree; the first write after the link works the first time; a package folder inside the linked repo is linked too.
3. `lore render` and `lore check leaks --ci` in the linked client repo work (no `E_NOT_A_STORE`, no "run from the project root"). Refused writes leave no `.lore/state/audit-v2.jsonl`, no hooks, no `.lore/` in the client tree; a refusal never hints `lore init` there.
4. Every commit the contractor makes passes `lore check leaks --as-client`: no actor ids, no org emails, no internal row ids reach the client repo's tree or history. `lore setup apply --step team` run in the client repo writes no internal store url into it.
5. CI variants of the internal repo: `git clone --depth 1`, a read-only HOME (`chmod a-w`), no git identity, a detached HEAD on a merge ref, `CI=true` and `GITHUB_ACTIONS=true`. `lore check leaks --ci`, `lore doctor`, `lore render --check`, list and search change nothing (no `.lore/lore.db-wal` in the tree), push nothing and exit 0 when clean; doctor is not DEGRADED on a shallow clone.
6. Week 2: the newhire runs the printed join line and sees the team's notes.
7. Week 3: the leaver has open tasks. `lore member remove` names them; `lore task reassign --from @leaver --assign @lead` hands them over; `--assign @leaver` after the removal is refused with a hint that names a real next step. A write the leaver made before removal but never synced is not pushed by the leaver's next `lore sync`.
8. The contractor's access to the client store is revoked: their next write says it could not reach the store (not "committed, check the address"), doctor names the lost access.
9. A write with `--to <org>/<folder>` inside the PHP app (no lore data) goes to the org store without asking for `lore init`.
10. The CTO sets up a second org: its first store commit is authored with the second org's email.
11. Fake secrets built at run time (an `AKIA…` key and a `ghp_…` token) pasted into a task comment, a decision, a plan, a reminder, a commit message and a code file are refused or masked everywhere; the allow command the refusal prints works.

## What must hold

- Nothing internal (actor details, org notes, internal ids, emails) ever lands in the client repo's tree or history.
- Standing rule 1: no remote moves without a person's push or sync (CI pushes nothing, the leaver's sync pushes nothing after removal).
- Standing rule 2: the PHP app stays byte-identical until someone runs `lore init` there.
- Standing rules 4, 5, 6; no fake secret value printed by lore anywhere (output, files, remotes).

## Findings this story guards

| Finding | Round id | What | Guarded by |
|---|---|---|---|
| D-63 | r2 D-6 | setup team step writes the internal url into the client repo | `TestStoryFClientCIPeople` step 4 |
| D-64 | r2 F-4 | check leaks lets actor ids and org emails into a client commit | `TestStoryFClientCIPeople` step 4 |
| D-65 | r2 F-2 | render in a linked client repo fails `E_NOT_A_STORE` | `TestStoryFClientCIPeople` step 3; `TestJourneyClientRepoStaysQuiet` |
| D-66 | r2 F-3 | check leaks --ci in a linked client repo fails | `TestStoryFClientCIPeople` step 3 |
| D-67 | r2 F-1 | org link on an empty remote fails "no branch main" | `TestStoryFClientCIPeople` step 1 |
| D-68 | r2 D-7 | first write after org link fails | `TestStoryFClientCIPeople` step 2 |
| D-76 | r2 F-5 | comment on a client-repo row refused | `TestStoryFClientCIPeople` step 2 |
| D-77 | r2 D-12 | plan add writes into the client's `.lore/data` | `TestStoryFClientCIPeople` step 2 |
| D-78 | r2 D-13 | package inside a linked client repo unlinked | `TestStoryFClientCIPeople` step 2 |
| D-79 | r2 F-6 | read-only HOME in CI: every read fails | `TestStoryFClientCIPeople` step 5; `TestJourneyCIShallowReadOnlyHome` |
| D-80 | r2 F-7 | doctor DEGRADED on a depth-1 clone | `TestStoryFClientCIPeople` step 5; `TestJourneyCIShallowReadOnlyHome` |
| D-81 | r2 F-8 | reads create `.lore/lore.db-wal` in a CI clone | `TestStoryFClientCIPeople` step 5 |
| D-85 | r2 F-9 | removed member's unsynced write pushed by their sync | `TestStoryFClientCIPeople` step 7 |
| D-86 | r2 F-10 | reassign to a person who left: wrong hint | `TestStoryFClientCIPeople` step 7; `TestJourneyTeamHandover` |
| D-87 | r2 F-11 | revoked access: "committed, check the address" | `TestStoryFClientCIPeople` step 8 |
| D-92 | r2 F-12 | refused client write leaves an audit file | `TestStoryFClientCIPeople` step 3; `TestJourneyClientRepoStaysQuiet` |
| D-93 | r2 F-13 | decision/reminder add in a client repo wires hooks | `TestStoryFClientCIPeople` step 2 |
| D-95 | r2 F-14 | comment `--secret-unstored` installs hooks in a client repo | `TestStoryFClientCIPeople` step 11 |
| D-96 | r2 D-17 | doctor DEGRADED forever after agent start | `TestStoryFClientCIPeople` step 6 |
| D-98 | r2 F-15 | `--to <org>/<folder>` in a no-lore repo refused | `TestStoryFClientCIPeople` step 9; `TestJourneyTeamHandover` |
| D-99 | r2 F-16 | second org's first commit has the first org's email | `TestStoryFClientCIPeople` step 10 |
