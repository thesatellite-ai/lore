# Story B — the admin, the people and the walls between them

Test: `tests/stories/b_admin_test.go` (`TestStoryBPeople`, `TestStoryBSecrets`).

## Who

Cora is the CTO and the admin of the org acme, with a team store on the (fake) GitHub and the company's app repo. Lio is the lead, Nia joins in week 2, Ruth leaves in week 3. Everyone has their own laptop (a sandbox HOME with a made-up git identity) and the app cloned under `~/code`, inside the setup's folders.

## Steps

1. Day 1: Cora runs the guided setup as the admin of a new org with the team store, inits the app, links it to the org and commits and pushes it with git.
2. Cora adds the staff from a CSV that has a malformed email and a duplicate: one run names every bad line by its file line (`line 3:`, `line 4: … is already on line 2`) and writes nothing.
3. Cora fixes the CSV and adds Lio, Nia and Ruth in one run, then adds a company rule to the org store from the app (`lore rule add --to acme/org --body …`). The team store does not move until Cora runs `lore sync`; then it does.
4. Day 8: Nia joins on her own laptop by the store url (she is on the roster, matched by her email), `lore whoami` names the org and her email, and she sees the company rule.
5. Cora gives Ruth two tasks.
6. Day 15: Cora removes Ruth: the output lists her two open tasks and the hand-over line (`lore task reassign --from …`); Cora runs it, both tasks go to Nia, and Ruth shows as left.
7. Secrets (a fresh org): Cora pastes a token (built at run time by `FakeSecret`) into a memory: the write is refused, and neither the refusal, `memory list`, `search` nor `check leaks --rows` prints the token or its tail; no file in the repo holds it.
8. Cora commits a config file holding the token: the commit hook blocks it naming the file and prints a ready `lore secrets allow --file <path> --rule <rule> --match-hash sha256:<hash>` line, never the value.

## What must hold

- Nothing pushes without push/sync: `member add --from` and `rule add --to acme/org` commit to the store clone only; the team store's refs move only with `lore sync` (D-43).
- No secret value printed: not the token, not its last 12 characters, in any output of steps 7-8.
- Named flags: every hint printed (the CSV refusal, the hand-over line, the leak refusal) uses named flags.
- The shared journey invariants hold after every step on every laptop (readable output, doctor, config, file modes).

## Journeys that already walk parts of this

The story replays the company's timeline in one world; the details of each part are in these journeys, which the story does not repeat:

- Staff from a CSV, roles, teams, joining by email: [tests/journeys/staff_test.go](../journeys/staff_test.go); the printed join line on a fresh machine and without a terminal (D-24): [staff_joinline_test.go](../journeys/staff_joinline_test.go).
- Removing a member, their unsynced writes after removal, undoing it: [staff_remove_test.go](../journeys/staff_remove_test.go); the hand-over and `--assign` naming a leaver (D-25, D-86): [team_handover_test.go](../journeys/team_handover_test.go).
- A client store and the contractor's wall (D-21, D-23): [orgstoreadd_test.go](../journeys/orgstoreadd_test.go); a client repo stays quiet (D-28, D-63..D-66, D-76, D-77, D-92, D-93): [clientrepo_quiet_test.go](../journeys/clientrepo_quiet_test.go).
- A second org on one laptop (D-38): [setup_secondorg_test.go](../journeys/setup_secondorg_test.go).
- A new laptop with only git access (D-39): [newlaptop_join_test.go](../journeys/newlaptop_join_test.go).
- A day offline, then sync (D-41): [offline_test.go](../journeys/offline_test.go).
- A finding in an ordinary repo file and its allow line: [allow_file_test.go](../journeys/allow_file_test.go).

## Findings it guards

D-7 (B-2), D-22 (B-7), D-25 (B-12), D-43 (B-16), and the owner's rule that no secret value is printed: rows in `tests/stories/REGRESSIONS.md`.
