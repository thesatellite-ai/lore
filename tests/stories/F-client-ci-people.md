# Story F — a client repo, a contractor, CI variants and people changes

Dev-company round 2, track F (plus the client-repo findings of track D). Replayed by `TestStoryFClientRepo` (steps 1-4 and 11), `TestStoryFCI` (step 5) and `TestStoryFPeople` (steps 6-10) in `F_client_ci_people_test.go` (build tag `stories`); the three parts run in parallel. The contractor whose only store is the client store, over months, is the agency persona's journey (`TestJourneyAgencyFirstWeeks`, `TestJourneyAgencyClientMoves`, `TestJourneyAgencyLeaves`); this story replays the client-repo work an org member does there.

## Who

- An org with a CTO (admin), a lead, a newhire who joins in week 2 and a leaver who leaves in week 3.
- A contractor who works only in the client's repo and sees only the client store.
- CI: a fresh clone every run.
- A second org on the CTO's laptop.
- Repos, generated: a client's monorepo with no lore data (the client repo), a PHP-style app with no lore data, an internal repo the CTO adopts lore in, and for CI the same internal repo still in the lore 0.1.17 format 1.

## Steps

1. The CTO adds a client store on an empty bare remote (`lore org store add`), then links the client repo (`lore org link --store`); both work on the empty remote (no "no branch main").
2. The CTO works in the client repo: a task (the first write after the link works the first time), a plan, a decision, a reminder, a comment on the task, and a task from a package folder (`libs/ui`) of the linked repo. Each lands in the linked client folder, never in the client repo's tree, and wires no hooks there.
3. `lore render` and `lore check leaks --ci` in the linked client repo work (no `E_NOT_A_STORE`, no "run from the project root"). A write refused for a fake secret and a comment with `--secret-unstored` leave no `.lore/state/audit-v2.jsonl`, no hooks, no merge driver, no `.lore/` in the client tree, and the secret is never printed.
4. Staged code that carries the CTO's actor id is refused by `lore check leaks`; a clean commit passes `lore check leaks --as-client` and is pushed; the client remote's whole history holds no actor id, no teammate's email, no org name and no store address. `lore setup apply --step team` run in the client repo writes nothing into it.
5. CI on the 0.1.17-format internal repo: a `git clone --depth 1`, a detached HEAD on a pull request's merge ref (`refs/pull/1/merge`), a read-only HOME (`chmod a-w`, the workspace stays writable), no git identity, `CI=true` and `GITHUB_ACTIONS=true`. `lore check leaks`, list and search exit 0, `lore doctor` is not DEGRADED; nothing changes in the clone (tree and `git status --ignored`, so no `.lore/lore.db-wal`), the format stays 1, nothing is pushed. (`render --check` of a fresh clone and `check leaks --ci` are `TestJourneyCIShallowReadOnlyHome`'s; this generated 0.1.17 project is over the render budget and holds 0.1.17 actor copies, which those checks rightly report.)
6. Week 2: the newhire and the leaver join with the line `lore team invite` printed; the newhire sees the org's rule. The newhire starts an agent session (`lore agent start`); the CTO's doctor never says an actor is missing from the roster.
7. Week 3: the leaver has two open tasks. `lore member remove` names them and prints the reassign line; `--assign` naming the leaver says they left (never "register"); `lore task reassign --from <leaver> --assign @nia` hands both over. A write the leaver makes before learning of the removal is not pushed by their next `lore sync`, which says they are marked as left and the work is kept on their machine.
8. The team store stops answering the newhire (its remote is gone, as a host answers once access is removed): a write is committed locally and never sends them to "check the store's address"; `lore doctor` says access was most likely removed and not to run `lore store init` over it.
9. A write with `--to <org>/org` inside the PHP app (no lore data) goes to the org store without asking for `lore init`; the app stays byte-identical.
10. The CTO sets up a second org on the same laptop: no commit in its store is authored with the first org's email.
11. (In step 3.) Fake secrets are built at run time (`FakeSecret`) and never appear in any output.

## What must hold

- Nothing internal (actor details, org notes, internal ids, emails) ever lands in the client repo's tree or history.
- Standing rule 1: no remote moves without a person's push or sync (CI pushes nothing, the leaver's sync pushes nothing after removal).
- Standing rule 2: the PHP app stays byte-identical until someone runs `lore init` there.
- Standing rules 4, 5, 6; no fake secret value printed by lore anywhere (output, files, remotes).

## Findings this story guards

| Finding | Round id | What | Guarded by |
|---|---|---|---|
| D-63 | r2 D-6 | setup team step writes the internal url into the client repo | `TestStoryFClientRepo` step 4 |
| D-64 | r2 F-4 | check leaks lets actor ids and org emails into a client commit | `TestStoryFClientRepo` step 4 |
| D-65 | r2 F-2 | render in a linked client repo fails `E_NOT_A_STORE` | `TestStoryFClientRepo` step 3; `TestJourneyClientRepoStaysQuiet` |
| D-66 | r2 F-3 | check leaks --ci in a linked client repo fails | `TestStoryFClientRepo` step 3 |
| D-67 | r2 F-1 | org link on an empty remote fails "no branch main" | `TestStoryFClientRepo` step 1 |
| D-68 | r2 D-7 | first write after org link fails | `TestStoryFClientRepo` step 2 |
| D-76 | r2 F-5 | comment on a client-repo row refused | `TestStoryFClientRepo` step 2 |
| D-77 | r2 D-12 | plan add writes into the client's `.lore/data` | `TestStoryFClientRepo` step 2 |
| D-78 | r2 D-13 | package inside a linked client repo unlinked | `TestStoryFClientRepo` step 2 |
| D-79 | r2 F-6 | read-only HOME in CI: every read fails | `TestStoryFCI` step 5; `TestJourneyCIShallowReadOnlyHome` |
| D-80 | r2 F-7 | doctor DEGRADED on a depth-1 clone | `TestStoryFCI` step 5; `TestJourneyCIShallowReadOnlyHome` |
| D-81 | r2 F-8 | reads create `.lore/lore.db-wal` in a CI clone | `TestStoryFCI` step 5 |
| D-85 | r2 F-9 | removed member's unsynced write pushed by their sync | `TestStoryFPeople` step 7 |
| D-86 | r2 F-10 | reassign to a person who left: wrong hint | `TestStoryFPeople` step 7; `TestJourneyTeamHandover` |
| D-87 | r2 F-11 | revoked access: "committed, check the address" | `TestStoryFPeople` step 8 |
| D-92 | r2 F-12 | refused client write leaves an audit file | `TestStoryFClientRepo` step 3; `TestJourneyClientRepoStaysQuiet` |
| D-93 | r2 F-13 | decision/reminder add in a client repo wires hooks | `TestStoryFClientRepo` step 2 |
| D-95 | r2 F-14 | comment `--secret-unstored` installs hooks in a client repo | `TestStoryFClientRepo` step 3; `TestJourneyClientRepoStaysQuiet` |
| D-96 | r2 D-17 | doctor DEGRADED forever after agent start | `TestStoryFPeople` step 6 |
| D-98 | r2 F-15 | `--to <org>/<folder>` in a no-lore repo refused | `TestStoryFPeople` step 9; `TestJourneyTeamHandover` |
| D-99 | r2 F-16 | second org's first commit has the first org's email | `TestStoryFPeople` step 10 |
