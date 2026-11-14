# Story G — months of use: volume, lifecycle, renames, many agents

Dev-company round 3, track G. Replayed by `TestStoryGMonthsOfUse` (volume, lifecycle, docs-only repo), `TestStoryGRenames` and `TestStoryGManyAgents` in `G_months_of_use_test.go` (build tag `stories`).

## Who

- A CTO and two developers, display names with accents on purpose (Chloé, Zoë); five people in the round-3 run.
- Coding-agent sessions (`LORE_ACTOR=agent:<label>`) in the same clone: four at once in the replay (ten in the round-3 run).
- Repos, generated: a code repo the CTO adopts lore in on day 1, a library in the lore 0.1.17 format 1 (it must stay format 1: nobody migrates it), a docs-only repo (markdown only, no code) and a site with no lore data (byte-identical throughout).

## Steps

1. Volume over "three months" (the test clock moves a week per writer; due dates spread over the months): each of three people writes 12 tasks, 6 memories, 3 decisions and 2 plans (plus Chloé's accent and emoji notes), commits, `git pull --rebase` and pushes. The round-3 run wrote 500 tasks, 200 memories, 50 decisions and 30 plans by five people; the replay keeps the shapes and scales the counts down for the gate. No write is lost: every clone counts every task and memory.
2. Search quality: an exact phrase, accents both ways (`Chloe`, `NAÏVE`, `naive` all find "naïve"), an emoji. `lore memory search` with no hits prints `(no matches)`. A task list page past the end prints something, never nothing.
3. Rows read through from the fetched default branch: Zoë pushes a note; Ines has fetched but not pulled; the list shows it and search finds it too.
4. Every `git pull --rebase` that brings rows leaves the tree clean, so the next pull works (checked on every pull of the story).
5. Lifecycle: a reminder past its due date is listed by `lore reminder list`. An overdue reminder also shows in `lore inbox` (in LORE.md and doctor too once D-148-render is fixed: skipped until then); old memories archive in bulk with one `--where`; trash purge finishes in seconds; `lore task stale --days 0` is refused with "--days must be 1 or more", never run as the 60-day default; doctor does not warn about a LORE.md at 8% of its budget; 80 must rules over the render budget keep their text.
6. The docs-only repo: its own `CLAUDE.md` comes first, then `lore init`, a rule and `lore render`; the file keeps its own text; a markdown-only commit with lore's hooks takes under 3 s and leaves the tree clean.
7. Renames (`TestStoryGRenames`): Chloé changes email and display name; her note is still shown by id and found by search, and she keeps writing. whoami shows one actor; the repo moves to a new address and writes and sync keep working. Still empty steps (skipped): team renames reach members' inboxes; a moved org store makes sync name `lore store set-url`; the old store clone is named by `store doctor`. After every rename every note is still found by id on every clone.
8. Many agents (`TestStoryGManyAgents`): four agent sessions add a task each and comment on one task at the same instant: every task and every comment is there, nothing is pushed without `--push`. `task next --push` never hands one task to two agents or a done task out again; concurrent `task done` writes no conflict markers and sync works; `task add --help` matches what agents must pass. Ten agent sessions in one clone then run `task next --push`, `comment add` and `task done` back to back, all at once: the 95th percentile of their command times stays under 5 s (on a loaded machine, under fifteen times one command of a lone agent measured just before).

## What must hold

- No lost writes: totals equal in every clone and the store after sync.
- Standing rule 1: pushes only on `lore sync`, `git push`, claims with `--push` and `migrate run` followed by a person's push.
- Standing rule 2: the untouched generated repos stay byte-identical.
- Standing rule 3: the 0.1.17 library stays format 1 (nobody migrates it).
- Standing rules 4, 5, 6, 7; no fake secret value printed.

## Findings this story guards

Steps for findings still being fixed are written as they should behave and skipped with the finding id until the fix lands.

| Finding | Round-3 id | What | Guarded by |
|---|---|---|---|
| D-123 | r3 G-1 | repo moved: writes blocked as live ref rewritten | `TestStoryGRenames` |
| D-132 | r3 G-2 | pull --rebase leaves LORE.md modified | `TestStoryGMonthsOfUse` steps 1, 3, 4; `TestJourneyPullRebase` |
| D-134 | r3 G-3 | read-through rows never found by search | `TestStoryGMonthsOfUse` step 3 |
| D-135 | r3 G-4 | search with no hits prints nothing | `TestStoryGMonthsOfUse` step 2 |
| D-148 | r3 G-5 | overdue reminders only in reminder list | `TestStoryGMonthsOfUse` (inbox); render and doctor skipped until D-148-render |
| D-151 | r3 G-6 | over-budget LORE.md renders must rules as ids | `TestStoryGMonthsOfUse` |
| D-152 | r3 G-7 | no bulk archive | `TestStoryGMonthsOfUse` |
| D-155 | r3 G-8 | trash purge slow | `TestStoryGMonthsOfUse` |
| D-156 | r3 G-9 | doctor warns at 8% of budget | `TestStoryGMonthsOfUse` |
| D-157 | r3 G-10 | tasks finished many times, next hands out done tasks | `TestStoryGManyAgents` |
| D-158 | r3 G-11 | nested conflict markers in done_by | `TestStoryGManyAgents` |
| D-159 | r3 G-12 | commands over 5 s under 10 agents | `TestStoryGManyAgents` |
| D-160 | r3 G-13 | `--commitment` help wrong for agents | `TestStoryGManyAgents` |
| r3 G-14 | (no D- id yet) | email change: whoami shows a second actor | `TestStoryGRenames` |
| r3 G-15 | (no D- id yet) | team tasks never reach the inbox; team rename | `TestJourneyTeamRename`; `TestStoryGRenames` step still empty (skipped) |
| r3 G-16 | (no D- id yet) | org store moved: sync says offline | `TestJourneyTeamLeadStoreMoved`; `TestStoryGRenames` step still empty (skipped) |
| r3 G-17 | (no D- id yet) | merge drops a confirmed repo url | `TestJourneyRepoMoveMerge` |
| r3 G-18 | (no D- id yet) | repo moved: E_STALE address warning | `TestStoryGRenames` |
| r3 G-19 | (no D- id yet) | old store clone left after set-url | `TestJourneyTeamLeadStoreMoved`; `TestStoryGRenames` step still empty (skipped) |
| r3 G-20 | (no D- id yet) | `task stale --days 0` runs with 60 | `TestStoryGMonthsOfUse` |
