# The audit log: who changed what

**In short:** lore writes down every change to your shared notes in a log that cannot be edited without it showing. `lore audit log` lists the changes, newest first. `lore audit verify` checks that nobody changed the log, or your notes, behind lore's back.

## What it is and when you want it

Every time a rule, memory, decision, task or other shared row changes, lore adds one line to the audit log: when, what kind of change, which row, and who did it. Each line also carries a fingerprint of the line before it (a hash chain), like numbered pages in a bound book: tearing a page out or rewriting one breaks the numbering, and `lore audit verify` notices.

lore also keeps a fingerprint of each shared row. If someone edits the database file with another tool (for example a SQLite editor), the row's fingerprint no longer matches and `verify` reports it.

You want this when:

- a rule suddenly says something nobody remembers writing,
- you want to know who changed a note and when,
- you suspect a script or tool edited `.lore/lore.db` directly.

## Before you start

- lore is set up in the repo.
- The log lives in the database on your machine. It is not shared through git: each machine has its own log of what happened on it, including the notes it took in from teammates.

## Step 1 — See recent changes

Alice (an agency developer) adds a rule, then reads the log. Each line is: time, table and kind of change, the row, and the actor id of whoever made it (an actor is a person or agent; the id is lore's name for them):

<!-- example: ops#audit-log -->
```console
# alice, in website
$ lore rule add --body 'Never put passwords in the code'
✓ rul_2ad3…f891
  scope: [master] severity: must
$ lore audit log
2026-11-02T10:00:00Z  rules.write                  rules/rul_2ad3…f891  by act_7a67…9504
2026-11-02T10:00:00Z  memories.write               memories/mem_162d…9666  by act_7a67…9504
2026-11-02T10:00:00Z  rules.write                  rules/rul_54fe…28f2  by act_7a67…9504
2026-11-02T10:00:00Z  repos.import                 repos/rep_05e9…4603  by act_7a67…9504
2026-11-02T10:00:00Z  projects.import              projects/prj_c0f3…b5b8  by act_7a67…9504
2026-11-02T10:00:00Z  actors.import                actors/act_7a67…9504  by act_7a67…9504
2026-11-02T10:00:00Z  repos.write                  repos/rep_05e9…4603  by act_7a67…9504
2026-11-02T10:00:00Z  policies.write               policies/pol_76bd…3f10  by act_7a67…9504
2026-11-02T10:00:00Z  projects.write               projects/prj_c0f3…b5b8  by act_7a67…9504
2026-11-02T10:00:00Z  actors.write                 actors/act_7a67…9504  by act_7a67…9504
$ lore audit verify
✓ audit chain intact (10 entries)
```
<!-- /example -->

`--limit` changes how many lines you see (50 by default, `0` for all); `--json` gives the same lines for scripts.

## Step 2 — Catch a change made outside lore

Now someone changes the first rule's text straight in the database file, without lore. `lore audit verify` finds it and exits 1:

<!-- example: ops#audit-tamper only=1 -->
```console
# alice, in website
$ lore audit verify
✓ audit chain intact (10 entries)
✗ hash mismatch: rules/rul_54fe…28f2 changed outside lore
ERROR: [E_AUDIT_CHAIN_BROKEN] hash mismatch: 1 row(s) changed outside lore
  hint: inspect them (`lore <kind> show <id>`); the next lore command records them as external writes
(exit 23)
```
<!-- /example -->

Run `verify` **first** when you investigate. Any other lore command notices the change too, accepts it and records it in the log as an `external-write`, so it stays visible but `verify` passes again:

<!-- example: ops#audit-tamper only=2,3,4 -->
```console
# alice, in website
$ lore rule list
lore sync: rules/rul_54fe…28f2.json: lore.db change not written (this table's files are owned by the 0.1.18 core; change it with the core's commands, e.g. `lore memory`)
rul_54fe…28f2 [must/always] Run the tests before you push
rul_2ad3…f891 [must/always] Never put passwords in the code
$ lore sync
· already in sync
$ lore audit log --limit 2
2026-11-02T10:00:00Z  rules.external-write         rules/rul_54fe…28f2  by act_7a67…9504
2026-11-02T10:00:00Z  rules.write                  rules/rul_2ad3…f891  by act_7a67…9504
```
<!-- /example -->

The rule now says "Pushing without tests is fine". The `external-write` line tells you it was not changed by a lore command. Fix the text with `lore rule edit`, and find out which tool touched the file.

## Try it

Run `lore audit verify` in any repo with lore. Exit 0 means the log is whole and every row matches what lore last wrote:

<!-- example: ops#audit-log only=3 -->
```console
# alice, in website
$ lore audit verify
✓ audit chain intact (10 entries)
```
<!-- /example -->

Exit 1 comes with `E_AUDIT_CHAIN_BROKEN` and a hint, as in Step 2.

## Pitfalls

- **The log is per machine.** A teammate's log shows their own changes and what their lore read in from git, not yours. It cannot be merged, so it is never committed.
- **Only `verify` stops on an outside edit.** Every other command records it and carries on. If you want to catch such edits, run `lore audit verify` before anything else (for example at the start of a CI job, see [Use lore in CI](ci.md)).
- **A rebuild starts a new log.** When you rebuild the database from `.lore/data` (see [Back up, restore and repair](backup-restore-repair.md#step-5--rebuild-from-the-files-in-git)), the old log is not carried over. Keep a backup if you need the history.
- **The times in the log are the real clock time,** even when you run lore with a fixed test clock.
- **The actor is an id (`act_…`).** `lore whoami` shows your own; for teams, [the roster](../team/concepts.md) maps ids to people and agents.

## Related

- [Check lore's health with `lore doctor`](doctor.md)
- [Back up, restore and repair](backup-restore-repair.md)
- [Secrets and personal data](secrets-and-pii.md)
- [Operations index](README.md)
