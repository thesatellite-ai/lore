# Concepts

**In short:** this page explains the few words lore uses, one at a time. A row is one note or task. Live rows are shared with everyone on any branch, before any merge, as soon as you run `lore sync` or your next `git push`. A store is a separate private repo for notes. An org is your company's setup. Your identity is your email.

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
  "id": "tsk_2ad3…f891",
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

**Live rows** are rows written to a hidden list on GitHub that is not a branch: the hidden ref `refs/lore/live`. Git's checkout never touches that ref, so switching branches does not hide them. Lore fetches it by itself; it pushes it on `lore sync` or your next `git push` (or after every write with `live.autopush: true`). If a row id exists in live, **the live copy wins**, always. Tasks, missions, handoffs and comments are live by default. Any other row is live when you pass `--live`. `--no-live` keeps a row with the code.

**Signals** are short-lived facts: "`agent-backend` holds this task until 10:20", a heartbeat (an agent's "I am still here"), a hold on the shared test suite. They sit on `refs/lore/signals`, which is replaced in place. It is always exactly one commit, so a thousand heartbeats leave no history. A signal carries an expiry time. An expired signal counts as absent.

The office picture: branches are rooms, each with its own **filing cabinet** (`.lore/data`); `main`'s cabinet is the **archive**. The live ref is a **whiteboard in the hallway** that everyone sees from any room. Signals are **sticky notes by the door**, replaced, never filed.

A monorepo (one repo that holds several projects) has one live ref. Each lore project is a folder in it: `_root/` for the project at the repo root, `web/` for the one in `web/`. A real listing:

<!-- example: internals#monorepo-live-tree only=2,3 -->
```console
# carol, in app
$ git ls-tree -r --name-only refs/lore/live
_live/epoch
_live/meta.json
_root/task_lists/tlt_7a67…9504.json
_root/tasks/tsk_2ad3…f891.json
$ git log --oneline refs/lore/live
601d285 lore: live 2 rows (_root)
```
<!-- /example -->

## Landing

**Landing** writes a live row into `main`'s `.lore/data`, its lasting home. Then, once lore sees that `main` holds the same content, it removes the row from live. Lore calls this **leaves live**.

```text
 lore task add --title "…"            lore task done --id tsk_…          next lore command
        │                            │                             │
        ▼                            ▼                             ▼
  refs/lore/live: tsk  ──▶  commit "lore: land 2 rows" ──▶  main holds the same file:
  (everyone sees it)        pushed to main                   tsk leaves live
```

A row lands when:

- it is **finished**: a task done or cancelled, a decision superseded or rejected (lore stores "rejected" as the status `deprecated`). Rules, memories, notes and comments count as finished on every write, so the next `lore sync` after each write lands a copy (the row may stay live while people keep editing it);
- someone runs **`lore land`** (one id, `--all`, `--kind`, `--mission`; `--dry-run` shows the plan);
- it is still open after **`live.max_age`** (7 days by default): a copy lands and the row stays live.

Landing needs no person and no CI. Lore builds the commit on a fresh copy of `origin/main` and pushes it directly. Your local `main` and your working folder are never touched. If `main` is **protected** and refuses direct pushes, the rows **ride along** on your next push. Lore's `post-commit` hook (a small script git runs after each commit) adds a separate commit `lore: land N rows` to your branch. It reaches `main` with your pull request.

## Stores and links

A **store** is a separate private repo that only your company can open, like a locked filing cabinet next to the client's repo. It holds only lore projects, one folder per project, plus a `lore-store.yaml` marker and a `README.md` at its root. Inside a store a folder's rows sit right under it (`client/tasks/tsk_….json`); there is no `.lore/data` in a store, and no folder may be named like a kind of row (`tasks`, `rules`, …). It is a notebook, never a code repo: lore refuses to use a repo that holds code as a store.

```text
 github.com/agency/notes            (only the agency can read it)
   lore-store.yaml                    marker
   README.md                          what the folders are, how to edit by hand
   org/                               company rules, roster (the org folder)
     lore-org.yaml                      the org's suggested settings
     actors/  rules/ …                  one folder per kind of row
   client/                            internal project for the client
     _meta.json                         the folder's id and repo links
     tasks/tsk_….json  memories/ …      its rows
   client2/                           internal project for client 2
   refs/lore/live/main                live rows of every folder
   refs/lore/signals/main             claims and leases
```

Each folder in a store is a full lore project. It has branch data (on the store's `main`), live rows and signals, and the same landing rules. Lore keeps one clone of each store, a normal git clone at `~/.lore/stores/<owner>/<repo>/` that you can open and browse; lore's own lock and stamps sit in its `.git/lore/`. See [Troubleshooting](troubleshooting.md#where-a-stores-clone-lives).

A **link** ties a client repo to its internal project: "project `prj_…` at `github.com/client/website` belongs to `client/`". Someone at the agency makes it once with `lore org link --org agency --project client`. The link is saved **only in the store** (in `client/_meta.json`). So nothing is written to the client's repo, and everyone at the agency finds the link on their next command. Several repos can link to one project. That is a **group**: three client 2 repos, one `client2/` project.

## Branch layers: notes that follow the code branch

In a repo whose notes live in a store folder, a code branch other than the default branch keeps its own notes: a **branch layer**. Everything lore writes there for the linked project (tasks, memories, rules, decisions, comments) goes into that branch's layer, not into the folder's shared notes. People and agents on the branch see the shared notes with the layer on top; people on `main` or another branch see none of it. When the branch merges, `lore branch land` squashes the layer into the shared notes as one commit; a branch deleted without merging leaves its layer to be dropped into the trash.

```text
 store repo (github.com/agency/notes)
   main                                         the shared notes (client/…)
   refs/lore/layer/main/client/<repo>/feat%2Fx  feat/x's own notes: a hidden ref, no branch on GitHub
   refs/lore/layer-trash/…@<stamp>              landed or dropped notes, kept until purged
```

```console
$ git switch -c feat/x
$ lore task add --title "try the new cache"
lore: wrote tasks/tsk_2ad3…f891 to the feat/x notes of client (store agency) (not shared with the default branch; lore branch land when the branch merges)
$ lore sync                                   # teammates on feat/x get it; main does not
$ lore branch list
BRANCH    REPO                   ROWS     AGE   STATE
feat/x    github.com/client/app  1 task   2d    merged
feat/x merged into main: land its notes with lore branch land --branch feat/x
$ lore branch land --branch feat/x --pr 123
✓ landed 1 task of feat/x into client: commit 1a2b3c4 on main (pushed); the notes are in the trash (lore branch restore --branch feat/x)
```

- **Tasks follow the branch too.** Tasks, assignments, claims, holds, notes and done, missions, handoffs and comments isolate like every other row and land with the merge: a task given on a feature branch reaches its assignee only when that branch lands, and a done marked on a feature branch reaches `main` with it. An edit made on an isolated branch to a row of the shared notes goes into the layer as well. So give a teammate a task from the default branch (or with `--no-layer`), and mark a work branch that several people or agents coordinate through, such as a mission's agent branches, with `lore branch share --branch <b>`: its claims, notes and done then reach the shared notes at once.
- **Which branches.** The default branch always writes to the shared notes, and so do `dev`, `staging` and `release/*` (policy `branches.main_like`). Every other branch is isolated, whatever its name. `lore branch mode` says which rule decided; `lore branch isolate --branch <b>` or `lore branch share --branch <b>` makes an exception for the whole team. The choice is read before anything else in a command, so a shared branch reads the same cache as `main` and an isolated one a cache of its own; a change of choice (yours, or a teammate's after a sync) takes effect on the next command.
- **Landing.** One commit with every row's own author and times; a row both sides changed is merged field by field, and the same text changed on both sides stops the landing (`E_LAND_CONFLICT`, naming the row, the field and the `--take` commands) and changes nothing; `--dry-run` says so first. A normal merge is detected and offered: a branch is merged only when every copy of it (yours, origin's) is in the default branch and the commits its notes were written on are there too, so a branch reset to `main`'s tip is not merged; a squash merge is landed by the CI step (`lore branch land --store <url> --folder <folder> --repo <repo> --branch <b>`) or by hand.
- **Abandoned branches.** A branch deleted without merging shows as orphaned; `lore sync` moves its notes to the trash after 14 days (`branches.orphan_days`), `lore branch restore` brings them back (and `lore sync` then keeps them until the branch writes again or someone drops them), and only `lore branch purge --confirm` removes them for good. Only a machine that wrote a branch's notes judges it deleted: on a teammate's machine a branch that was never pushed stays open, since it may live on its author's laptop.
- **Two people on one branch.** Their notes merge on `lore sync` field by field; when both changed the same text of a row, the newer edit wins and the sync line names the value it replaced, as on the shared notes.
- **Now, not later.** `--no-layer` on every add and edit (`task`, `rule`, `memory`, `decision`, `comment`, `mission`, `plan`, …) writes to the shared notes on a branch; `LORE_LAYER=off` (also `0` or `false`, in any case) does it for every command of one shell, and any other value is refused; `lore branch promote --ids <id>` shares a row already written.
- **Older lore.** The first branch notes of a folder mark it as needing lore 0.1.18, the release that ships branch layers (`_meta.json`), so an older lore reads it but refuses to write there (`E_OLD_LORE`) instead of writing a branch's note into the shared notes.
- **Landing options.** `--kinds tasks,memories` lands only those tables and keeps the rest with the branch; `--review` asks keep or drop for every row (a terminal only); `--take branch --ids <id>` (or `--take main`) settles a row whose text both sides changed: only the clashing fields take that side, a field only the other side changed keeps its change. `lore branch restore --branch <b> --revert` also reverts a landing with a new commit.
- **Stacked branches.** A branch made from another branch that has notes (`feat/b` from `feat/a`) reads `feat/a`'s notes under its own, also before it writes a note of its own. `lore branch land --branch feat/b --into feat/a` moves its notes into `feat/a`'s; when `feat/a` lands, `feat/b`'s notes move onto the shared notes.
- **Older notes.** `lore branch adopt --branch <b>` moves rows written on a branch into the shared notes before branch layers existed (their origin names the branch) into that branch's notes.
- **Find and trash by the branch a note was born on.** Every row remembers the code branch it was created on (`lore show --id <id> --origin`), and a land, a stacked land `--into`, an adopt or a `restore --revert` keeps it. So every list and `lore search` take `--born-branch <b>` (also the REST list routes, as `born_branch`), on the branch and long after it landed. `lore branch show --branch <b>` counts the rows of the shared notes born on it (landed or adopted), also when the branch has no open notes any more, and names the list and trash commands. `lore trash add --born-branch <b> [--kind task] --dry-run` prints the count per kind and the rows; without `--dry-run` it asks first (`E_NEEDS_CONFIRM`, answer `--yes`) and moves them as one batch, each restorable with `lore trash restore --id <id>` (or the whole batch with `--id <bat_…>`). A row born on the branch whose delete would take rows made elsewhere (a task list made on the branch that `main`'s tasks now use) stays and is named. Only notes are swept, never the org's setup (actors, repos, policies, tags). In a repo's own notes a row keeps the branch name only when the branch has an upstream (a local-only branch name is never written into a code repo), so such rows are not found by it.
- **CI.** In a repo of your own, `lore branch list` and `lore doctor` offer `lore branch install-action`, which writes a GitHub workflow that runs the land step when a pull request merges (it asks first; never in a client's repo, where the file would name the internal store).
- **A repo whose notes live in its own `.lore/data`.** Rules and memories already follow the branch with git. On an isolated branch, new tasks, missions, handoffs and comments do too: they are written with the branch's files instead of going live, and reach `main` when the branch merges. `main`, `dev`, `staging` and `release/*` keep live rows as before; `--live`, `--no-layer` or `LORE_LAYER=off` share one row or one shell at once.

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
org        none  (matched by: nothing matched)   hint: lore org use --entry <entry> (agency, client3)
identity   Alice Doe <human:alice@example.com>   act_05e9…4603  (from: top-level identity (config.yaml))
repo       no org  (fallback identity)
machine    mch_98c3dece
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
| **client repo** | matched by `repos:`, or linked without `--own` when no `own:` pattern matches it | on: internal kinds default to the store, commits are checked |
| **moved own repo** | no `own:` pattern matches its remote any more, nothing links it as a client, and its lore data was written as an own repo (actor rows lore keeps out of client repos) | off: it stays an own repo; `lore whoami` names the move and both fixes (an admin adds the new owner under `own:`, or `lore org link --org <org> --project <folder> --yes` makes it a client repo) |
| **own repo** | matched by `own:`, or linked with `--own` (a repo `own:` matches links as own without it) | off |
| **no org** | nothing matched | off; only your `everywhere/` personal notes load |

## People: profiles and stints

Each person on the roster can carry a **profile**: a title, a timezone, contacts (several emails, GitHub accounts, a phone, …), **company periods** (one per stint at the company, with its dates and company role) and **project memberships** (which projects they work on, since when, in which **roles**). A person has a **status**: `active`, `on_leave`, `left` or `archived`; left and archived people drop out of `--assign` and pickers, but their old rows keep their name. `lore actor offboard` and `lore actor rejoin` move someone out and back in, and the actor id never changes.

Contacts marked as **identity contacts** tie every email and GitHub login of one person to one actor, so a second laptop with a personal git email never makes a second person. All of this lives only in the org's roster store; a client repo still sees a person as `{id, display_name, kind}`. Team profiles are in development for lore 0.1.18 and not released yet. See [Team profiles](profiles.md).

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

`--to` picks **who** sees a row. `--live` picks **when**. The two are independent. In a client repo, a row written without `--to` goes to the org's default for its kind. By default, tasks and memories go to the linked internal project, and rules and decisions need a person at a terminal or an explicit `--to`. So forgetting a flag puts a note in the safe place. Moving a row to a wider audience is always one deliberate step: `lore move --id <id> --to <target>` shows the text, asks, and moves the row with the same id (a row has one home, so there is never a second copy). A pointer (a row that names another row's id) never goes from a wider layer to a narrower one, because the pointer alone would show that the hidden row exists.

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
13. A write on an isolated code branch stays with that branch: in a store folder its layer, in a repo's own notes the branch's files (new tasks too). It reaches the shared notes only by landing (`lore branch land`, or the branch's merge for a repo's own notes) or `lore branch promote`.

The one rule behind all of them: **hidden is not private**. Anyone who can clone a repo can download every ref in it. Privacy is decided only by which repo a row is stored in.

## Pitfalls

- **"Finished" is per kind.** A task is finished when it is done or cancelled, a decision when it is superseded or rejected; rules, memories and notes count as finished as soon as they are written, so each write also lands a copy on `main`.
- **The fetch cache.** Lore fetches once per command; a read reuses that fetch for 5 minutes, a write for 60 seconds; `lore sync` always fetches.
- **Gates need a person.** Any person on the roster may approve a gate; a mission's `approvers:` list can narrow that. An agent never can.
- **Teams stay internal.** In a linked client repo, assigning a whole team is refused; assign a person.

## Related

- [Overview](overview.md): the whole picture in one diagram.
- [How it works](how-it-works.md): the same ideas, as git operations.
- [Getting started](getting-started.md): the concepts in a first run.
- [Commands reference](commands.md).
- [Team profiles](profiles.md): people, contacts, stints and project roles in detail.
