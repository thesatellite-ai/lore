# How it works

**In short:** this page shows what lore does in git behind each command. You see where a note is written, how a teammate gets it, and how it ends up saved on `main`. You never run these git steps yourself. Read this page when you want to know why lore behaves the way it does.

Each section has a diagram and a short explanation. The [Concepts](concepts.md) page explains the words. A few words come up on every section:

- A **row** is one saved item: a task, rule, decision, memory or note. Each row is one small JSON file.
- A **hidden list** is a place on GitHub that is not a branch; git calls it a ref. Lore keeps two: `refs/lore/live` and `refs/lore/signals`.
- **Live** is the hidden list every teammate sees at once, whatever branch they are on. A row on live is shared now, not when a branch merges.
- **Landing** is the step that copies a finished row from live into `main`, so it is saved with the code.

Contents: [writing a row](#writing-a-row-where-it-goes) · [lookup](#lookup-which-copy-of-a-row-wins) · [fetch](#fetch-the-tracking-ref-and-the-5-minute-cache) · [writing to live](#writing-to-live) · [landing](#landing) · [protected main](#protected-main-the-ride-along-commit) · [merging a row](#merging-two-versions-of-a-row) · [compaction](#compaction-and-the-epoch) · [signals and claims](#signals-claim-renew-expire-reclaim) · [done and merged](#done-then-merged) · [org resolution](#org-resolution) · [links](#lore-org-link-and-the-link-scan) · [store checks](#store-checks) · [leak guards](#leak-guards) · [render](#rendering-context-outside-the-checkout)

## Writing a row: where it goes

Two separate choices decide where a new row is written. `--to` says **who** may read it. `--live` says **when** others see it.

```text
                      without --live                 with --live (or a live kind)
 no --to              this branch's .lore/data        this repo's refs/lore/live
 (own repo / no org)  (reaches main with the branch)  (everyone, now; lands when finished)

 no --to              the org's default for the kind: tasks and memories → the linked
 (client repo)        internal project in the store; rules and decisions → as configured,
                      and an agent (or a script without a terminal) must pass --to

 --to repo            this repo, even a client repo (a deliberate choice)
 --to agency       the linked internal project ─┐ in the store: committed and pushed
 --to agency/org   that folder of the store    ─┤ at once (no code branches there),
 --to me              your personal store         ─┘ or to the store's live ref
```

Tasks, missions, handoffs and comments are live by default (`live.defaults` changes this per kind). Rules, decisions and memories travel with the code unless you pass `--live`. A **store** is a separate private repo that only the agency can open, like a locked filing cabinet next to the client's repo. In the store, "branch data" means the store branch itself: a write without `--live` is a commit on the store's `main`, sent with your next `lore sync` (at once only when the store is set to `push=now`). The output always says where a row went (`filed: … → the linked project client/`).

Sometimes lore cannot tell whether the repo is a client repo: the store cannot be read and there is no saved list of links. Then a write without `--to` is refused with `E_LINK_UNKNOWN`. Lore does not guess.

In a client repo, `lore sync` and reads write nothing into the checkout: the repo gets no repo id in `.lore/data/_meta.json` and no repos row file (they would carry the client's remote into the client's history), and a committed `.lore/LORE.md` is not rendered again behind your back. Your private context goes to `~/.lore/render`, imported through the ignored `CLAUDE.local.md`; `lore render` still writes `.lore/LORE.md` when you ask for it.

In your own repo, reads never write into `.lore/data`, and neither does a `lore sync` while `.lore/data` has nothing waiting for your commit: a repo without a repo id yet (one upgraded from lore 0.1.17) gets it, with its repos row and your actor row, from the first sync that finds rows of yours waiting for a commit, or from your first `--scope repo` or `--scope dir:<path>` write (the id is derived from the project and the remote, so every clone that writes it writes the same one), and that commit carries them. So a teammate committing the repo id never makes `git pull` in your clone stop over copies lore left there.

## Lookup: which copy of a row wins

A command that takes an id (`lore task done --id tsk_…`) has to find exactly one copy. A **tombstone** below is a marker on live that says "this row was deleted".

```text
 for each loaded source, in order:            in each source:
   1. this repo                                  a. its live folder  ──▶ hit? stop
   2. the linked store project(s)                b. its branch data  ──▶ hit? stop
   3. the org's global project                   (a tombstone in live = "deleted", stop)
   4. your personal store (org folder, then everywhere/)

 found in two sources  → E_AMBIGUOUS_ID, name one with --from
 found nowhere, on main → read origin/main too (a row that landed while you were not pulled;
                          never a file a commit of yours removed, such as store adopt --move)
 found nowhere          → E_NOT_FOUND with a `lore sync peek` hint
```

Live always wins over branch data. So you see a teammate's edit on live, whatever your branch holds. `lore explain --id <id>` prints every copy and which one won:

<!-- example: small-team#explain -->
```console
# alice, in billing
$ lore explain --id tsk_54fe…28f2
tsk_54fe…28f2 in ~/work/billing (folder _root)
  fetched 35s ago
  live (local 6d00426): row, live since 2026-11-02T10:00:25Z, hlc 2026-11-02T10:00:55.000Z/0000/act_7a67…9504
  this branch (HEAD): not present
  origin/main: present
  wins: the live copy (rule 4: if a row id is in live, live wins)
  land: leaves live on the next sync (finished, main holds it)
```
<!-- /example -->

## Fetch: the tracking ref and the 5-minute cache

On the first lore command in a clone, lore adds one fetch rule to the repo's git config:

<!-- example: internals#refspec -->
```console
# alice, in website
$ git config --get-all remote.origin.fetch
+refs/heads/*:refs/remotes/origin/*
+refs/lore/*:refs/lore-remote/origin/*
```
<!-- /example -->

```text
   remote                      your clone
 refs/lore/live     ──fetch──▶ refs/lore-remote/origin/live   (tracking copy: what the remote has)
                                         │ merge (never overwrite)
                                         ▼
                               refs/lore/live                   (your copy: may hold unpushed writes)
```

The remote's hidden lists arrive in **tracking copies** under `refs/lore-remote/`. A tracking copy is your clone's note of what the remote holds. Lore then merges it into your own `refs/lore/live`. It never overwrites your copy, so a live write you made offline is kept until it can be pushed.

Each lore command fetches once: one `git fetch` gets live, signals and `main` together. A read skips the fetch if the last one is less than `live.fetch_ttl` (5 minutes) old, a write if it is less than 60 seconds old (a write's refusals, such as a teammate's claim or gate, need the remote's rows). `lore sync` always fetches, once per remote, and pushes once per remote. So a teammate's change shows up within a minute, or at once after `lore sync`:

Commands in one clone take turns on its live list: each waits for the one before it to finish, in the order they started (a command that waited longer always goes first), and gives up with `E_STORE_LOCKED` after 30 seconds. Ten agent sessions working in one clone at the same time each wait for about the other nine commands, a few seconds; agents that each have their own clone or worktree of the repo do not wait for each other at all.

<!-- example: small-team#assign-across-branches only=2,3 -->
```console
# bob, in billing
$ lore sync && git switch -q -c feat/payments
lore live: pushed waiting live writes commit dfd8dc8 to origin refs/lore/live
lore live: 1 row(s) left live (main holds them)

# alice, in billing
$ git switch -q -c feat/login && lore task list --mine
(no tasks)
```
<!-- /example -->

Offline, reads use the last fetched state, and a search or ask that read a store that way names it under its answer (`<store>: unreachable, results from the copy of <date>`; `--json`: `unreachable`). A **hook** is a small script git runs at a set moment. Git's `post-checkout` and `post-merge` hooks also start a fetch in the background, so switching branches refreshes live rows.

## Writing to live

```text
 1. fetch and merge refs/lore/live (above)
 2. build the new tree with a private index: old live tree + the row file
 3. same tree as before? stop (writing the same thing twice is a no-op)
 4. commit "lore: live N rows (<folder>)" on top of the live ref
 5. move refs/lore/live only if nobody moved it meanwhile (compare-and-swap)
 6. push it (only with live.autopush: true; else on lore sync or your git push);
    rejected because a teammate pushed first? back to 1, merge, retry
```

Step 2 uses a **private index**: a scratch staging area of lore's own, so your `git add` work is not touched. Step 5 is a **compare-and-swap**: lore moves the list only if it still points where lore last saw it, like saving a document only if nobody saved over it meanwhile.

Your branch, your staged files and your working folder are never touched. Lore builds commits with git's low-level commands, outside any checkout. The one exception is a row a live write names that only your working folder holds (a plan you added and have not committed, then `lore mission add --plan`): it moves to the live list with that write, so teammates can read it, and lore says so in one line (`lore: moved plans/pln_… to this clone's refs/lore/live with the live row that names it, so teammates can read it; <file> is no longer in your working folder (it lands with that row)`); a table folder the move leaves empty is removed. Lore's own upkeep of your actor row (its origin stamp, a name that follows its source) rides the live list with the write too, so the committed actor file is not rewritten. By default a live write is not pushed by itself: `live.autopush` is off, so lore keeps the write here, says so once per repo (`lore live: (live, kept here: …)`), and sends it on your next `lore sync` or `git push` (the pre-push hook pushes the live list with it). With `live.autopush: true` each write is pushed at once, and lore says once per repo that `lore config set --key live.autopush --value false` turns that off. Sometimes the push cannot happen: you are offline, `LORE_LIVE_AUTOPUSH=0` is set, the host refuses `refs/lore/*`, or teammates' pushes won every retry (`lease.push_retries`). Then the write stays on your local live list, the command still succeeds, and lore says `(live, not pushed yet: N waiting; …)` with why and what happens next, once: not again on every read or git hook until the count or the reason changes. Your next live write or `lore sync` pushes it; a read never pushes, so after a lost race the line says ``lost N races to teammates' pushes: your next live write or `lore sync` sends them``. When the remote is not there at all (a mistyped or deleted `origin`, a path with no repository) or refuses your credentials, or is a local folder you may not read (it then says `cannot read <path> (permission denied)` and that the address is right), the line says so and names the remote instead of "offline", `lore sync` gives the same reason, and `lore doctor` warns until a push goes through. `lore live diff` shows what is waiting:

<!-- example: errors#refs-refused-by-the-host only=1,2 -->
```console
# bob, in portal
$ lore task add --title 'set up the portal CI'
lore: wrote task_lists/tlt_54fe…28f2 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/portal/.lore/data/task_lists/tlt_54fe…28f2.json is unchanged)
lore: wrote tasks/tsk_05e9…4603 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/portal/.lore/data/tasks/tsk_05e9…4603.json is unchanged)
✓ tsk_05e9…4603 — set up the portal CI [medium]
  created tasklist tlt_54fe…28f2 "default" for tasks added without --tasklist
lore live: (live, kept here: `lore sync` or your next `git push` sends live writes; `lore config set --key live.autopush --value true` sends each write at once)
$ lore live diff
local     86dbae476bd6
tracking  (none)
seen      (none)
pending   2 write(s) not confirmed by the remote
  _root/task_lists/tlt_54fe…28f2.json
  _root/tasks/tsk_05e9…4603.json
```
<!-- /example -->

## Landing

```text
          ShouldLand(row)
   ┌────────────────────────────────────────────────────────┐
   │ finished (task done/cancelled, decision superseded/    │──▶ land
   │ rejected; rules, memories, notes, comments: every write)│
   │ named by `lore land`                                   │──▶ land (open rows: copy)
   │ open and older than live.max_age (7d), differs from main│──▶ copy, stays live
   │ otherwise                                              │──▶ stay, with the reason
   └────────────────────────────────────────────────────────┘

 land:  fetch origin/main ─▶ private index on origin/main's tree ─▶ write the row files
        (without their _live block) ─▶ commit "lore: land N rows (<folder>)" ─▶ push to main
          accepted              → done; local main is never read or moved
          main moved meanwhile  → start again on the new origin/main
          main is protected     → ride along (next section)
          no main on the remote → nothing to land yet (a brand-new repo): quiet, rows stay live

 when:  lore sync · lore land · your own git push of main (pre-push adds the landing
        commit to your main and asks you to push again); never by itself after a write

 leave live:  next fetch sees main's file == live file, and the row is finished
              → remove it from live (only if the live copy did not change meanwhile)
```

Landing never moves the remote `main` behind your back: a write records the row on the live list only, and the landing runs when you run `lore sync` or `lore land`, or folds into your own `git push` of `main` (the pre-push hook commits the finished rows onto your `main` as `lore: land N rows`, stops that push and asks you to run `git push` again, so the push carries them). In `lore sync` the landing commit and your waiting live writes go to the remote in one push (`git push --atomic`), so a slow remote is reached once; a host without atomic pushes, or one that refuses either ref, gets the live list first on its own, and nothing is called landed unless `main` took the commit. Landing twice writes the same file and commits nothing. A row reopened after it landed differs from `main`, so it stays live and lands again later. A deleted row lands as a file delete plus an entry in `.lore/data/_purged.json`, so an old branch copy cannot bring it back. For a store project the same steps run on lore's own clone of the store (a plain git clone under `~/.lore/stores/<owner>/<repo>`) and the store branch. A store branch must accept direct pushes (`lore store doctor` checks). Your personal store and an org store may push in batches instead of at once, and their old history may be folded into one commit: see [How writes reach the remote](identity-and-stores.md#how-writes-reach-the-remote).

**On a feature branch.** Between "landed on main" and your next pull, lore reads a landed row from `origin/main` only while this checkout is on the default branch (the "found nowhere, on main" step above). On any other branch, a row that landed and left live drops out of your lists and `lore show` until you merge main into the branch. The sync that lands it says so on stderr:

```text
lore live: landed on main (1a2b3c4); leaves live once main is fetched
lore live: landed on main; this checkout is on feature, so those rows show here after you merge main
```

Run `git merge origin/main` (or `git pull origin main`) on the branch and the rows show again. Nothing is lost: the rows are on `main`, and teammates on `main` see them at once.

`lore land --dry-run` prints the plan for every row with its reason. `lore explain --id <id>` gives the decision for one row (`land: waits: main is protected; rides on your next push`).

## Protected main: the ride-along commit

A **protected branch** is one GitHub only lets you change through a pull request. When the remote refuses a direct push to `main` for this reason, the finished rows wait. Your next commit on any branch other than `main` carries them:

```text
 lore task done --id tsk_…        → "main is protected: the rows ride on your next push"
 git commit -m "fix login"   → post-commit hook adds a second commit:
                                  c95f118 fix login timeout
                                  8390546 lore: land 1 row (_root)     ← only the row files
 git push                    → the pull request carries both; merging it lands the row
```

<!-- example: small-team#protected-main only=2,4,5 -->
```console
# bob, in billing
$ lore sync
lore live: pushed waiting live writes commit 2fc021b to origin refs/lore/live
lore live: 2 row(s) left live (main holds them)
lore live: landed on main; this checkout is on feat/payments, so those rows show here after you merge main

# alice, in billing
$ lore sync
lore live: pushed waiting live writes commit 51886a5 to origin refs/lore/live
lore live: main is protected: the rows ride on your next push (lore: land N rows)
$ lore explain --id tsk_c0f3…b5b8
tsk_c0f3…b5b8 in ~/work/billing (folder _root)
  fetched 5s ago
  live (local 51886a5): row, live since 2026-11-02T10:01:40Z, hlc 2026-11-02T10:01:50.000Z/0000/act_7a67…9504
  this branch (HEAD): not present
  origin/main: not present
  wins: the live copy (rule 4: if a row id is in live, live wins)
  land: waits: main is protected; rides on your next push
```
<!-- /example -->

The ride-along commit is built with a scratch staging area, so your staged changes are untouched. If one of the row files has uncommitted edits, lore skips it and says why. The `pre-push` hook only checks (in a client repo it also runs the leak check, see [Leak guards](#leak-guards)). If the ride-along commit is missing (after an amend or reset) or older than live, it adds a fresh one and asks you to push again. `git push --no-verify` skips the check (and the leak check), and the rows simply ride on a later push. A pull request closed without merging lands nothing; the rows stay live and ride again.

## Merging two versions of a row

Two people can edit the same live row at the same time. A branch can also merge into `main` with a row someone also edited on live. Lore's **merge driver** settles this: a small helper git calls to combine two edits of the same file. It works one file at a time:

```text
 file level:   added on one side → take it        deleted on one side, untouched on the other → deleted
               same on both      → take it        deleted on one side, EDITED on the other    → edit wins, row kept
 field level:  status, priority, dates, numbers → the newer edit (by hybrid logical clock)
               one-value text (done_by, done_commit, work_branch, approved_by, role)
                                                → the newer edit, never conflict markers
               text fields, live row (refs/lore/live)
                                                → the newer edit, never conflict markers;
                                                  the sync names the text it replaced
               text fields, git merge of a row file
                                                → both edits kept, with conflict markers
               sets (labels, links)             → union
```

The order of edits comes from a **hybrid logical clock** (HLC, `hlc` in the live file): a timestamp that also counts edits, so two edits made in the same second still have an order. It holds the wall time, a counter and the writer, so a laptop with a wrong clock does not win by accident. A live row never gets conflict markers: the live list is read by every clone, so markers there would reach everyone and stop the sync of people who never touched the row. A row file with conflict markers (from a git merge, or a live row an older lore merged) stays visible and is never landed. `lore sync conflicts` lists it until `lore sync resolve` settles it.

## Compaction and the epoch

Every live write is a commit, so the live list's history keeps growing. **Compaction** replaces that history with one commit holding the same rows, like rewriting a long notebook as one clean page. It also raises a counter, the **epoch**, in the list's `_live/epoch` file. The epoch tells other clones "the history was rewritten":

<!-- example: leak-cleanup#purge only=2,3,4 -->
```console
# alice, in website
$ lore live compact --dry-run
refs/lore/live has 2 commits (threshold 1000); compacting would keep 5 files, epoch 1 → 2, on this machine only (the remote origin has no live ref yet; `lore sync run` sends the compacted ref with the live writes waiting here)
$ lore live compact --yes
✓ compacted refs/lore/live to 1 commit (epoch 2) on this machine only; the remote origin has no live ref yet, and `lore sync run` sends it with the live writes waiting here; backup kept locally as refs/lore-backup/live/20261102T100025
$ lore sync >/dev/null; lore live log
lore live: pushed waiting live writes commit 601d285 to origin refs/lore/live
lore: pushed store agency/client: commit 86dbae4 to branch main (1 commit)
epoch 2
601d285 2026-11-02 alice  lore: live compact (epoch 2)
```
<!-- /example -->

```text
 before:  A─B─C─…─(1,214 commits)        epoch 1
 after:   Z (same files, no parent)       epoch 2      backup: refs/lore-backup/live/<stamp> (local only)

 a clone with unpushed live writes sees epoch 2 > 1:
   back up its live ref → take the remote's commit → re-apply only its own pending writes → push
```

"No parent" means the new commit starts a fresh history with nothing before it. A clone that sees a higher epoch backs up its live list, takes the remote's commit and re-applies only its own unpushed writes on top.

Compaction runs by itself past `live.compact_after` (1000) commits, at most once a day, and only in a run whose live writes go out (`live.autopush` on, or `lore sync`). It never touches `main` or any code branch. A compaction never pushes live writes that wait for `lore sync`: in the example above the remote had no live ref yet, so `lore live compact` stayed on Alice's machine and her `lore sync` sent one commit.

## Signals: claim, renew, expire, reclaim

```text
 every signals write:
   fetch refs/lore/signals ─▶ copy every unexpired file, apply the change
   ─▶ commit with NO parent ("lore: signals") ─▶ push --force-with-lease=<what I fetched>
        rejected (someone else wrote) → decide again from a fresh fetch (never merge)
        remote unreachable            → E_OFFLINE, exit 15 (never queued)
        lost the race on every retry  → E_CONTENDED, exit 11 (never queued)
```

Signals are short-lived facts, mostly claims. A **claim** is an agent putting its name on a task, like writing your name on a whiteboard job. A **lease** is how long the claim lasts: it runs out after a set time unless the agent keeps saying it is alive (a **heartbeat**), like a parking ticket.

Unlike live, the signals list keeps no history. Each write is a new commit with no parent, pushed with `--force-with-lease`: git replaces the remote list only if it still holds what lore fetched. If someone else wrote first, lore reads again and decides again; it never merges. So the list always holds one commit, and a heartbeat replaces it:

<!-- example: internals#signals only=2,3,4,5,6 -->
```console
# agent-backend, in website
$ lore task claim --id tsk_d431…313a --push
lore: committed locally to store agency/client; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/client/tasks/tsk_d431…313a.json (store agency/client)
✓ claimed tsk_d431…313a renew the client SSL certificate (lease 20m, token ct_a5f6f…) on the signals ref refs/lore/signals/main of store agency/client (pushed to the store's remote now: teammates see it on their next fetch)

# alice, in ~
$ cd ~/.lore/stores/agency/notes && git fetch -q origin '+refs/lore/*:refs/lore-remote/origin/*' && git ls-tree -r --name-only refs/lore-remote/origin/signals/main && git log --oneline refs/lore-remote/origin/signals/main
client/claims/tsk_d431…313a.json
9b40ce7 lore: signals
$ cd ~/.lore/stores/agency/notes && git cat-file -p refs/lore-remote/origin/signals/main:$(git ls-tree -r --name-only refs/lore-remote/origin/signals/main | head -1)
{
  "kind": "claim",
  "task": "tsk_d431…313a",
  "project": "client",
  "holder": "act_f147…5139",
  "holder_label": "agent:agent-backend",
  "session": "s-10e90368",
  "claim_token": "ct_a5f6f…",
  "claim_epoch": 1,
  "since": "2026-11-02T10:01:10Z",
  "expires": "2026-11-02T10:21:10Z",
  "renewed_seq": 0,
  "renewed_at": "2026-11-02T10:01:10Z",
  "host": "devbox"
}

# agent-backend, in website
$ lore task heartbeat --id tsk_d431…313a
✓ tsk_d431…313a renewed until 2026-11-02T10:21:25Z (seq 1)

# alice, in ~
$ cd ~/.lore/stores/agency/notes && git fetch -q origin '+refs/lore/*:refs/lore-remote/origin/*' && git log --oneline refs/lore-remote/origin/signals/main
05513aa lore: signals
```
<!-- /example -->

A claim on a code repo's own `refs/lore/signals` is pushed to that repo's remote only when you ask: `lore task claim`, `lore task next --claim` and `lore hold claim` take `--push` (or `LORE_PUSH_CLAIMS=1` in the shell or agent's environment, see [Environment](../reference/environment.md), or `live.autopush: true` in `config.yaml`, which pushes every write at once); without it they are refused before anything is written, with `E_REFUSED` and a hint that prints your own command again with `--push` added (the real task id or hold name and every flag you gave), which runs as printed; for a claim of one task the hint also names `lore task start --id` with that task's id, which starts the task without a claim. A claim cannot wait for `lore sync`: it is a lease two people race for, decided by the push. Renewing, releasing and finishing a claim you pushed push again without a new flag: `lore task heartbeat`, `lore task release` and `lore hold release` are the named exemptions to `--push` (their `--help` says so), because each touches only what your pushed claim or hold published, on the same remote; a claim never pushed is on no remote, so releasing it pushes nothing. A claim on a linked store's signals ref (the example above: a client repo's task, or any task of a repo linked to a store folder) follows the same rule: it is pushed to the store's remote only with `--push` (or the same standing go-ahead), is refused without it (as are `lore task revoke` and `lore task reassign` of a claimed task there), and never touches the client's remote. Its commit is signed with the store's identity (your identity in the org for an org store); an agent that claims for you is named in a `Lore-Agent` trailer, never as the author. `lore task release` takes the claim off the same ref and says where it went. A task that lives in an org store's folder needs no code repo to be claimed: `lore task claim --id <id> --push`, `lore task release --id <id>` and the other commands that name one task by `--id` also run from the home folder (or any folder outside a repo) and inside the store clone, on that store's signals ref, as your identity in the org, with the same `--push` rule. `lore task next --setup <setup>` from the home folder (or `--store <org>` / `--store <org>/<folder>` from any folder, or plain `lore task next` inside the store clone) ranks the tasks of that org's store across its folders, the company folder and the org's other stores included; plain `lore task next` from the home folder does the same for the only org setup of this machine (it says `setup <name> (the only org setup on this machine)` first), and with two or more org setups it is refused with a hint naming `lore task next --store <org>` and `lore task next --setup <setup>` with this machine's orgs and setups; `--claim --push` takes the first, and its claim lands under the task's own folder on its store's signals ref, the place a later `lore task release --id <id>` finds it; each printed next step carries the same `--setup` or `--store`. `lore task heartbeat` with no `--id` renews every claim you hold on the org stores of this machine (their held record is the store clone's) from the home folder, inside the store clone and in a code repo, after the code repo's own claims; within `lease.renew_every` of the last renewal it names the claim as held instead. `lore task heartbeat --daemon`, `lore inbox` and `lore mission status` still run in a code repo.

| Operation | What is checked | What is written (claims on a task push signals and the task row together, atomically) |
|---|---|---|
| **claim** (`task claim`, `task next --claim`) | task ready, no unexpired claim, you hold fewer than `agents.max_claims_per_agent` | a claim with a fresh `claim_token` and `expires = now + lease.ttl` (20m); the task row's `claim_epoch` goes up by one, status `in_progress`, `attempts` counted. Your own claim still on the ref is never taken twice: while its lease runs it resumes (nothing written); once its lease ran out (nobody took the task, or its token would be gone) it is renewed like a heartbeat, same token, `epoch` and `attempts`, and the line says `renewed your claim on <id> … your lease had run out and nobody took the task`; `task next --claim` renews the lapsed claim it resumes the same way |
| **renew** (`task heartbeat`) | your token is on the ref (a lease that ran out still renews while your token is there: nobody took the task) | a later `expires`, `renewed_seq + 1`; the task row is not touched |
| **release** | your token is on the ref | the claim is removed; the task row gets back the status and `started_at` it had before the claim, one `release` claim-log entry, and the holder's time span only when the claim saw work (a heartbeat, or held a minute or more, or a status set during the claim: `lore task start` or `lore task edit --status`, which the release keeps) |
| **done, note, gate request by the holder** | your token is on the ref **and** the row's `claim_epoch` matches your claim (a lease that ran out still passes while your token is there) | the row change and the claim change, in one atomic push; a note or gate request on a lease that ran out renews it in the same push |
| **revoke, cancel, reassign by a person** | a human identity | the claim is removed and `claim_epoch` goes up |
| **expire** | `now > expires + lease.grace` **and** the same `renewed_seq` was seen for at least `lease.renew_every` | nothing: readers treat it as absent; the next writer drops it |

The `claim_token` is a ticket number for one claim. The row's `claim_epoch` counts how many times the task has been claimed or taken back.

**Fencing** means lore refuses a write from an agent whose claim was taken from it. Its token is gone from the list, or the row's `claim_epoch` has moved on. So its `done` fails with `E_LOST_CLAIM` (exit 12), `E_CANCELLED` (13) or `E_REASSIGNED` (14). Expiry needs both the time check and an unchanged `renewed_seq`. So a reader with a fast clock cannot steal a lease that is still being renewed. A lease that only ran out is not a lost claim: every decision reads the list fresh and the write is a compare-and-swap push, so while your token is still on the list nobody took the task, and your claim, heartbeat, note or done goes through (claim, heartbeat, note and gate request renew the lease). When the claim on the list is your own actor's and still refused (held in another session, or a claim the task's `claim_epoch` no longer matches), the `E_LOST_CLAIM` hint says so instead of "stop work": another session's holds it, or `lore task claim --id <id>` (with `--push` where claims need it) takes it back.

**Reclaim:** an expired claim is free. The next `task next --claim` takes the task, counts up `attempts` and names the previous holder. Past `agents.max_attempts` (3) the task becomes `blocked` ("failed 3 times") and goes to its owner's inbox.

Claims for tasks of a linked client repo sit on the **store's** signals list, never on the client's. How the agency runs its agents is internal.

## Done, then merged

A task is finished once. The holder's `lore task done` drops its claim and, in the same push, leaves a done marker on the signals ref (`<folder>/done/<task>.json`, kept 7 days): a clone whose copy of the row still says todo (not synced yet) cannot claim the task again, `task next` skips it and moves on, and a second `lore task done` on a finished task changes nothing and prints who finished it (`· <id> was already done by <who> at <time>; nothing changed`, `already_done` and `done_by` in `--json`). A claim of a finished task is refused with one message whether the done reached the signals ref, the row or both (`E_REFUSED <id> was already done by <who> at <time>`, hint `lore task next`). A task reopened after the done (its row edited later) is claimable again. `lore sync resolve --take kept|other` keeps the same side of nested conflict blocks at every level.

`lore task done` records that the work is finished. Whether the code reached `main` is checked later and separately. Any later `task next`, `task list`, `mission status` or `inbox` in a checkout of the task's repo does the check:

```text
 done <id> --commit <sha> | --pr <n>    → done_commit / done_pr on the row
 check, in order (writes nothing until it finds the merge):
   1. git merge-base --is-ancestor <done_commit> origin/main
   2. git log origin/main --grep "Lore-Task: <id>"   (squash and rebase merges)
   3. merge_check.adapter: github → gh pr view <n>
 found     → merged_at = the merge commit's date, written once
 not found → "done, not merged yet"
 no commit, no trailer, no adapter → "done (merge unknown)"; `lore task merged --id <id>` marks it by hand
```

If the linked store cannot be reached, the command still runs and prints `lore: warning: merge state not checked: <reason>; done tasks may show as not merged yet (the next lore command retries)`.

While you hold a claim, lore's `commit-msg` hook adds a `Lore-Task: <id>` trailer (a last line in the commit message) to your commits. It does this only when the task row lives in the repo you commit to, or is that repo's own linked client task. A task that lives only in the store never puts its id into a client repo's commits. Its merge is found by ancestry (is the commit part of `main`'s history?) or the adapter.

`--after tsk_X` waits for **merged** when the task it waits on is in another repo, and for **done** in the same repo. `--after tsk_X:done` or `:merged` overrides it.

<!-- example: agents#done-then-merged only=3,4,5,6,7 -->
```console
# agent-backend, in agents/api
$ lore task done --id tsk_162d…9666 --commit $(git rev-parse HEAD) --lesson 'invite links stop working after 48 hours; the README says so'
lore: committed locally to store agency/client2; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/client2/tasks/tsk_162d…9666.json (store agency/client2)
lore: wrote ~/.lore/stores/agency/notes/client2/memories/mem_f147…5139.json (store agency/client2)
✓ tsk_162d…9666 done (commit 86dbae4, not linked to the task; link it with: lore link add --entity tsk_162d…9666 --commit 86dbae41af28); waiting for merge
  lesson → mem_f147…5139

# agent-frontend, in agents/app
$ lore task next --claim --mission msn_2ad3…f891 --repo app --push
ERROR: [E_NO_WORK] nothing ready:
  tsk_54fe…28f2 build the invite page: waits for tsk_162d…9666 to be merged (done, 86dbae4 not on the default branch yet)
  hint: lore task board shows what each task waits on
(exit 10)

# alice, in api
$ git pull -q && git merge -q --no-ff --no-edit origin/agent-backend/invite && git push -q origin main
lore: set lore.clientrepo=true in ~/work/api/.git/config: this is a client repo, so its commit hook refuses a commit no lore can check for internal notes (`lore where` lists it; `git config --unset lore.clientrepo` removes it)
$ lore task list
lore: committed locally to store agency/client2; lore sync sends it
✓ tsk_162d…9666 merged 10:28 (found by: ancestry, 05513aa)
○ tsk_54fe…28f2 build the invite page [medium]  [agency/client2]
○ tsk_c0f3…b5b8 deploy to staging [medium]  ⏸ gate: needs approval  [agency/client2]

# agent-frontend, in agents/app
$ lore task next --claim --mission msn_2ad3…f891 --repo app --push
lore: committed locally to store agency/client2; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/client2/tasks/tsk_54fe…28f2.json (store agency/client2)
✓ claimed tsk_54fe…28f2 build the invite page (lease 20m, token ct_dfebf…) on the signals ref refs/lore/signals/main of store agency/client2 (pushed to the store's remote now: teammates see it on their next fetch)
  read its notes: lore task show --id tsk_54fe…28f2
```
<!-- /example -->

## Org resolution

```text
 in: LORE_ORG, repo_orgs in config.yaml, .lore/org.yaml, the repo's remotes, every store's link index
 first rule that answers wins:
   1. LORE_ORG=<entry>
   2. lore org use <entry>          (checked against the remote: a fork with the upstream's id is skipped)
   3. .lore/org.yaml                (only when the remote matches that org's own: patterns)
   4. a link to this project id AND remote in one entry's stores
   5. own: / repos: patterns
   6. none → fallback identity, everywhere/ personal notes only
 two entries at one rule → E_ORG_AMBIGUOUS (exit 17)
 a staging entry (store branch ≠ the store's default) never matches by rules 4–5: only by 1–2
```

Org resolution is how lore decides which org (which company setup) a repo belongs to. Remote URLs are compared as `host/owner/repo` (ssh or https, with or without `.git`), read from the raw `remote.<name>.url`. `*` matches one path segment, `**` any depth. A pattern always starts with the host: `client-*` or `acme` alone is refused with the pattern it most likely meant (`github.com/<owner>/client-*`, `github.com/acme/*`, the owner taken from the command's other patterns) and the same command with it as the hint. When an org is found, its identity is used and the git email fallback is skipped. `LORE_ACTOR` is accepted only for agents and bots (`system:`, `cron:`, `plugin:<label>`) on that org's **roster** (the list of people and agents in an org); a bot needs an owner there (`lore actor register --kind <kind> --label <label> --owner @<person>`), and any other value is said on stderr and not used.

## `lore org link` and the link scan

```text
 lore org link agency --project client      (asks first; --yes in scripts)
   1. pick the store: the one already holding client/, else the entry's first store (or --store <url>)
   2. check the store (marker, no code files) and that its branch exists
   3. create client/ as a lore project if missing
   4. add {project_id, remote} to client/_meta.json "links"   (a set: concurrent links merge)
   5. commit on the store branch and push

 every command: scan each store's folders for links → index {(project id, remote) → folder},
 cached in ~/.lore/cache/links-<entry>.json, rebuilt when a store commit changes
```

Linking writes no file of the repo. The render that follows (`lore render`, or the one `lore sync` runs) wires only the coding tools the repo already uses or that you picked for it, never every tool of your setup; see [Which file your coding tool reads](../choosing/render-targets.md).

The link file, as it sits in the store:

<!-- example: internals#store-layout only=5 -->
```console
# alice, in ~
$ git -C ~/.lore/stores/agency/notes cat-file -p main:client/_meta.json
{
  "_v": 1,
  "features": [
    "hlc",
    "task_tree"
  ],
  "links": [
    {
      "project_id": "prj_05e9…4603",
      "remote": "github.com/client/website"
    }
  ],
  "min_lore_version": "0.1.18",
  "project_id": "prj_3b62…e913"
}
```
<!-- /example -->

A **link** tells lore that a store folder holds the agency's private notes for one client repo. The scan matches the project id **and** the remote, so a fork that copied the project id is not treated as linked: no write goes to the agency's folder from it. A fork of a client repo still keeps the client's leak guards (fail closed), because lore cannot tell a fork from a client repo that moved to a new address; `lore whoami` names the old remote and says how to re-link the repo here or, for a copy that is another project, give it a new id. A repo linked from two folders needs one marked `--default` for `--to agency` to work.

## Store checks

<!-- ds:block id=team-store-allowed-a7qb5uuj lines=1-30 -->
A repo listed under `stores:` must be a lore store: it needs `lore-store.yaml` at its root, and may hold only lore's files (the signpost `.0-needs-lore-0.1.18-or-newer` that lore 0.1.17 names when it refuses the store, row files at `<folder>/<table>/<id>.json` and `_store/<table>/<id>.json`, each folder's `_meta.json` and `_purged.json`, `lore-org.yaml`, and the files of the older layouts: `.lore/` folders and `<table>/<id>.json` at the top) plus the starter files a git host offers (`README*`, `LICENSE*`, `LICENCE*`, `COPYING*`, `.gitignore`, `.gitattributes`) and `.github/workflows/lore-*.yml`. Lore checks this (with `git ls-tree`, no checkout) every time it reads a store at a new commit, and refuses with `E_NOT_A_STORE` otherwise, so a code repo is never cloned into `~/.lore/stores` or committed to by lore. `lore store init --name <name> --url <url>` turns a new empty repo into a store and attaches it; `lore store init --name <name> --url '<url>?branch=staging'` creates another store branch, and `lore store add --name <name> --url <url> --branch staging` attaches it in another repo. A missing store branch is never created by accident.

## Leak guards

<!-- ds:block id=team-leak-words-jpw3kpv4 -->
A **leak** is internal or personal content reaching anything the client can read. Four guards:

```text
 1. safe default      in a client repo, kinds default to the internal project; unsure → refuse (E_LINK_UNKNOWN)
 2. nothing inside    internal render, cache and context live under ~/.lore, never in the checkout
 3. checks            id rule:   a row of another project, or a pointer to an internal/personal row
                      text rule: 8 or more consecutive words that also appear in an internal or personal row
                      secrets:   credential patterns
                      run at: commit (the lines the staged files add; a match names file:line),
                      pre-push (the lines each pushed commit adds, in every file version and
                      commit message the push sends; a match names file:line), live-ref writes,
                      every landing commit, `lore check pr-body`; unsure → block
 4. one deliberate    `lore move` shows the text, asks, moves the row with the same id
    step to share     (one home: never a second copy)
```

<!-- example: agency#leak-blocked only=1,2 -->
```console
# alice, in website
$ mkdir -p src && echo '// they approve estimates faster when we send them on WhatsApp' > src/notes.ts && git add src/notes.ts
$ git commit -m 'add notes'
ERROR: [E_LEAK_BLOCKED] blocked, this commit would share internal content with the client repo:
  commit src/notes.ts:1                    matches internal note mem_c0f3…b5b8 (10 words in a row; the check blocks a verbatim copy of 8 or more words)
  hint: Remove the internal text from src/notes.ts
(exit 1)
```
<!-- /example -->

The pre-push check refuses a push whose new commits carry internal ids, internal sentences or secrets, and refuses when the store cannot be read. Like the commit check, it reads only the lines the pushed commits add (a merge adds only the lines that are new against every parent), so a push of an unrelated edit to a file committed long ago (a hand-written `CLAUDE.md` whose old text is now a note in the store) goes out, while a pushed line that quotes a note is refused, named as `<file>:<line>`; the secret check still reads every pushed file version whole. `git commit --no-verify` and `git push --no-verify` skip every local check, so the client's own CI should run `lore check leaks --ci` as the backstop. It needs no store and prints no internal name: it scans the files the pushed commits add or change, and their names, with the secret rules (the built-in rules, the repo's `.gitleaks.toml` and the committed `.lore/data/secret_allows` entries; the team's own rules live in the store, so they do not apply there). The commits are those after `--since <commit>` (the last commit CI checked) when given, else the push or pull request range the CI names (GitHub Actions' event payload, GitLab's `CI_MERGE_REQUEST_DIFF_BASE_SHA` or `CI_COMMIT_BEFORE_SHA`), else those since the default branch (`origin/HEAD`) on a branch, else the last 50 commits, so every commit of a push straight to the default branch is scanned, not only its last one (see [CI checks](../operations/ci.md#which-commits-lore-check-leaks---ci-scans)); a value the same file already held before them is not new, as at commit and push, and a clone with less history than that (`fetch-depth: 1`, a young repo) has every tracked file scanned, and it checks the ids and structure of `.lore/data`, so a key pushed with `--no-verify` fails the job, named by file, line, rule and hash with its `lore secrets allow --file` line, never the value; it does not see pasted sentences. In an org's own repo it accepts the full actor copies lore writes there (a person's key and handle, stamped with the org as origin); it cannot read the org config, so the stamp is what it goes by, and a hand-written stamp would pass it too, so the commit and push hooks stay the real guard for a client repo.

What hooks cannot stop, honestly:

- `git commit --no-verify` and `git push --no-verify` (they skip every local check), and edits in the GitHub web interface.
- A teammate without lore.
- Text that was reworded instead of copied.
- An agent writing internal text into a pull request body it never checked.
- A client-owned machine that has your stores cloned, and backups of `~/.lore`.

The guards catch copy-paste mistakes; they are not a wall. See [Cleaning up after a leak](use-cases/leak-cleanup.md).

## Rendering context outside the checkout

```text
 .lore/LORE.md (committed)                  this repo's own rows only
 ~/.lore/render/<repo-id>/<store-id>.md     one private file per internal store (org, personal); first line:
                                            "private: never quote this content in committed files…"
 ~/.lore/render/<repo-id>/index.md          the list of those files
 ~/.lore/cache/<project-id>.db              store and personal rows for this repo (one org only)
 ~/.lore/cache/repo/<repo>-<hash>.db        this repo's branch and live rows only
```

<!-- example: agency#render -->
```console
# alice, in website
$ lore render
✓ .lore/LORE.md (3129 bytes, budget 16000 codex; 1 full + 0 line rules, 0 + 0 hotfixes, 1 topic)
✓ .lore/LORE.full.md (344 bytes, every pinned entry in full; not imported)
lore render: .lore/LORE.md and .lore/LORE.full.md not committed in this repo yet; added to this clone's .git/info/exclude so a git pull of a teammate's commit that adds them works (to commit them from this clone: git add -f .lore/LORE.md .lore/LORE.full.md)
= ~/work/website/AGENTS.md (pointer present)
✓ private context → ~/.lore/render/app-rep_28f8…03df (2 stores, 2 rows, outside the checkout); no target tool has a private file: agents get it through `lore context` and the hooks
$ cat ~/.lore/render/*/index.md
<!-- generated by lore; private: never quote this content in committed files, client files, commits, PRs or chat -->
repo app (rep_28f8…03df)
- agency-sto_7a67…9504.md  store agency, agency/org  -  internal
- me-sto_4c59…6c38.md  store me, me/everywhere  -  internal
$ git status --short
```
<!-- /example -->

Agents get the private text through `lore context` and the session hooks; nothing in the checkout points at it. The checkout itself holds no internal text, so a Docker build, a ZIP or an upload carries none.

## Pitfalls

- **Never edit lore's hidden lists by hand** (`refs/lore/live`, `refs/lore/signals`). Lore writes them itself. If the remote's live list is deleted or replaced (a mirror push, a mistaken delete), lore stops writing to it and keeps a local backup: every live write, `lore sync` and `lore land` refuse with `E_READ_ONLY` (exit 23) until then, and that sync exchanges nothing with the attached stores either, so it never reports success it could not deliver. `lore live restore` pushes that backup back. A remote that never had the live list (the repo moved to a new remote) is not a deletion: lore says `never pushed here`, keeps the rows on this clone and goes on writing; your next push or `lore sync` creates it there. This needs a clone that saw the list with lore 0.1.18 or newer, which records the address it fetched the list from (a command run after `git remote set-url` and before the next fetch keeps the old address, so the move is still recognised). A clone last updated by an older lore cannot tell the two apart: it stops with `E_READ_ONLY` saying the repo moved or the list was deleted, and if the repo moved, `lore live restore` publishes the local live rows on the new remote, records its address, and writes go on. A write that goes to the local live list names it (`lore: wrote tasks/<id> on this clone's refs/lore/live (a live row, not pushed yet; <file> is unchanged)`), never the row file it left alone.
- **A protected `main` delays landing.** Finished rows wait and ride on your next commit to another branch. `lore explain --id <id>` says why a row has not landed yet.

## Related

- [Concepts](concepts.md): the words before the mechanics.
- [Commands reference](commands.md): each command and its flags.
- [Troubleshooting](troubleshooting.md): when the live ref or a landing goes wrong.
- [Real-repo trial](real-repo-trial.md): the same story on a real git host.
