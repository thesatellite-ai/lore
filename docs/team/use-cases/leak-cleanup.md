# Cleaning up after a leak

## The situation

Alice meant to add an internal task, but typed `--to repo`: "chase Acme about the unpaid March invoice" went to Acme's own repo. Tasks are live, so it was pushed to `github.com/acme/app`'s `refs/lore/live` at once, and Acme's developer can already see it.

Nothing checks that text: it did not match any internal note, and `--to repo` is a deliberate choice. This is the kind of mistake hooks cannot stop, so the cleanup steps matter.

## The config

The [agency setup](agency-client-repo.md#the-config).

## The commands

The mistake, and what the client sees:

<!-- example: leak-cleanup#the-mistake -->
```console
# alice, in acme-app
$ lore task add 'chase Acme about the unpaid March invoice' --to repo
✓ tsk_7a67…9504 tsk_7a67…9504 — chase Acme about the unpaid March invoice [medium]
lore live: (live, pushed)

# acmedev, in app
$ lore sync >/dev/null; lore task list
○ tsk_7a67…9504 chase Acme about the unpaid March invoice [medium]
```
<!-- /example -->

The cleanup: `lore live purge` removes the row from live and writes its id to a purge list on the live ref, so every clone drops it on its next fetch and no clone can push it back. `lore live compact` then rewrites the live ref to a single commit, so the row is gone from the ref's history too:

<!-- example: leak-cleanup#purge -->
```console
# alice, in acme-app
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

# acmedev, in app
$ lore sync >/dev/null; lore task list
(no tasks)

# newdev, in ~
$ git clone -q --mirror https://github.com/acme/app app.git && cd app.git && git for-each-ref --format='%(refname)' && for c in $(git rev-list --all); do git grep -q -i 'unpaid March' $c && echo "found in $c"; done; echo 'searched every commit the remote serves'
refs/heads/main
refs/lore/live
searched every commit the remote serves
```
<!-- /example -->

Acme's developer no longer lists it, and a fresh mirror of everything the remote serves (every branch and every `refs/lore/*` ref) contains no trace of the text.

## The runbook

1. **`lore live purge <id>`**: removes the row from the live ref for good. Purge entries are kept for 30 days; a clone that has not run lore since then could push the row back, which `lore doctor` reports.
2. **`lore live compact`**: drops the row from the live ref's history. Your own previous live ref is kept locally as `refs/lore-backup/live/<stamp>`.
3. **If it landed on `main`** (or rode along in a merged pull request), it is an ordinary git history problem: revert it, and rewrite history if the text must be gone.
4. **If it contained a secret**, rotate the secret. A pushed secret must be treated as known.
5. **Copies already fetched stay.** Anyone who fetched before the purge (Acme's developer did) still has the objects in their clone, exactly as with any pushed commit. Lore cannot reach other people's machines.

## What happened

```text
 refs/lore/live (acme/app)
   before:  A ─ B (adds the task)              everyone who fetched has B
   purge:   A ─ B ─ C (task removed, id on the purge list)
   compact: Z (one commit, epoch 2, no task, no history)   backup on Alice's machine only
```

## Gotchas

- **Prevent it next time:** in a client repo, leave `--to` out for internal kinds (they go to the store by default), and use `--to repo` only when the client should read the row.
- **`lore live restore`** puts live rows back after someone deleted or rewrote the remote live ref; it never re-pushes a purged id or a row already on `origin/main`.
- **End of an engagement:** a client asking for its data to be removed from Northwind's store needs that store's folder purged or its history rewritten. With one store per client it is deleting one repo.
