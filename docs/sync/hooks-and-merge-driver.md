# Hooks and the merge driver

**In short:** lore adds a few short lines to your repo's git set-up so notes stay safe without you thinking about them. A *merge driver* (a small helper git calls to combine two edits of the same note) merges them field by field. A few *hooks* (small scripts git runs at fixed moments, like before a commit) check note files before you commit them and read new notes in after a pull. lore sets all of this up by itself and never takes over hooks you already have.

## What it is and when you want it

git lets a repo plug in its own tools at two places:

- **Merge drivers:** a program git calls instead of its normal line-by-line merge for certain files. lore's driver knows that a note file is a list of fields, so it can keep the change Alice (an agency developer) made to one field and the change Bob (another agency developer) made to another (see [Conflicts and duplicates](conflicts-and-duplicates.md)).
- **Hooks:** scripts in `.git/hooks` that git runs before or after a commit, checkout, merge or push. They are never shared by `git clone`; each clone has its own.

lore sets both up on its first command in a clone and checks them, quickly, after every command. You want this page to know what was added to your repo, to check it, or to understand why a commit was refused.

## Before you start

- A repo set up with lore ([How notes travel with git](how-notes-travel-with-git.md)). Here Alice works in `github.com/client/website`.
- If your team uses a hook manager (a tool that shares one set of hooks with the whole team: husky, lefthook, or a committed `.githooks` folder), nothing special is needed: lore adds its lines to that manager's hook scripts.

## Step 1 — See what lore set up

`lore sync install-git` does the set-up now and prints what it found (on a repo that is already set up, nothing changes). The hooks are in `.git/hooks`, and the driver is in the clone's own git config:

<!-- example: sync#hooks only=1,2,3 -->
```console
# alice, in website
$ lore sync install-git
✓ git wired for lore data
  hooks dir:   ~/work/website/.git/hooks
  attributes:  changed=false
  gitignore:   changed=false
  config:      unchanged
  hooks:       unchanged
$ ls .git/hooks | grep -v sample
commit-msg
post-checkout
post-commit
post-merge
post-rewrite
pre-commit
pre-push
$ git config --get-regexp '^merge\.lore'
merge.lore.name lore field-level row merge
merge.lore.driver lore merge-driver %O %A %B %P
merge.lore-keep-ours.name lore: keep ours, re-render after merge
merge.lore-keep-ours.driver lore merge-driver --keep-ours %O %A %B %P
```
<!-- /example -->

<!-- ds:block id=sync-mergedriver-config-bur8yta8 -->
There are two drivers. `lore` merges row files field by field. `lore-keep-ours` is for `.lore/LORE.md`, a summary file lore generates: on a merge it keeps your side, and the next lore command writes the file again from the merged notes.

<!-- ds:block id=sync-gitattributes-xzr8sqyk -->
The `.gitattributes` lines (shown in [How notes travel with git](how-notes-travel-with-git.md#step-1--set-up-lore-and-commit-what-it-wrote)) tell git which files use which driver. They also force plain `LF` line endings (the invisible end-of-line character) for note files, so Windows and macOS machines write the same bytes, and mark the files as generated, so GitHub folds them in pull request diffs.

## Step 2 — Read a hook

Each hook gets one block between `>>> lore-sync >>>` and `<<< lore-sync <<<` markers. The block does nothing when lore is not installed, and it checks that the installed lore knows the command before it calls it, so a teammate on an older lore can still commit:

<!-- example: sync#hooks only=4 -->
```console
# alice, in website
$ cat .git/hooks/pre-commit
#!/bin/sh
# >>> lore-sync >>>
( if command -v lore >/dev/null 2>&1; then if lore sync hook --help >/dev/null 2>&1; then lore sync hook pre-commit; else echo "lore: installed lore cannot check .lore/data (no 'sync hook'); upgrade it" >&2; fi; fi ) || exit $?
# <<< lore-sync <<<
```
<!-- /example -->

<!-- ds:block id=sync-hooklines-3282rwuw -->
| Hook | What lore's block does |
|---|---|
| `pre-commit` | runs a pass, then refuses the commit if a note file is broken JSON, has conflict markers, or looks like it holds a password or key; otherwise stages `.lore/data` |
| `post-checkout`, `post-merge`, `post-rewrite` | reads the new files in at once after a checkout, pull, merge or rebase; never fails the git command |
| `commit-msg` | adds a `Lore-Task:` line for the task you are working on, then checks the message and staged files for internal notes that must not reach a client repo (see [Lore for teams](../team/README.md)) |
| `post-commit`, `pre-push` | carry finished live rows (tasks on the live list that are done) to a protected main (a main branch GitHub will not let you push to directly) in a small extra commit (see [How it works › Protected main](../team/how-it-works.md#protected-main-the-ride-along-commit)) |

## Try it — a broken note file is refused

Alice breaks a note file by hand and tries to commit it. The pre-commit hook stops the commit and names the file; restoring the file fixes it:

<!-- example: sync#commit-refused -->
```console
# alice, in website
$ printf '{ broken' > .lore/data/rules/rul_76bd…3f10.json && git add .lore
$ git commit -m 'oops'
lore sync: invalid-file rules/rul_76bd…3f10.json: canonjson: decode: invalid character 'b' looking for beginning of value
ERROR: [E_INVALID_INPUT] refusing to commit broken lore files: rules/rul_76bd…3f10.json (invalid-file), .lore/data/rules/rul_76bd…3f10.json (invalid JSON)
  hint: fix the JSON syntax of the files marked invalid JSON; remove the credential from the files that hold one, then commit again
(exit 1)
$ git checkout -q HEAD -- .lore/data && git status --short
```
<!-- /example -->

## Pitfalls

- **lore never changes `core.hooksPath`** (the git setting that says which folder holds the hooks). When a hook manager owns the hooks folder, lore adds its block to the scripts there. Its block goes right after the first line, so an early `exit 0` in your own script cannot skip it.
- **A fresh clone has no hooks until lore runs.** git does not copy hooks when you clone. Run any lore command (or `lore sync install-git`) once; until then, a merge in that clone uses plain line merging.
- **`LORE_SYNC_GIT=0` leaves git alone.** With it set, lore still syncs files but never adds hooks, the driver or `.gitattributes` lines. Use it on build machines where you do not want lore to change the clone.
- **The hint after a refused commit talks about markers even for broken JSON.** If the message says "invalid JSON", the file is not valid JSON at all: restore it from git (`git checkout HEAD -- <file>`) or fix the text.
- **Removing lore's block by hand is undone.** lore checks the set-up after every command and puts the block back. To stop it, set `LORE_SYNC_GIT=0`.

## Related

- [Conflicts and duplicates](conflicts-and-duplicates.md): the merge driver at work.
- [The GitHub workflow](github-workflow.md): merging on pull requests, where hooks and drivers do not run.
- [Lore for teams › commands › lore check leaks](../team/commands.md#lore-check-leaks)
- [Feature index › Git sync](../reference/features.md#git-sync-notes-travel-with-your-commits)
