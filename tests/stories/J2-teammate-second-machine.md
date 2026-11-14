# J2 — a teammate on a second machine

Test: `TestStoryJ2TeammateSecondMachine` in [j2_teammate_test.go](j2_teammate_test.go). Track: the bug hunt's journey 2 (a teammate joins the admin's org from another machine), replayed on generated repos.

## Who

Lena Lead (lena.lead@example.test, lena-gh) starts the org brightco; Mo Mate (mo.mate@example.test, mo-gh) joins on their own laptop and later sets up a second laptop (the desk). The code repo is `github.com/bright-co/app`, the team store `github.com/bright-co/lore-team`.

## Steps

1. Day 1: Lena runs the guided admin setup, `lore init` in the app, commits and pushes it, adds a memory and a task to the team folder `brightco/platform` (`--to brightco/platform`), links the repo to that folder (`lore org link`), syncs and invites Mo (`lore team invite`), which prints a `lore-invite:` line.
2. Mo pulls the app, runs the guided member setup with that invite line, and sees the team's memory and task; `lore whoami` names the org and Mo's email.
3. Day 2: Lena raises the task's priority while Mo renames it; both sync.
4. Day 3: Mo opens the team note's row file in their store clone, changes the text by hand, and commits and pushes it with plain git (their own lore hooks run). Lena syncs and reads the new text.
5. Week 2: Mo gets a second laptop. In a fresh clone of the app, `lore whoami` says how to join (`lore setup join --invite`); Mo runs the member setup with the team store's url, matched by email, and sees everything: the hand-edited note and the task with both changes.

## What must hold

- Nothing pushes without push or sync: the two task edits move neither the team store nor the app remote until each person runs `lore sync`.
- Both people's changes to one row are kept when they touch different fields (the title from one, the priority from the other), on both laptops and on the second laptop.
- A hand edit committed and pushed with plain git in a store clone reaches the team at their next sync.
- A store commit is titled by what changed, never by an actor id; `~/.lore/cache` never holds both `repo/` and `repos/`.
- Named flags in every sync hint; the shared journey invariants after every step.

## Linked journeys

- `tests/journeys/teamlead_test.go` (`TestJourneyTeamLeadTeam`): invites with teams, the same row edited at once (E_STALE, both edits kept, settled by an edit), a week offline.
- `tests/journeys/joiner_test.go` (`TestJourneyJoiner`): the member setup step by step.
- `tests/journeys/newlaptop_join_test.go` (`TestJourneyNewLaptopJoinHint`): the join hint on a new laptop.
- `tests/journeys/staff_test.go`: member remove and restore.

## Findings guarded

B2-4 (store commit titles), B2-6 (one cache folder), B2-22 (settling a row drops the other side's field change), S-1 / U-2 (edits never push by themselves), U-4 (named flags), D-39 (a new laptop's whoami says how to join).
