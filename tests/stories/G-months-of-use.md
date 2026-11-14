# Story G — months of use: volume, lifecycle, renames, many agents

Dev-company round 3, track G. Replayed by `TestStoryGMonthsOfUse` (volume, lifecycle, docs-only repo), `TestStoryGRenames` and `TestStoryGManyAgents` in `G_months_of_use_test.go` (build tag `stories`).

## Who

- A CTO, a lead and three developers, display names with accents on purpose (Chloé, Zoë).
- Ten coding-agent sessions (`LORE_ACTOR=agent:<label>`) in the same repo and store.
- Repos, generated: a Go API (about 100 rows, format 1, migrated by the CTO in week 1), a Go library (about 35 rows, format 1) and a docs-only repo (markdown only, about 380 rows, format 1, no code).

## Steps

1. Volume over "three months" (the test clock moves; due dates and reminders spread over the months): 500 tasks, 200 memories, 50 decisions, 30 plans and missions written by five people in parallel, plus 80 must rules. No write is lost: every count matches in every clone after sync.
2. Search quality: an exact title, a word inside a body, accents both ways (`Chloé`/`Chloe`, `naïve`/`NAÏVE`), an emoji. A search with no hits says so; rows read through from the fetched default branch are found too. List paging past the end says so.
3. LORE.md after months stays readable and inside its budget; when the rules outgrow it, must rules still render with their text, not as bare ids. `lore doctor` does not list a LORE.md at 8% of budget as a warning.
4. Every `git pull --rebase` that brings rows leaves the tree clean, so the next pull works.
5. Lifecycle: reminders come due and pass due; overdue reminders show in `lore inbox` and render, not only in `lore reminder list`. A mission is completed with its tasks archived; 50 old memories are archived in bulk with one command; trash and purge after months finish in a few seconds; `lore task stale --days 0` means zero days, not the default.
6. Renames: a developer changes email and display name (whoami shows one actor); a team is renamed and team-assigned tasks reach members' inbox; the org store moves to a new url (`lore store set-url`) and sync names set-url instead of "offline"; the repo's remote url changes (a GitHub rename) and writes keep working; the old store clone folder is named by `store doctor`. Every note is still found by id and by search after each rename; nobody loses access.
7. Ten agent sessions write for two minutes at once in one clone and one store: `task next` with `--push`, comments, `task done`, `lore sync`. No task is finished twice, `task next` never hands out a done task, no conflict markers are written into a row, sync never fails, and every command stays under the 5 s budget. `task add --help` matches what agents must pass.
8. The docs-only repo: `lore migrate run`, render, the pointer file kept, markdown-only commits stay fast with the hooks on.

## What must hold

- No lost writes: totals equal in every clone and the store after sync.
- Standing rule 1: pushes only on `lore sync`, `git push`, claims with `--push` and `migrate run` followed by a person's push.
- Standing rule 2: the untouched generated repos stay byte-identical.
- Standing rule 3: the Go library stays format 1 until migrated.
- Standing rules 4, 5, 6, 7; no fake secret value printed.

## Findings this story guards

Steps for findings still being fixed are written as they should behave and skipped with the finding id until the fix lands.

| Finding | Round-3 id | What | Guarded by |
|---|---|---|---|
| D-123 | r3 G-1 | repo moved: writes blocked as live ref rewritten | `TestStoryGRenames` (skipped until D-123) |
| D-132 | r3 G-2 | pull --rebase leaves LORE.md modified | `TestStoryGMonthsOfUse` step 4; `TestJourneyPullRebase` |
| D-134 | r3 G-3 | read-through rows never found by search | `TestStoryGMonthsOfUse` step 2 |
| D-135 | r3 G-4 | search with no hits prints nothing | `TestStoryGMonthsOfUse` step 2 |
| D-148 | r3 G-5 | overdue reminders only in reminder list | `TestStoryGMonthsOfUse` (skipped until D-148) |
| D-151 | r3 G-6 | over-budget LORE.md renders must rules as ids | `TestStoryGMonthsOfUse` (skipped until D-151) |
| D-152 | r3 G-7 | no bulk archive | `TestStoryGMonthsOfUse` (skipped until D-152) |
| D-155 | r3 G-8 | trash purge slow | `TestStoryGMonthsOfUse` (skipped until D-155) |
| D-156 | r3 G-9 | doctor warns at 8% of budget | `TestStoryGMonthsOfUse` (skipped until D-156) |
| D-157 | r3 G-10 | tasks finished many times, next hands out done tasks | `TestStoryGManyAgents` (skipped until D-157) |
| D-158 | r3 G-11 | nested conflict markers in done_by | `TestStoryGManyAgents` (skipped until D-158) |
| D-159 | r3 G-12 | commands over 5 s under 10 agents | `TestStoryGManyAgents` (skipped until D-159) |
| D-160 | r3 G-13 | `--commitment` help wrong for agents | `TestStoryGManyAgents` (skipped until D-160) |
| r3 G-14 | (no D- id yet) | email change: whoami shows a second actor | `TestStoryGRenames` (skipped until r3 G-14) |
| r3 G-15 | (no D- id yet) | team tasks never reach the inbox; team rename | `TestStoryGRenames` (skipped until r3 G-15) |
| r3 G-16 | (no D- id yet) | org store moved: sync says offline | `TestStoryGRenames` (skipped until r3 G-16) |
| r3 G-17 | (no D- id yet) | merge drops a confirmed repo url | `TestStoryGRenames` (skipped until r3 G-17) |
| r3 G-18 | (no D- id yet) | repo moved: E_STALE address warning | `TestStoryGRenames` (skipped until r3 G-18) |
| r3 G-19 | (no D- id yet) | old store clone left after set-url | `TestStoryGRenames` (skipped until r3 G-19) |
| r3 G-20 | (no D- id yet) | `task stale --days 0` runs with 60 | `TestStoryGMonthsOfUse` (skipped until r3 G-20) |
