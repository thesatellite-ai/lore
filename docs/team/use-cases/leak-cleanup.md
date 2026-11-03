# Cleaning up after a leak

**In short:** Alice (an agency developer) wrote a private task into the client's repo by mistake, and the client could see it. This page removes it from what the client's GitHub repo serves. It also explains what cannot be undone: copies someone already downloaded.

## The situation

Alice meant to add an internal task, but typed `--to repo`. So "Chase the client about the unpaid March invoice" went to the client's own repo. Tasks are live: they go to a hidden list on GitHub that is not a branch (`refs/lore/live`), and everyone sees them at once. So it was pushed to `github.com/client/website` at once, and Carol (the client's developer) can already see it.

Nothing checks that text. It did not match any internal note, and `--to repo` is a deliberate choice. Hooks (the checks git runs before a commit or push) cannot stop this kind of mistake. So the cleanup steps matter.

## The config

The [agency setup](agency-client-repo.md#the-config).

## The commands

The mistake, and what the client sees:

<!-- example: leak-cleanup#the-mistake -->
```console
# alice, in website
$ lore task add 'chase the client about the unpaid March invoice' --to repo
✓ tsk_7a67…9504 — chase the client about the unpaid March invoice [medium]
lore live: (live, pushed)

# carol, in app
$ lore sync >/dev/null; lore task list
○ tsk_7a67…9504 chase the client about the unpaid March invoice [medium]
```
<!-- /example -->

The cleanup has two steps. `lore live purge` removes the row from live and writes its id to a purge list on the live ref. So every clone drops it on its next fetch, and no clone can push it back. `lore live compact` then rewrites the live ref to a single commit. So the row is gone from the ref's history too:

<!-- example: leak-cleanup#purge -->
```console
# alice, in website
$ lore live purge tsk_7a67…9504
✓ purged tsk_7a67…9504 from live; run `lore live compact` to drop them from the ref's history too
lore live: (live, pushed)
$ lore live compact --dry-run
refs/lore/live has 2 commits (threshold 1000); compacting would keep 5 files, epoch 1 → 2
$ lore live compact
✓ compacted refs/lore/live to 1 commit (epoch 2); backup kept locally as refs/lore-backup/live/20261102T100025
$ lore live log
epoch 2
601d285 2026-11-02 Alice Doe  lore: live compact (epoch 2)

# carol, in app
$ lore sync >/dev/null; lore task list
(no tasks)

# newdev, in ~
$ git clone -q --mirror https://github.com/client/website app.git && cd app.git && git for-each-ref --format='%(refname)' && for c in $(git rev-list --all); do git grep -q -i 'unpaid March' $c && echo "found in $c"; done; echo 'searched every commit the remote serves'
refs/heads/main
refs/lore/live
searched every commit the remote serves
```
<!-- /example -->

Carol no longer sees it. A fresh mirror of everything the remote serves (every branch and every `refs/lore/*` ref) holds no trace of the text.

## The runbook

1. **`lore live purge <id>`**: removes the row from the live ref for good. Purge entries are kept for 30 days. A clone that has not run lore since then could push the row back; `lore doctor` reports that.
2. **`lore live compact`**: drops the row from the live ref's history. Your own previous live ref is kept locally as `refs/lore-backup/live/<stamp>`.
3. **If it landed on `main`** (or rode along in a merged pull request), it is an ordinary git history problem. Revert it, and rewrite history if the text must be gone.
4. **If it contained a secret**, rotate the secret. A pushed secret must be treated as known.
5. **Copies already fetched stay.** Anyone who fetched before the purge (Carol did) still has the data in their clone, exactly as with any pushed commit. Lore cannot reach other people's machines.

## What happened

```text
 refs/lore/live (client/website)
   before:  A ─ B (adds the task)              everyone who fetched has B
   purge:   A ─ B ─ C (task removed, id on the purge list)
   compact: Z (one commit, epoch 2, no task, no history)   backup on Alice's machine only
```

## Pitfalls

- **Prevent it next time:** in a client repo, leave `--to` out for internal kinds. They go to the store (the agency's private notes repo) by default. Use `--to repo` only when the client should read the row.
- **Text pasted into a file is checked twice:** at commit (the `pre-commit` and `commit-msg` hooks) and at push (the `pre-push` hook, which checks every commit the push would send and refuses when the store cannot be read). `git commit --no-verify` and `git push --no-verify` skip every local check. Run `lore check leaks --ci` in the client's CI as the backstop; it catches internal row ids, not pasted sentences.
- **`lore live restore`** puts live rows back after someone deleted or rewrote the remote live ref. It never re-pushes a purged id or a row already on `origin/main`.
- **End of an engagement:** the client may ask for its data to be removed from the agency's store. Then that store's folder must be purged, or its history rewritten. With one store per client, you just delete one repo.
