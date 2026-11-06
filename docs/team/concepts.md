# Concepts

**In short:** this page explains the few words lore uses, one at a time. A row is one note or task. Live rows are shared with everyone at once, before any merge. A store is a separate private repo for notes. An org is your company's setup. Your identity is your email.

Each section covers one idea, most with a diagram. The words in **bold** are the names lore uses in its output and errors.

## Git words, once

Lore uses a few git words. You do not need to know how git works inside. These one-liners are enough.

| Word | What it is | Think of it as |
|---|---|---|
| **commit** | a saved snapshot of files | a photo of the folder |
| **branch** | a name pointing at one commit (`main`, `feat/login`) | a bookmark in a book |
| **ref** | any such name; branches are refs under `refs/heads/` | a bookmark, of any colour |
| **hidden ref** | a ref outside `refs/heads/`, like `refs/lore/live`; git fetches and pushes it, but `git branch` does not list it and nobody checks it out | a bookmark kept in a drawer |
| **remote** | the copy of the repo on GitHub (or any git server), usually called `origin` | the shared shelf |
| **bare clone** | a copy of a repo with no working folder, only git's data | a library copy you never open on your desk |
| **default branch** | the branch the remote points at (`origin/HEAD`), almost always `main` | the archive |

## Rows

A **row** is one record: a task, rule, decision, memory, mission, handoff (a note that passes work to the next person), comment or actor (a person or agent). Each row is one JSON file named after its id. It sits inside the `.lore/data` folder of a **project**: a folder with `.lore/` in it, usually the repo root.

```text
.lore/data/
  _meta.json                         which project this is (project_id)
  tasks/tsk_01a1…a739.json           one task
  rules/rul_01a1…ce08.json           one rule
  memories/mem_01a1…54a7.json        one memory
```

A real task file, as lore wrote it (shortened):

<!-- example: internals#monorepo-live-tree only=5 keep=^[{}]|_live|hlc|live_since|"(id|title|status|claim_epoch|project_id)" -->
```console
# carol, in app
$ git cat-file -p refs/lore/live:$(git ls-tree -r --name-only refs/lore/live | grep '_root/tasks/')
{
  "_hlc": "2026-11-02T10:00:10.000Z/0001/act_54fe…28f2",
  "_live": {
    "hlc": "2026-11-02T10:00:10.000Z/0001/act_54fe…28f2",
    "live_since": "2026-11-02T10:00:10Z"
  …
  "id": "tsk_7a67…9504",
  …
  "project_id": "prj_05e9…4603",
  "status": "todo",
  …
  "title": "fix header",
  …
}
```
<!-- /example -->

One id is one file. So two people adding rows never conflict. Writing the same row twice overwrites one file instead of making a duplicate.

## Branch data, live rows and signals

Lore keeps rows in three places in a repo. They differ in **when** others see a change, and **whether** the change builds history.

```text
                  who sees a change            history           ends up on main
 branch data      people on that branch;       yes, like code    when the branch merges
 (.lore/data)     everyone after the merge
 live rows        everyone, on their next      yes, compacted    when finished (landing)
 (refs/lore/live) lore command, any branch     now and then
 signals          everyone, on their next      none: always      never
 (refs/lore/      lore command                 one commit
  signals)
```

**Branch data** is what lore always had: rows in `.lore/data` on the branch you are on. A rule you add on `feat/x` stays on `feat/x` until the branch merges.

**Live rows** are rows written to a hidden list on GitHub that is not a branch: the hidden ref `refs/lore/live`. Git's checkout never touches that ref, so switching branches does not hide them. Lore pushes and fetches it by itself. If a row id exists in live, **the live copy wins**, always. Tasks, missions, handoffs and comments are live by default. Any other row is live when you pass `--live`. `--no-live` keeps a row with the code.

