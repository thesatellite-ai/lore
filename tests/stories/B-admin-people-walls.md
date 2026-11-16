# Story B — the admin, the people and the walls between them

Test: `tests/stories/b_admin_test.go` (`TestStoryBPeople`, `TestStoryBSecrets`).

## Who

Cora is the CTO and the admin of the org acme, with a team store on the (fake) GitHub and the company's app repo. Lio is the lead, Nia joins in week 2, Ruth leaves in week 3. Everyone has their own laptop (a sandbox HOME with a made-up git identity) and the app cloned under `~/code`, inside the setup's folders.

## Steps

1. Day 1: Cora runs the guided setup as the admin of a new org with the team store, inits the app, links it to the org and commits and pushes it with git.
2. Cora adds the staff from a CSV that has a malformed email and a duplicate: one run names every bad line by its file line (`line 3:`, `line 4: … is already on line 2`) and writes nothing.
3. Cora fixes the CSV and adds Lio, Nia and Ruth in one run, then adds a company rule to the org store from the app (`lore rule add --to acme/org --body …`). The team store does not move until Cora runs `lore sync`; then it does.
3a. From `~/code` (the parent of the clones, inside the setup's folders, not a repo) Cora reads what she wrote: `rule list --to acme/org`, `search --query … --to acme/org`, and with no flag `rule list`, `lint` and `actor list` (the org's stores). Nothing says "run git init", and `~/code` stays a plain folder (D-421).
3b. Cora wants every open task and proposed decision of the company in one command: at the store clone's top (`<home>/stores/<org>/<store>`) `task list`, `decision list`, `search` and `sql` cover every folder, each row labelled `acme/<folder>`; inside one folder they cover that folder, and `--store acme` (or `--store acme/<other folder>`, `--project <folder>`) widens it; from `~/code` `--store acme` covers every folder too, and `memory edit --id …` / `move --id … --to acme/<folder>` work there; in a folder of the clone `task edit --id … --assign @lio` finds the roster, and `task next` says it is a store clone, not a code repo, naming `lore org link` (D-431, D-433, D-434, D-454).
3c. Cora reads the company store from inside it: `project list` gives each folder's row count; in the `sync` folder `project member list` lists sync's people (`--project sync` warns only about the teams sync uses, never a client's team), `resource list --kind <org kind>`, `resource show` and `resource report` see the org folder's kinds, and `show --id <id>` finds a row of the `ops` folder; `show --id <id> --json` prints the whole row: fields with lists as lists, the origin with the store's name, why git is `none`, the lore version and machine, the audit with names, the tags and the rows pointing at it. From a code repo linked to `sync`, `task list --store acme/ops` reads ops read-only, and a repo attaching the store whole gets every folder in `lore sql`, each place once.
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
- Reads, `repo add --project` and a resource of an org-folder kind from a plain folder (D-421, D-422): [outside_reads_test.go](../journeys/outside_reads_test.go).
- The admin updates servers with `resource import --upsert-by name` from a file that lists only the changed column (merged with the stored row), and a resource's size warning counts its values, not its row file (devco:D-452): `ops/settings` (`TestResourceUpsertMergesRequiredFields`, `TestResourceSizeWarningCountsValues`).
- The admin's weekly resource health pass: `resource stale` and `resource report` give the same "to check" numbers, report says where each vault entry is used (rotate only when offboarding), and lint names a domain whose renewal date is empty or past (devco:D-450): `ops/settings` (`TestResourceHealthAgrees`, `TestCheckStateOf`).
- The org-wide view from a store clone's top, a folder and a plain folder; edits and moves by id from a plain folder; --assign and coordination in a store folder (devco D-431, D-433, D-434, D-454): [storescope_test.go](../journeys/storescope_test.go).
- An admins-only store (servers, domains, accounts) in the same org: a member with no access to it joins, syncs and runs doctor with no error, never sees its notes, and keeps own/client guards; no repo links to it; a member made admin gets it on the next sync; the store is opened to everyone again with one command (devco D-442): [adminstore_test.go](../journeys/adminstore_test.go).
- Row counts, member lists and org kinds in a store folder; show --json with everything and any row by id from any folder; --store <org>/<folder> and sql over every folder from a code repo (devco D-443, D-444, D-445, D-446, D-455, D-456, D-458): [storereads_test.go](../journeys/storereads_test.go).
- Looking things up by the names people use: `--id` on every show, `decision show --id D5`, `decision list --status proposed`, `project show` in a store folder with no id, project names in `member show` (devco D-449): [showflags_test.go](../journeys/showflags_test.go).
- The admin setup run by a wrapper with `LORE_ACTOR=agent:<label>` in the admin's coding agent: a refused team step of the new org leaves nothing behind (no store clone, no org entry, no push), the retry completes and every row and commit is the admin's (devco D-440): [agent_setup_actor_test.go](../journeys/agent_setup_actor_test.go).
- Six processes, half of them coding-agent sessions, add notes into a store folder nobody has written into yet (each with `--create`) while others read in the app repo and the store clone: every note is committed, none is lost, no E_STALE or E_CONTENDED, and the reads leave the app repo's tree and `.lore/lock` as they were (devco D-437, D-465, D-470): [parallel_writers_test.go](../journeys/parallel_writers_test.go).
- `--store <org>` reads the whole org the same from a linked repo, an unattached repo, a store clone, the home folder and a repo no setup covers; a read naming a folder the store lacks says it does not exist yet and names `store folder list --store`, never `--create` (devco D-471, D-472); `lore sql --store <org>` and `--store <org>/<folder>` read the same, each row once, labelled `<org>/<folder>` (devco D-473): [storereads_test.go](../journeys/storereads_test.go).
- `lore context --for-prompt` and `--for-file` inside a folder of the company store clone print each rule and memory once, as in a linked code repo; rule list, memory list and search there too (devco D-479): [storefolder_context_test.go](../journeys/storefolder_context_test.go).
- A company store of 16 and of 40 folders: `search` and `sql` read every folder from the store clone's top, a plain setup folder, `--setup <setup>`, `--store <org>` and a code repo attaching it whole, with the same rows, and never stop at SQLite's attach limit (devco D-478): [storemany_test.go](../journeys/storemany_test.go).
- A code repo attaching the company store whole (no folder): `rule list`, `task list`, `search` and `lore sql` all read every folder of it, and `--store <store>` keeps the whole store's folders; inside one folder of the store clone `search --all-repos` covers every folder of that store (devco D-484, D-485): [storereads_test.go](../journeys/storereads_test.go).
- A second org on one laptop (D-38): [setup_secondorg_test.go](../journeys/setup_secondorg_test.go).
- A team named with a dash (`acme-ops`), and the admin's second laptop joining as `member`: saved as an admin setup and the join step says why (devco D-451, D-447); the setup texts name the lore home in use, the identity step's email, the roster's email among git's and an includeIf address (devco D-448): [r11misc_setup_test.go](../journeys/r11misc_setup_test.go).
- A person the admin added with `--name` and `--github` joins on a laptop whose global git names someone else: the join lines carry the roster's name and GitHub account and a `Prefilled:` line says where each came from; a person added with neither keeps git's name (devco D-487): [join_rosterprefill_test.go](../journeys/join_rosterprefill_test.go).
- The admin setup run through the admin's coding agent: the org's first rows and commits are the admin's, no agent actor and no `Lore-Agent:` trailer, and an agent actor merged into its person from a folder that is no repo (devco D-424): [agent_setup_credit_test.go](../journeys/agent_setup_credit_test.go).
- A new laptop with only git access (D-39): [newlaptop_join_test.go](../journeys/newlaptop_join_test.go).
- A day offline, then sync (D-41): [offline_test.go](../journeys/offline_test.go).
- A finding in an ordinary repo file and its allow line: [allow_file_test.go](../journeys/allow_file_test.go).

## Findings it guards

D-7 (B-2), D-22 (B-7), D-25 (B-12), D-43 (B-16), D-421, D-431, D-433, D-434, D-437, D-440, D-441, D-449, D-454, D-470, D-478, D-479, D-484, D-485, D-487, and the owner's rule that no secret value is printed: rows in `tests/stories/REGRESSIONS.md`.
