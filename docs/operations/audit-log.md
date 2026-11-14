# The audit log: who changed what

**In short:** lore writes down every change it makes to your notes in a log on your machine that cannot be edited without it showing. `lore audit log` lists the changes, newest first. `lore audit verify` checks that nobody changed the log behind lore's back.

## What it is and when you want it

Every time a rule, memory, decision, task or other row changes through lore, lore adds one line to the audit log: when, what kind of change, which row, and who did it. Each line also carries a fingerprint of the line before it (a hash chain), like numbered pages in a bound book: tearing a page out or rewriting one breaks the numbering, and `lore audit verify` notices.

The log records what lore did. The notes themselves are the files in `.lore/data`, and git is their record: an edit to one of those files, by lore or by hand, shows up in `git status` and `git log -p .lore/data`, not in the audit log.

You want this when:

- you want to know who changed a note through lore and when,
- you want proof that this machine's log was not edited afterwards.

## Before you start

- lore is set up in the repo.
- The log lives on your machine only, in `~/.lore/state/machine.db` (the `audit_chain` table). It is never shared through git: each machine has its own log of what lore did on it.

## Step 1 — See recent changes

Alice (an agency developer) adds a rule, then reads the log. Each line is: time, table and kind of change, the row, and the actor id of whoever made it (an actor is a person or agent; the id is lore's name for them):

<!-- example: ops#audit-log -->
```console
# alice, in website
$ lore rule add --body 'Never put passwords in the code'
lore: wrote ~/work/website/.lore/data/rules/rul_7a67…9504.json
✓ rul_7a67…9504
  scope: [project] severity: must
$ lore audit log
2026-11-02T10:00:00Z  rules.write                  rules/rul_7a67…9504  by alice
2026-11-02T10:00:00Z  memories.write               memories/mem_2ad3…f891  by alice
2026-11-02T10:00:00Z  rules.write                  rules/rul_162d…9666  by alice
2026-11-02T10:00:00Z  policies.write               policies/pol_54fe…28f2  by alice
2026-11-02T10:00:00Z  repos.write                  repos/rep_05e9…4603  by alice
2026-11-02T10:00:00Z  projects.write               projects/prj_c0f3…b5b8  by alice
2026-11-02T10:00:00Z  actors.write                 actors/act_76bd…3f10  by alice
$ lore audit verify
✓ audit chain intact (7 entries)
```
<!-- /example -->

`--limit` changes how many lines you see (50 by default, `0` for all); `--json` gives the same lines for scripts.

## Step 2 — Catch a change to the log

Alice keeps a copy of `~/.lore/state/machine.db`, then someone changes one line of the log with a SQLite tool, so the memory's write now reads as a delete. `lore audit verify` finds it and exits 23:

<!-- example: ops#audit-tamper only=1 -->
```console
# alice, in website
$ lore audit verify
ERROR: [E_AUDIT_CHAIN_BROKEN] audit chain broken at aud_d431…313a (an entry was edited or deleted)
  hint: the chain is the audit_chain table of ~/.lore/state/machine.db; compare it with a backup of that file to investigate
(exit 23)
```
<!-- /example -->

The hint names the file. Compare it with a copy you kept; here Alice puts her copy back and the chain is whole again:

<!-- example: ops#audit-tamper only=2,3 -->
```console
# alice, in ~
$ cp ~/machine.db.copy ~/.lore/state/machine.db && rm ~/machine.db.copy

# alice, in website
$ lore audit verify
✓ audit chain intact (7 entries)
```
<!-- /example -->

## Step 3 — An edited note file is git's business

A row file under `.lore/data` is the note itself, so changing it by hand is an ordinary file change: lore reads the new text, `git status` shows the file, and the audit log stays as it was, because lore did not make that change. Review such edits like any other change in the repo, and undo one with git:

<!-- example: ops#row-file-edit -->
```console
# alice, in website
$ sed -i 's/Run the tests before you push/Pushing without tests is fine/' .lore/data/rules/*.json && lore rule list
lore: picked up a hand edit of rules rul_162d…9666 (body): checked and stamped with your name and the time in ~/work/website/.lore/data/rules/rul_162d…9666.json; commit it with your code
rul_162d…9666 [must/always] Pushing without tests is fine
rul_7a67…9504 [must/always] Never put passwords in the code
$ git status --short .lore/data
 M .lore/data/rules/rul_162d…9666.json
?? .lore/data/rules/rul_7a67…9504.json
$ lore audit verify
✓ audit chain intact (8 entries)
$ git restore .lore/data && lore rule list
rul_162d…9666 [must/always] Run the tests before you push
rul_7a67…9504 [must/always] Never put passwords in the code
```
<!-- /example -->

## Try it

Run `lore audit verify` in any repo with lore. Exit 0 means the log is whole:

<!-- example: ops#audit-log only=3 -->
```console
# alice, in website
$ lore audit verify
✓ audit chain intact (7 entries)
```
<!-- /example -->

Exit 23 comes with `E_AUDIT_CHAIN_BROKEN` and a hint, as in Step 2.

## Pitfalls

- **The log is per machine.** A teammate's log shows what lore did on their machine, not yours. It cannot be merged, so it is never committed.
- **Keep a copy if you need the history.** `~/.lore/state/machine.db` is not in any repo. Back it up with the rest of your home folder; a broken chain can only be investigated against an older copy.
- **Hand edits to `.lore/data` are not in the log.** Use `git log -p .lore/data` (and code review) to see who changed a note file outside lore.
- **The times in the log are the real clock time,** even when you run lore with a fixed test clock.
- **The actor is an id (`act_…`).** `lore whoami` shows your own; for teams, [the roster](../team/concepts.md) maps ids to people and agents.

## Related

- [Check lore's health with `lore doctor`](doctor.md)
- [A damaged cache](backup-restore-repair.md)
- [Secrets and personal data](secrets-and-pii.md)
- [Operations index](README.md)