**Signals** are short-lived facts: "`agent-backend` holds this task until 10:20", a heartbeat (an agent's "I am still here"), a hold on the shared test suite. They sit on `refs/lore/signals`, which is replaced in place. It is always exactly one commit, so a thousand heartbeats leave no history. A signal carries an expiry time. An expired signal counts as absent.

The office picture: branches are rooms, each with its own **filing cabinet** (`.lore/data`); `main`'s cabinet is the **archive**. The live ref is a **whiteboard in the hallway** that everyone sees from any room. Signals are **sticky notes by the door**, replaced, never filed.

A monorepo (one repo that holds several projects) has one live ref. Each lore project is a folder in it: `_root/` for the project at the repo root, `web/` for the one in `web/`. A real listing:

<!-- example: internals#monorepo-live-tree only=2,3 -->
```console
# carol, in app
$ git ls-tree -r --name-only refs/lore/live
_live/epoch
_live/meta.json
_root/task_lists/tlt_162d…9666.json
_root/tasks/tsk_7a67…9504.json
$ git log --oneline refs/lore/live
601d285 lore: live 2 rows (_root)
```
<!-- /example -->

## Landing

**Landing** writes a live row into `main`'s `.lore/data`, its lasting home. Then, once lore sees that `main` holds the same content, it removes the row from live. Lore calls this **leaves live**.

```text
 lore task add "…"            lore task done tsk_…          next lore command
        │                            │                             │
        ▼                            ▼                             ▼
  refs/lore/live: tsk  ──▶  commit "lore: land 2 rows" ──▶  main holds the same file:
  (everyone sees it)        pushed to main                   tsk leaves live
```

A row lands when:

- it is **finished**: a task done or cancelled, a decision superseded or rejected (lore stores "rejected" as the status `deprecated`). Rules, memories, notes and comments count as finished on every write, so each write lands a copy right away (the row may stay live while people keep editing it);
- someone runs **`lore land`** (one id, `--all`, `--kind`, `--mission`; `--dry-run` shows the plan);
- it is still open after **`live.max_age`** (7 days by default): a copy lands and the row stays live.

Landing needs no person and no CI. Lore builds the commit on a fresh copy of `origin/main` and pushes it directly. Your local `main` and your working folder are never touched. If `main` is **protected** and refuses direct pushes, the rows **ride along** on your next push. Lore's `post-commit` hook (a small script git runs after each commit) adds a separate commit `lore: land N rows` to your branch. It reaches `main` with your pull request.

## Stores and links

A **store** is a separate private repo that only your company can open, like a locked filing cabinet next to the client's repo. It holds only lore projects, one folder per project, plus a `lore-store.yaml` marker at its root. It is a notebook, never a code repo: lore refuses to use a repo that holds code as a store.

```text
 github.com/agency/notes            (only the agency can read it)
   lore-store.yaml                    marker
   org/.lore/data/                    company rules, roster (the org folder)
   client/.lore/data/                 internal project for the client
   client2/.lore/data/                internal project for client 2
   refs/lore/live/main                live rows of every folder
   refs/lore/signals/main             claims and leases
```

Each folder in a store is a full lore project. It has branch data (on the store's `main`), live rows and signals, and the same landing rules. Lore keeps one bare clone of each store in `~/.lore/clones/` and never checks it out.

A **link** ties a client repo to its internal project: "project `prj_…` at `github.com/client/website` belongs to `client/`". Someone at the agency makes it once with `lore org link agency --project client`. The link is saved **only in the store** (in `client/.lore/data/_meta.json`). So nothing is written to the client's repo, and everyone at the agency finds the link on their next command. Several repos can link to one project. That is a **group**: three client 2 repos, one `client2/` project.

## Orgs, identity and `whoami`

An **org** is one entry under `orgs:` in your `~/.lore/config.yaml`. It is a company you work for. It holds your identity there, its stores, and URL patterns for its own repos (`own:`) and its client repos (`repos:`).

In every repo lore picks **at most one org**, by the first rule that answers:

```text
 1. LORE_ORG=<entry>                 this shell only
 2. lore org use <entry>             this repo on this machine (saved in config.yaml)
 3. .lore/org.yaml in the repo       committed, own repos only
 4. a link in one org's store        project id + remote URL
 5. own: or repos: pattern matches   e.g. github.com/client/*
 6. nothing matched                  no org: your fallback identity, everywhere/ notes only
```

If two entries match at the same rule, that is an error (`E_ORG_AMBIGUOUS`): lore never guesses. `lore whoami` prints the result and the rule that decided it. `lore whoami --explain` prints every rule with hit or miss.

<!-- example: two-orgs#whoami-per-repo only=4 lines=20 -->
```console
# alice, in forked-tool
$ lore whoami --explain
org        none  (matched by: nothing matched)   hint: lore org use <entry> (agency, client3)
identity   Alice Doe <human:alice@example.com>   act_05e9…4603  (from: top-level identity (config.yaml))
repo       no org  (fallback identity)
machine    mch_fac0df3a
agent      none  (no coding agent detected)
project    prj_c0f3…b5b8
remotes    github.com/someone/tool
links      known  0 link(s) for this repo (stores of 2 entries scanned)
personal   me -> ~/.lore/personal
org resolution (first rule that answers wins):
  ✗ 1. LORE_ORG         LORE_ORG is not set
  ✗ 2. lore org use     no `lore org use` choice for prj_c0f3…b5b8
  ✗ 3. .lore/org.yaml   no .lore/org.yaml
  ✗ 4. store link       no store links this project id at these remotes (links known)
  ✗ 5. repo pattern     no entry's own: or repos: pattern matches github.com/someone/tool
  ✓ 6. nothing matched  no rule matched: fallback identity, personal everywhere/ notes only
```
<!-- /example -->

Your **identity** in an org (`identity.key`, a name and a `handle`) is what lore writes as the author of rows in that org's repos. The org's **roster** is the list of people and agents in the org: the actors in the store's org folder, each with a stable id. `lore actor register` puts you on it. Then `@handle` works in `--assign @alice` on every machine. A coding agent is its own actor, owned by a person, and started with `lore agent start`.

The **repo role** decides which guards apply. A leak guard is a check that stops internal notes from reaching the client's repo.

| Role | When | Leak guards |
|---|---|---|
| **client repo** | matched by `repos:` or linked without `--own` | on: internal kinds default to the store, commits are checked |
| **own repo** | matched by `own:`, or linked with `--own` | off |
| **no org** | nothing matched | off; only your `everywhere/` personal notes load |

## Who sees what: the layers

```text
  narrowest                                                       widest
  ┌──────────┐   ┌─────────────────┐   ┌──────────────┐   ┌────────────────┐
  │ me       │   │ <org>/<folder>  │   │ <org>/org    │   │ repo           │
  │ personal │──▶│ internal project│──▶│ whole company│──▶│ client can read│
  │ store    │   │ (client/)       │   │ (global)     │   │                │
  └──────────┘   └─────────────────┘   └──────────────┘   └────────────────┘
   --to me        --to agency           --to agency/org    --to repo
```

`--to` picks **who** sees a row. `--live` picks **when**. The two are independent. In a client repo, a row written without `--to` goes to the org's default for its kind. By default, tasks and memories go to the linked internal project, and rules and decisions need a person at a terminal or an explicit `--to`. So forgetting a flag puts a note in the safe place. Moving a row to a wider audience is always one deliberate step: `lore move <id> --to <target>` shows the text, asks, and moves the row with the same id (a row has one home, so there is never a second copy). A pointer (a row that names another row's id) never goes from a wider layer to a narrower one, because the pointer alone would show that the hidden row exists.

## The fixed rules in plain words

1. `main`'s `.lore/data` is the lasting home of every row. Everything else (live rows, signals, caches, rendered files) is a view or a temporary copy.
2. Any row can be live. The kind of row only sets the default.
3. A code repo has one live ref, `refs/lore/live`; a store has one per store branch, `refs/lore/live/<branch>`. Projects are folders inside it.
4. If a row id is in live, live wins. Always.
5. Lookup goes through every loaded source in order (this repo, the linked store project, the org's global project, your personal store), live before branch data; the first hit wins. An id found in two sources is refused until you name one with `--from`.
6. A row lands when it is finished, when someone runs `lore land`, or as a copy at its max age (`live.max_age`, 7 days by default). It leaves live only when it is finished and `main` holds the same content.
7. Landing is automatic: a direct push, or a ride-along commit when `main` is protected.
8. ZIP downloads, forks, plain mirrors and shallow clones do not carry `refs/lore/*`. That is why live is temporary and open rows are copied to `main` at their max age (7 days).
9. A store is a separate lore-only repo with a `lore-store.yaml` marker; each store branch is a separate lore, and lore never moves rows between store branches.
10. Signals are not rows: they never land and never build history.
11. Coordination writes (claim, renew, release, done on a claimed task, gate approval, resource holds) succeed only when the remote confirmed them. A gate is a step that waits for a person to say yes. Offline, these writes fail. They are never queued.
12. One org per repo. Orgs never mix.

The one rule behind all of them: **hidden is not private**. Anyone who can clone a repo can download every ref in it. Privacy is decided only by which repo a row is stored in.

## Pitfalls

- **"Finished" is per kind.** A task is finished when it is done or cancelled, a decision when it is superseded or rejected; rules, memories and notes count as finished as soon as they are written, so each write also lands a copy on `main`.
- **The fetch cache.** Lore fetches once per command and reuses that fetch for 60 seconds; `lore sync` always fetches.
- **Gates need a person.** Any person on the roster may approve a gate; a mission's `approvers:` list can narrow that. An agent never can.
- **Teams stay internal.** In a linked client repo, assigning a whole team is refused; assign a person.
