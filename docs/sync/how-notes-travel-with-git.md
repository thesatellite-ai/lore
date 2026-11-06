# How notes travel with git

**In short:** every note you keep "with the code" is one small file in `.lore/data`. You commit it with your change, push it, and your teammate gets it with `git pull`, exactly like code. lore keeps its fast database and those files in step by itself, before and after every command.

## What it is and when you want it

Think of `.lore/data` as a card box that lives inside the repo: one card (file) per note. The database `.lore/lore.db` is a fast index of that box that stays on your machine and is never committed. Because the cards are ordinary files:

- a note written on a branch stays on that branch until the branch merges, like the code it belongs to;
- a pull request shows new and changed notes in its diff;
- a teammate who clones or pulls gets the notes with no extra step.

lore calls keeping the two in step a **sync pass** (it reads files that git changed into the database, and writes database changes out to files). One pass runs before every lore command and one after it, so you rarely run it by hand.

You want this page when you set up lore in a repo that other people also use, or when you wonder which files to commit.

## Before you start

- lore installed ([Install](../../README.md#install)) and a git repo with a remote. Here Alice (an agency developer) works in `github.com/client/website`.
- No team or org set-up is needed. In a repo with no org, rules, memories and decisions are kept with the code (this page). Tasks are live by default: they go on a live list, a list every teammate sees at once, whatever branch they are on (see [Lore for teams](../team/README.md)).

## Step 1 — Set up lore and commit what it wrote

`lore init` creates the `.lore/data` folder with the first rows (the project, this repo and you), a local cache that lore rebuilds from those files whenever it needs to, a pointer file for your coding tool (here `AGENTS.md`, saved as the `context.targets` setting), and a few lines in `.gitignore` and `.gitattributes`. It also sets up git hooks (small scripts git runs on its own, for example just before a commit) and a merge driver (a small helper git calls to combine two edits of the same note). Both are described in [Hooks and the merge driver](hooks-and-merge-driver.md).

<!-- example: sync#init -->
```console
# alice, in website
$ lore init --non-interactive --name=website
lore initialized
    project_id: prj_7a67…9504
    name:       website
    repo_id:    rep_b69e…5ed7

· context targets: codex (AGENTS.md) (no AI tool file found; AGENTS.md is the cross-tool file, change with --target <profile>)
✓ ~/work/website/AGENTS.md (pointer → .lore/LORE.md)
· saved as context.targets; change with: lore policy set context.targets <profiles> --yes
  next: lore render            to write .lore/LORE.md for those tools
$ git status --short
?? .gitattributes
?? .gitignore
?? .lore/
?? AGENTS.md
$ ls .lore .lore/data
.lore:
data
lock
lore-cache.db
state

.lore/data:
_meta.json
actors
policies
projects
repos
$ cat .gitattributes
# >>> lore-sync >>>
.lore/data/**/*.json text eol=lf merge=lore linguist-generated=true
.lore/data/*.json text eol=lf merge=lore linguist-generated=true
.lore/LORE.md text eol=lf merge=lore-keep-ours linguist-generated=true
# <<< lore-sync <<<
$ git add -A && git commit -qm 'lore: init' && git push -q origin main
```
<!-- /example -->

<!-- ds:block id=sync-datanames-nhh4krj2 -->
`.lore/data` has one folder per kind of row (`rules`, `memories`, `projects`, ...) and one file per row, named after the row's id. `_meta.json` says which project the data belongs to. Commit everything `lore init` wrote; the local cache (`.lore/lore-cache.db`), the lock file, `.lore/state`, and a v0.1.17 `.lore/lore.db` are already in `.gitignore`.

## Step 2 — Write a note and commit it with your change

Add a rule. lore writes the row file at once and reminds you that it is not committed yet. A rule is a hard "always" or "never", like a sign on the office wall.

<!-- example: sync#first-rule -->
```console
# alice, in website
$ lore rule add --body 'Run the tests before you push.'
✓ rul_2ad3…f891
  scope: [master] severity: must
$ git status --short
?? .lore/data/rules/
$ cat .lore/data/rules/rul_2ad3…f891.json
{
  "_hlc": "2026-11-02T10:00:30.000Z/0000/act_162d…9666",
  "_o": {
    "cmd": "rule add",
    "git": {
      "branch": "main",
      "commit": "466cc836cfd2",
      "state": "branch"
    },
    "repo": "rep_54fe…28f2",
    "store": "repo"
  },
  "_table": "rules",
  "_v": 2,
  "_w": {
    "v": "0.1.0-dev"
  },
  "activation": "always",
  "body": "Run the tests before you push.",
  "created_at": "2026-11-02T10:00:00.000000001Z",
  "created_by": "act_162d…9666",
  "id": "rul_2ad3…f891",
  "project_id": "prj_7a67…9504",
  "severity": "must",
  "source_kind": "manual",
  "trust_score": 0.5,
  "updated_at": "2026-11-02T10:00:00.000000001Z",
  "updated_by": "act_162d…9666"
}
$ lore sync status
baselined:        true
data dir present: true
tracked files:    4
pending exports:  0
open conflicts:   0
trash rows:       0
uncommitted:      1
meta quarantined: 0
live rows:        0 (_root)
$ git add -A && git commit -qm 'lore: test rule' && git push -q origin main
```
<!-- /example -->

The file is plain JSON with sorted keys, one per line, so a diff shows exactly which field changed. `lore sync status` sums up the state: `uncommitted: 1` is the new rule file. Commit it the way you commit code: in the same commit as the change it belongs to, or on its own.

## Step 3 — A teammate clones the repo

Bob (another agency developer) clones the repo. There is no `lore.db` in a fresh clone, so his first lore command builds it from `.lore/data`, and the rule is there:

<!-- example: sync#teammate-clone -->
```console
# bob, in ~
$ git clone -q https://github.com/client/website

# bob, in website
$ lore rule list
rul_2ad3…f891 [must/always] Run the tests before you push.
```
<!-- /example -->

From now on, `git pull`, `git checkout`, `git merge` and `git rebase` just change files, and the next lore command reads the change in. Bob never has to "import" anything.

## Step 4 — Run a pass by hand when you want to see it

`lore sync` runs one pass now and prints what changed; `lore sync status` shows what is waiting:

<!-- example: sync#sync-by-hand -->
```console
# bob, in website
$ lore sync
· already in sync
$ lore sync status
baselined:        true
data dir present: true
tracked files:    4
pending exports:  0
open conflicts:   0
trash rows:       0
uncommitted:      0
meta quarantined: 0
live rows:        0 (_root)
```
<!-- /example -->

| Line in `lore sync status` | What it means |
|---|---|
| `pending exports` | rows changed in the database that are not written to files yet (normally 0: the pass after each command writes them) |
| `open conflicts` | rows where the file and the database both changed; see [Conflicts and duplicates](conflicts-and-duplicates.md) |
| `trash rows` | rows removed because their file disappeared; see [Trash, restore from git and purge](trash-and-purge.md) |
| `uncommitted` | files in `.lore/data` that git has not committed yet |
| `live rows` | rows on the live list (tasks and other live rows, see [Lore for teams](../team/README.md)) |

## Try it — turn sync off for one command

<!-- ds:block id=sync-env-qmha7pjc -->
Three environment variables (settings you put in front of a command, like `LORE_SYNC=0 lore ...`) change what a pass does. `LORE_SYNC=0` turns syncing off for that command: the database changes, but no file is written. `LORE_SYNC_GIT=0` keeps syncing files but never touches git settings, `.gitattributes` or hooks. `LORE_SYNC_QUIET=1` hides the `lore sync:` progress lines (errors still print). `LORE_SYNC_ALLOW_SECRETS` was removed in 0.1.18: lore ignores it and `lore doctor` warns while it is still set.

Here Alice adds a memory with sync off. A memory is something worth remembering, like "the client replies faster on WhatsApp than email". Nothing shows in git until the next normal command (here `lore sync`) writes the file:

<!-- example: sync#sync-off -->
```console
# alice, in website
$ LORE_SYNC=0 lore memory add --body 'The client replies faster on WhatsApp than email.'
✓ mem_05e9…4603
  scope: [master]
$ git status --short
?? .lore/data/memories/
$ lore sync
lore sync: imported 1
$ git status --short
?? .lore/data/memories/
$ git add -A && git commit -qm 'lore: whatsapp memory' && git push -q origin main
```
<!-- /example -->

`LORE_SYNC_QUIET=1` hides the `lore sync:` lines that the first command prints:

<!-- example: environment#sync-quiet -->
```console
# alice, in website
$ lore rule add --body 'Run the tests before you push'
✓ rul_54fe…28f2
  scope: [master] severity: must
$ LORE_SYNC_QUIET=1 lore memory add --body 'The client replies faster on WhatsApp than email.'
✓ mem_05e9…4603
  scope: [master]
```
<!-- /example -->

`LORE_SYNC_GIT=0` writes the files but leaves git alone: no hooks and no `.gitattributes` until a pass runs without it:

<!-- example: environment#sync-git-off -->
```console
# alice, in shop
$ LORE_SYNC_GIT=0 lore init --non-interactive --name=shop
✓ lore initialized at ~/work/shop
    project_id: prj_c0f3…b5b8
    name:       shop
    origin:     https://github.com/client/shop
    db:         ~/work/shop/.lore/lore.db
    identity:   human:alice@agency.example (git config)
· context targets: codex (AGENTS.md) (no AI tool file found; AGENTS.md is the cross-tool file, change with --target <profile>)
✓ ~/work/shop/AGENTS.md (pointer → .lore/LORE.md)
· saved as context.targets; change with: lore policy set context.targets <profiles> --yes
  next: lore learn-from docs   to bootstrap from existing markdown
        lore render            to write .lore/LORE.md for those tools
lore sync: exported 0 rows to .lore/data — review and commit it
$ ls .git/hooks | grep -v sample; cat .gitattributes 2>/dev/null || echo '(no .gitattributes)'
(no .gitattributes)
$ lore sync
lore sync: imported 3
$ ls .git/hooks | grep -v sample; cat .gitattributes
commit-msg
post-checkout
post-commit
post-merge
post-rewrite
pre-commit
pre-push
# >>> lore-sync >>>
.lore/data/**/*.json text eol=lf merge=lore linguist-generated=true
.lore/data/*.json text eol=lf merge=lore linguist-generated=true
.lore/LORE.md text eol=lf merge=lore-keep-ours linguist-generated=true
# <<< lore-sync <<<
```
<!-- /example -->

Text that looks like a password stops `lore memory add` with a question; a bare `--yes` does not answer it. `--not-a-secret --reason "<why>" --yes` stores the row as not a secret (`--secret-ref` and `--secret-unstored` are the other answers), so its file is in `.lore/data` at once. lore's commit hook still checks every file and refuses to commit one that holds a credential pattern, so edit or delete such a row before you commit (the `''` inside `pass''word` below only keeps this page from tripping that hook; the shell joins the two halves into one word before lore sees it):

<!-- example: environment#not-a-secret -->
```console
# alice, in website
$ lore memory add --not-a-secret --reason 'example value, not a real password' --yes --body 'The staging password is pass''word=example-only'
✓ mem_76bd…3f10
  scope: [master]
$ ls .lore/data/memories | grep -c mem_76bd…3f10 || true
1
```
<!-- /example -->

The database remembers which rows still need a file, so a row written with sync off, or by another tool that changed `lore.db` directly, is never lost: the next pass writes it out.

## Pitfalls

- **Forgetting to commit `.lore/data`.** A note that is only on your machine is not shared. Every command that writes a row file prints `N file(s) in .lore/data not committed`; `lore sync status` always shows the count. Commit the files with your change.
- **Deleting `.lore/data` by mistake.** lore leaves the database alone and tells you; `git checkout HEAD -- .lore/data` brings the files back. See [Trash, restore from git and purge](trash-and-purge.md#step-2--get-back-a-deleted-loredata).
- **Editing a row file by hand.** It works (the next pass reads it in), but a broken file (bad JSON, conflict markers) is skipped and reported on every command until you fix it, and the pre-commit hook refuses to commit it.
- <!-- ds:block id=sync-maxfilesize-zk9m2syn --> **Very large rows.** A row file larger than 4 MiB is refused on read and reported, never imported. Keep long logs out of notes.
- <!-- ds:block id=sync-locktimeout-rh9uf4cq --> **Two lore commands at once** (for example two agents in one repo) take turns. Each one puts a lock on the database, like a "busy" sign on a door. A pass waits up to 15 seconds for the other one's lock, then gives up with an error. Run the command again.
- **One database per working folder.** Each clone and each git worktree (a second working folder of the same repo) has its own `lore.db`; they share notes only through the committed files.

## Related

- [Branches: peek and promote](branches-peek-promote.md): notes on other branches.
- [Conflicts and duplicates](conflicts-and-duplicates.md): two people changed the same note.
- [Hooks and the merge driver](hooks-and-merge-driver.md): what lore adds to git.
- [Lore for teams](../team/README.md): live rows, stores and orgs.
- [Feature index › Git sync](../reference/features.md#git-sync-notes-travel-with-your-commits)
