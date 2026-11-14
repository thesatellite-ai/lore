# Hooks and the merge driver

**In short:** lore adds a few short lines to your repo's git set-up so notes stay safe without you thinking about them. A *merge driver* (a small helper git calls to combine two edits of the same note) merges them field by field. A few *hooks* (small scripts git runs at fixed moments, like before a commit) check note files before you commit them and read new notes in after a pull. lore sets all of this up by itself and never takes over hooks you already have.

## What it is and when you want it

git lets a repo plug in its own tools at two places:

- **Merge drivers:** a program git calls instead of its normal line-by-line merge for certain files. lore's driver knows that a note file is a list of fields, so it can keep the change Alice (an agency developer) made to one field and the change Bob (another agency developer) made to another (see [Conflicts and duplicates](conflicts-and-duplicates.md)).
- **Hooks:** scripts in `.git/hooks` that git runs before or after a commit, checkout, merge or push. They are never shared by `git clone`; each clone has its own.

lore sets both up on its first command in a clone and checks them, quickly, after every command. You want this page to know what was added to your repo, to check it, or to understand why a commit was refused.

## Before you start

- A repo set up with lore ([How notes travel with git](how-notes-travel-with-git.md)). Here Alice works in `github.com/client/website`.
- If your team uses a hook manager (a tool that shares one set of hooks with the whole team: husky, lefthook, or a committed `.githooks` folder), lore adds its lines to the hook scripts already there. When that folder is part of the repo (husky's `.husky`, a committed `.githooks`), a lore command never creates a hook file in it and never adds its lines to the team's own hook files there, because a new or changed file there reaches every teammate: it says which hooks it left alone, and `lore fix`, which asks first, writes them and tells you to commit them (or undo them). A folder git ignores (husky 9's `.husky/_`), or one with no committed file in it, is not part of the repo, so lore writes there as in `.git/hooks`.

## Step 1 — See what lore set up

lore does the set-up after every lore command, so there is no command to run for it; on a repo that is already set up, nothing changes. Any command will do, here `lore sync status`. The hooks are in `.git/hooks`, and the driver is in the clone's own git config:

<!-- example: sync#hooks only=1,2,3 -->
```console
# alice, in website
$ lore sync status >/dev/null
$ ls .git/hooks | grep -v sample
commit-msg
post-checkout
post-commit
post-merge
post-rewrite
pre-commit
pre-push
$ git config --get-regexp '^merge\.lore' | sed "s|'/[^']*'|/path/to/lore|"
merge.lore.name lore field-level row merge
merge.lore.driver lore merge-driver %O %A %B %P
merge.lore-keep-ours.name lore: keep ours, re-render after merge
merge.lore-keep-ours.driver lore merge-driver --keep-ours %O %A %B %P
```
<!-- /example -->

<!-- ds:block id=sync-mergedriver-config-cthptudb -->
There are two drivers. When the lore that sets them up is the `lore` on your PATH, they call plain `lore`, as above, so they keep working when a package manager moves the binary on an upgrade. When it is another binary (a build outside PATH, a second copy), they name that binary by its full path (`'/path/to/lore' merge-driver …`), so a merge uses the lore that wrote the notes. Once a working driver is set, lore leaves it alone: running some other lore binary in the clone never repoints it. lore rewrites a driver only when it is missing, or when it names a full path that no longer exists (a deleted build); `lore init` and `lore doctor --fix` also replace a value that is not lore's driver at all, which `lore doctor` reports. `lore` merges row files field by field. `lore-keep-ours` is for the files render generates at a fixed place (`.lore/LORE.md`, `.lore/LORE.full.md` and the path rule files `.claude/rules/lore/lore-*.md` and `.cursor/rules/lore-*.mdc`): on a merge it keeps your side, and the next lore command writes the file again from the merged notes. If one of them is still left unmerged (a merge without the driver, or a `.gitattributes` from before a rule existed), `lore doctor` counts it under `open conflicts` and names the fix: `lore render && git add <file>`.

<!-- ds:block id=sync-gitattributes-symdu732 -->
The `.gitattributes` lines (shown in [How notes travel with git](how-notes-travel-with-git.md#step-1--set-up-lore-and-commit-what-it-wrote)) tell git which files use which driver. They also force plain `LF` line endings (the invisible end-of-line character) for note files, so Windows and macOS machines write the same bytes, and mark the files as generated, so GitHub folds them in pull request diffs.

## Step 2 — Read a hook

Each hook gets one block between `>>> lore-sync >>>` and `<<< lore-sync <<<` markers. Unlike the merge driver, a hook runs whatever `lore` is first on your PATH, not the binary that installed it. The block does nothing when lore is not installed, and it checks that the installed lore knows the command before it calls it. A commit that changes nothing under `.lore/data` (a code-only commit) starts no lore at all in pre-commit: one `git status` of that folder decides, and commit-msg adds the `Lore-Task:` line and runs the leak check in a single lore call (falling back to the separate calls, as before, with a lore that cannot do both), so a code-only commit pays one lore start-up there instead of six. One case is refused on purpose: when a commit changes `.lore/data`, which holds lore 0.1.18 row files (`"_v": 2`), and the `lore` on PATH is older, the pre-commit hook stops the commit and asks you to install lore 0.1.18 or newer, because an older lore cannot check the new files:

<!-- example: sync#hooks only=4 -->
```console
# alice, in website
$ cat .git/hooks/pre-commit
#!/bin/sh
# >>> lore-sync >>>
( { d="$(git status --porcelain --untracked-files=all -- .lore/data 2>/dev/null)" && [ -z "$d" ] && exit 0; true; } && if command -v lore >/dev/null 2>&1; then if ! lore migrate --help >/dev/null 2>&1 && grep -rqsE '"_v": *2' .lore/data; then echo "lore: .lore/data holds lore 0.1.18 rows but the lore on PATH is older; install lore 0.1.18 or newer to commit" >&2; exit 1; elif lore sync hook --help >/dev/null 2>&1; then lore sync hook pre-commit; else echo "lore: installed lore cannot check .lore/data (no 'sync hook'); upgrade it" >&2; fi; fi ) || exit $?
# <<< lore-sync <<<
```
<!-- /example -->

<!-- ds:block id=sync-hooklines-g87bgvsj lines=33-48 -->
| Hook | What lore's block does |
|---|---|
| `pre-commit` | refuses the commit if a changed note file is broken JSON or has conflict markers, or if the lore on `PATH` is older than 0.1.18 while `.lore/data` holds 0.1.18 rows; otherwise stages the row files lore wrote in `.lore/data` and names them in one line (`lore: added 2 lore row file(s) to this commit: …`, up to five; nothing when it added none); when nothing else is staged it adds none and says so (`lore: 1 lore row file(s) left out of this commit: …`), because git decides whether a commit has anything in it before its hooks run, so a plain `git commit` or `git commit -a` would stop at `nothing added to commit` anyway: commit them with `git add .lore/data && git commit`; it never stages the files a `lore fix` format migration rewrote and you have not committed yet (it says how many it left out: commit them on their own with `git add .lore/data && git commit`); when the commit carries row files and `.lore/LORE.md` is committed, it renders it again from those rows and stages it (and `.lore/LORE.full.md` when that is committed too) with one line (`lore: refreshed .lore/LORE.md in this commit for the row files it carries`), so a checkout of the commit has nothing to re-render; whenever a commit hook refuses, your index is put back exactly as you staged it |
| `post-checkout`, `post-merge`, `post-rewrite` | reads the new files in at once after a checkout, pull, merge or rebase; never fails the git command, and changes no file while a rebase, merge, cherry-pick, revert or bisect is still running (so `git pull --rebase` never stops on `.lore/LORE.md`): it renders when the rebase ends, or the next lore command does. A committed `.lore/LORE.md` that you have not changed is left as it is after every checkout, pull, merge and rebase, so no tracked file turns dirty in the middle of your git flow (the next `git pull --rebase` or `git merge` never stops on it, and discarding the file sticks). After a merge or a rebase that combined rows from two sides it can lag behind the rows until the next commit that carries row files, whose `pre-commit` hook renders it again, or until you run `lore render`. When the render changes `.lore/LORE.md` or `.lore/LORE.full.md` it says so in one line (`lore: re-rendered .lore/LORE.md for the rows git brought in; commit it with your next change`); a render with the same bytes says nothing |
| `commit-msg` | adds a `Lore-Task:` line for the task you are working on, then checks the message and staged files for secrets and for internal notes that must not reach a client repo (see [Lore for teams](../team/README.md)); only what the commit adds is refused: a secret-looking value the same row (or the same file) already held in the history you pushed (the last commit when nothing was ever pushed), as after a `lore fix` format rewrite or a moved row file, lets the commit through with one line that counts them (`lore check leaks --rows` lists them); a value in a commit you have not pushed yet (one made with `--no-verify`) is still new, so `git commit --amend` checks every file of the commit it replaces and is refused until the value is gone; a secret in the commit message itself gets a `fix:` line (edit the message, or `git commit --amend`), never an allow command; in a client repo it refuses the commit when no lore on `PATH` can do that check |
| `pre-push` | checks every file version and commit message the push sends that the remote does not have for secrets (in a client repo also for internal ids and internal sentences, and refuses the push when the store cannot be read); as at commit, a secret-looking value the same row already held in a commit the remote has is not new, so a commit that passed the commit check (a `lore migrate run` rewrite) also pushes, with one line that counts them; each refused finding prints its ready `lore secrets allow --row-id <row-id> --field <field> --rule <rule> --match-hash <sha256:…>` line: run it, commit the new `.lore/data/secret_allows` file and push again. `git push --no-verify` skips it |
| `post-commit`, `pre-push` | carry finished live rows (tasks on the live list that are done) to a protected main (a main branch GitHub will not let you push to directly) in a small extra commit (see [How it works › Protected main](../team/how-it-works.md#protected-main-the-ride-along-commit)) |

## Try it — a broken note file is refused

Alice breaks a note file by hand and tries to commit it. The pre-commit hook stops the commit and names the file; restoring the file fixes it:

<!-- example: sync#commit-refused -->
```console
# alice, in website
$ printf '{ broken' > .lore/data/rules/rul_b4f4…a5b0.json && git add .lore
$ git commit -m 'oops'
ERROR: [E_INVALID_INPUT] refusing to commit broken lore files:
  .lore/data/rules/rul_b4f4…a5b0.json (invalid JSON)
  hint: fix the file by hand (a row file is one JSON object named after its own id; settle a conflict with the row's edit command), or restore it with `git checkout HEAD -- <path>`; then commit again
(exit 1)
$ git checkout -q HEAD -- .lore/data && git status --short
```
<!-- /example -->

The hook names each refused file on its own line with the reason: invalid JSON, conflict markers (an unresolved git merge: `lore sync conflicts` lists it), a row whose id is not its file name (and, when the file named after that id exists too, `holds that id too (a duplicate id)`), a file it cannot read (`permission denied` with the `chmod u+rw <file>` that fixes it), or a deleted `.lore/data/_meta.json` (the project's id and format; `git checkout HEAD -- .lore/data/_meta.json` puts it back). A commit that deletes the whole `.lore` folder is refused with `refusing to commit the deletion of the lore project folder .lore` and the `git checkout HEAD -- .lore` that puts it back; to take lore out of a repo on purpose, commit with `git commit --no-verify`. A row file an editor saved with a UTF-8 byte order mark (Windows Notepad) is not refused: the hook removes the mark, says `lore: removed the UTF-8 byte order mark from <file>`, and commits the file as plain UTF-8.

## Rewriting history: rebase, amend, force-push

A note lives in a commit, so a rewrite of history can take notes away with no delete anywhere. lore's hooks watch for it:

- **A forced push that takes notes off the remote is refused.** The pre-push hook compares the remote branch (as this clone fetched it) with what you push. When the push is not a fast-forward and row files on the remote would be gone with no commit deleting them, it stops with `E_NEEDS_CONFIRM` and lists them (`dec_… (decisions, in 3aabf17 "Record Go pin decision")`). Keep them: `git pull --rebase` and push again. Drop them on purpose: `LORE_ALLOW_ROW_DROP=1 git push --force-with-lease`. A remote tip this clone never fetched cannot be compared: fetch first (`--force-with-lease` refuses that push anyway).
- **A rebase that leaves notes out says so.** After `git pull --rebase` drops your own commit as already upstream (someone force-pushed over it), or any rebase leaves row files out that no commit deleted, the hook prints `lore: this rebase removed 1 lore row that no commit deleted` with each id and `bring back: git cherry-pick <commit>`.
- **Commit links follow a rebase or an amend.** The post-rewrite hook gets git's list of old and new commit ids and moves each [commit link](../tasks/link-commits.md) to the new commit (`lore: moved 1 commit link(s) to the rewritten commits`); commit the changed `.lore/data` with your next change. A rewrite made without lore's hooks leaves links on the old commits: `lore doctor` names them (`commit_links: … on no branch`) and `lore link remap --pair <old>=<new>` moves one.
- **A missing id names the commit that has it.** `lore <kind> show --id <id>` for a row whose commit is on no branch any more, but still in this clone's reflog, answers with `git cherry-pick <commit>`.

lore's own backups are local refs, never pushed (`lore where` lists them): `refs/lore-backup/migrate/<date>` is the state before `lore migrate run` (put it back with `lore migrate rollback`), `refs/lore-backup/live/<stamp>` is your live ref before a live compaction or a rewritten remote live ref (put it back with `lore live restore`), and `refs/lore/backup/epoch-<n>-<sha>` in a store clone is that clone's history before it adopted a compacted store. None of them covers a force-push of your branch: there git's reflog is the backup (`git reflog`, then `git cherry-pick <commit>`).

## Pitfalls

- **lore never changes `core.hooksPath`** (the git setting that says which folder holds the hooks). When a hook manager owns the hooks folder, lore adds its block to the scripts there. Its block goes right after the first line, so an early `exit 0` in your own script cannot skip it.
- **A global `core.hooksPath` folder runs lore only where lore is set up.** When `core.hooksPath` comes from your global or system git config, that one folder holds the hooks of every repo on the machine. lore still adds its blocks there (the `set up git` line says the folder is shared), but each line starts with a check: it runs lore only when the repo has a `.lore/` folder or lore wired that clone (`.git/lore/git-wiring`). In any other repo the blocks do nothing: a commit there starts no lore, adds no git config key, fetches no `refs/lore` and writes no file.
- **Upgrading from v0.1.17: update every `lore` on your PATH.** After `lore migrate run` the row files are in the 0.1.18 format. If an old v0.1.17 `lore` is still first on your PATH (an old Homebrew or `~/.local/bin` copy, or a second machine), every `git commit` in that repo that changes `.lore/data` is refused with `lore: .lore/data holds lore 0.1.18 rows but the lore on PATH is older` when the hook block is the one this lore wrote. A clone where lore 0.1.17 itself ran before the migration keeps 0.1.17's own hook until this lore runs there, and that hook refuses every commit (code too) with one `newer-format … upgrade lore` line per row and `refusing to commit broken lore files`, ending in an unrelated hint about removing a credential; `git commit --no-verify` gets past it until the lore on PATH is upgraded. Run `which lore` and `lore version` and upgrade or remove the old copy; the merge driver calls the same `lore` on PATH (or the full path it was set up with), so upgrade that one too.
- **A fresh clone has no hooks until lore runs.** git does not copy hooks when you clone. Run `lore sync` once; until then, a merge in that clone uses plain line merging.
- **`LORE_SYNC_GIT=0` leaves git alone.** With it set, lore still syncs files but never adds hooks, the driver or `.gitattributes` lines. Use it on build machines where you do not want lore to change the clone.
- **The hint after a refused commit talks about markers even for broken JSON.** If the message says "invalid JSON", the file is not valid JSON at all: restore it from git (`git checkout HEAD -- <file>`) or fix the text.
- **Removing lore's block by hand is undone.** lore checks the set-up after every command and puts the block back. To stop it, set `LORE_SYNC_GIT=0`.
- **Missing blocks turn the leak guard off, and lore says so.** When the `commit-msg`, `pre-push` or `pre-commit` block is gone from a clone lore set up before (removed by hand, or a hook manager took the hooks over with `core.hooksPath` or its own `pre-commit` script), `lore doctor` lists first `leak guard off in this clone: lore's hook blocks are missing in <folder> (…), so commits are not checked for secrets …; run `lore fix` to restore them`, and the next lore command says it once as well. Both also scan the commits no remote has yet, as the pre-push check would, and name a secret-looking value in them with its `lore secrets allow` line: rewrite that commit (`git commit --amend`, or `git rebase --interactive`) before you push.

## Related

- [Conflicts and duplicates](conflicts-and-duplicates.md): the merge driver at work.
- [The GitHub workflow](github-workflow.md): merging on pull requests, where hooks and drivers do not run.
- [Lore for teams › commands › lore check leaks](../team/commands.md#lore-check-leaks)
- [Feature index › Git sync](../reference/features.md#git-sync-notes-travel-with-your-commits)
