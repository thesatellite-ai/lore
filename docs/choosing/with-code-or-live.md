# Keep a note with the code, or share it at once

**In short:** a note "with the code" is a file you commit on your branch. It reaches `main` when your pull request merges, just like the code next to it. A "live" note reaches everyone on their next lore command, whatever branch they are on, without any commit. Tasks are live by default; rules, memories and decisions stay with the code. Pass `--live` or `--no-live` to choose for one note.

## The two places

| | With the code (`--no-live`) | Live (`--live`) |
|---|---|---|
| Where it is written | `.lore/data` on the branch you are on | a hidden list on the git server that is not a branch (git calls it a ref: `refs/lore/live`), which lore pushes and fetches by itself |
| Who sees it, and when | people on that branch; everyone else after the branch merges | everyone, on their next lore command, on any branch |
| Needs a commit | yes, you commit it with your change | no |
| Reaches `main` | when the branch merges | by itself when it is finished, with `lore land` (copy a live note onto `main`), or as a copy after 7 days |
| Default for | rules, memories, decisions and every other kind of note | tasks, missions, handoffs and comments |
| Good for | "this rule belongs to this change": it is reviewed and merged with the code | "everyone must know now": a task for a teammate, an urgent warning |

A note is **finished** when it is done or cancelled (tasks), or superseded or rejected (decisions; lore stores "rejected" as the status `deprecated`). Rules, memories, notes and comments count as finished on every write, so a live rule also lands a copy on `main` right away. A row is one note. When `main` is protected and refuses direct pushes, the finished rows ride along: lore adds a separate commit to your branch on your next push, and it reaches `main` with your pull request.

Lore fetches once per command and reuses that fetch for 60 seconds; `lore sync` always fetches. So a teammate's live note can take up to a minute to show up.

## If this, then that

- **If you give a teammate a task,** do nothing special: tasks are live, so `lore task add 'Send the invoice reminder before Friday' --assign @bob` reaches Bob (another developer on your team) on his next command, even on another branch.
- **If you add a rule that only makes sense with your change** ("Run the tests before you push" in the branch that adds the tests), keep it with the code. It is reviewed in the same pull request.
- **If a rule is urgent for everyone** ("Never deploy on a Friday"), add it with `--live`. Everyone sees it now, and a copy lands on `main` at once.
- **If you want your tasks to follow your branches,** turn live off for tasks in `~/.lore/config.yaml` with `live: {defaults: {tasks: false}}`, or pass `--no-live` per task.
- **If you want no network use for one shell** (a plane, a slow link), set `LORE_LIVE=0` (every row waits for a commit) or `LORE_LIVE_AUTOPUSH=0` (live writes wait on your machine until `lore sync`; claiming a task, which puts your name on it, then fails with `E_OFFLINE` and is never queued).
- **If a note with the code must reach `main` before the pull request merges,** copy it there with `lore sync promote` instead of making it live after the fact.
- **If the repo has no remote yet,** live rows say `(live, local: no remote)` and stay on your machine; `lore land --all --dry-run` shows what will land once there is a `main` on a remote.

## Pitfalls

- **Hidden is not private.** Anyone who can clone the repo can fetch the live list. `--live` decides *when* people see a note; `--to` decides *who* (see [How to lay out your stores](store-layout.md)). In a client's repo, a live note is a note the client can read, unless it goes to your store (a separate private repo that only you can open).
- **Live is temporary.** ZIP downloads, forks, plain mirrors and shallow clones do not carry the live list. That is why open rows are copied to `main` after `live.max_age` (7 days by default).
- **If a note is in both places, the live copy wins,** always. Use `lore explain <id>` to see where a row is and why.
- **No live rows in a folder store.** Live rows hang off the repo's own git remote; notes written `--to` a folder store are never live ([Where your notes live](database-mode.md)).
- **A branch note stays on its branch.** A rule you add on `feat/x` is invisible on `main` until `feat/x` merges. Read it from elsewhere with `lore sync peek`.

## Guides and examples for each option

| Option | Guide | Example |
|---|---|---|
| With the code | [How notes travel with git](../sync/how-notes-travel-with-git.md), [Branches: peek and promote](../sync/branches-peek-promote.md) | [`sync`](../../tests/docs/sections/sync.sh) ([output](../../tests/docs/transcripts/sync.txt)) |
| Live | [Small team in one repo](../team/use-cases/small-team.md), [How it works: writing to live](../team/how-it-works.md#writing-to-live) | [`small-team`](../../tests/team/docs/transcripts/small-team.txt), [`getting-started`](../../tests/team/docs/transcripts/getting-started.txt) |
| Tasks with the code | [Solo developer](../team/use-cases/solo.md) | [`solo`](../../tests/team/docs/transcripts/solo.txt) |
| Landing and protected `main` | [How it works: landing](../team/how-it-works.md#landing), [protected main](../team/how-it-works.md#protected-main-the-ride-along-commit) | [`small-team`](../../tests/team/docs/transcripts/small-team.txt) |

## Related

- [Concepts: branch data, live rows and signals](../team/concepts.md#branch-data-live-rows-and-signals)
- [Which command for which job](workflow-methods.md#merging-notes-hooks-and-merge-driver-or-the-ci-merge): how git merges notes on your machine and on GitHub.
- [Which storage backend and cache](backend.md)
- [Where your notes live](database-mode.md)
- [All decision guides](README.md)
