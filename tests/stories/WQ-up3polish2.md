# WQ — proofs for the third use pass's polish

**Who.** Ada (made-up names) is the admin of the org `acme`; her team store is `github.com/acme-co/lore-team` and her repo `~/code/platform` is in the setup's folders. Ben joins later from his own laptop with an invite.

**Why.** The fixes for the owner's third use pass (D-646..D-652) were checked by unit tests, but four things were never replayed the way the owner met them: the sync counts after a set-url round trip and on a pull, a raw actor id in doctor (the matrices' rule (f) let an agent's id pass as an unknown writer), the commands printed by the changed lines, and roster sentences still saying "the roster in acme org/".

**Test.** `TestStoryWQUp3Polish2` (tests/stories/WQ_up3polish2_test.go), about 20 s; the guard's own check is `TestStoryWRawActorIDs`.

## Steps

1. Ada starts the team in `~/code/platform`.
2. From the home folder and in `platform`: `lore member show --who nobody@example.test --setup acme-admin`; in `platform`: `lore team show --handle @ghost`, `lore team add --handle @web …`, `lore team remove --handle @web --yes`.
3. Every printed command is run where it was printed: `org config set` without `--yes`, `resource list --kind server` (and its `--wide` / `--json`), `team list`, `member show`, `actor register --setup acme-admin` from the home folder, a memory deleted and `trash list --store acme/org` in the repo and the home folder, then two days later `setup list` and `setup show` after a `setup check`.
4. A memory to `acme/org` and `lore store push --store acme`; the store moves to another GitHub owner and back with `store set-url --name acme`; two memories added and edited; `lore sync --setup acme-admin` from the home folder, the remote's commit count read before and after.
5. Ben joins, adds and edits two memories and syncs each; Ada's `lore sync --setup acme-admin` from the home folder, her store clone's `git rev-list --count <before>..HEAD` read after.
6. Ada's and Ben's coding agents (Claude Code sessions) each add a memory; Ben syncs, Ada pulls; both agents' actors rows are then taken out of Ada's store clone (an agent whose saves arrive before its actors row). `lore doctor` in `platform`, in a repo linked to `acme/linked`, in the store clone and its `org/` folder, and in the home folder; `lore doctor --all`.

## What must hold

- Every roster sentence names the roster as `the roster of acme (acme/org)`, never `the roster in acme org/` or `acme org/'s roster`.
- Every concrete `lore …` command an output prints runs where it was printed (wyFollow): no refusal for a wrong hint, no E_NOT_A_STORE, no loop back to the same hint. The roster miss hint names the teammate's commands with `--setup <setup>`.
- `sent N` on the setup sync's store line is the number of commits `git rev-list` counts going out on the remote, and `received N` the number of commits that arrived in the clone, also when rows and commits differ (edits) and when the commits arrived through the refresh before the sync.
- No doctor output prints an act_ id of a known person or of a detected agent of one where a name belongs (rule (f), whose known set now holds each listed actor's detected-agent ids in every tool lore detects); an id in parentheses right after the name is allowed.

## Findings it guards

- `devco:D-660`..`devco:D-663` (REGRESSIONS.md, owner use pass 3 leftovers of D-646..D-652).
