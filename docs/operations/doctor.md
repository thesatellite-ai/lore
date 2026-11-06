# Check lore's health with `lore doctor`

**In short:** `lore doctor` is a quick check-up for lore in one repo, like the warning lights on a car dashboard. It tells you if the local cache is fine, if your notes are in step with git, and who lore thinks you are. It ends with an exit code (0, 1 or 2) so a script can act on it.

## What it is and when you want it

lore keeps your shared notes as plain files under `.lore/data`, so they travel with your git commits, and builds a local cache from them, `.lore/lore-cache.db` (a database here is just one file that makes reads and search fast). What lore keeps for your machine only (the audit log, sessions) is in `~/.lore/state/machine.db`.

`lore doctor` looks at all of that and gives one answer: **HEALTHY**, **DEGRADED** (it works, but something needs your attention) or **BROKEN** (lore cannot read its notes or stores). Run it:

- when a lore command prints an error you do not understand,
- after you upgrade lore or rebuild the cache,
- in a script or CI job that should stop when lore is not healthy (see [Use lore in CI](ci.md)).

It only reads. It never changes your notes.

## Before you start

- lore is installed and set up in the repo (`lore init`, see [Your first project](../knowledge/first-project.md)).
- Run the commands from inside the repo.

## Step 1 — Run the check

Run `lore doctor` in the repo. It prints one block per area it checked:

<!-- example: ops#doctor only=1 -->
```console
# alice, in website
$ lore doctor
Status: HEALTHY
Binary: dev
[db]
  ok:       true
  wal_size: 0 bytes
[identity]
  resolved: human:alice@agency.example
  source:   git config user.email
  stable:   yes
Notes:
  - live: tracking refspec installed, not forced into refs/lore/*
  - live: last fetch 5s ago
  - live: post-commit and pre-push hook blocks installed
  - Team versions (newest save per writer and machine):
  -   alice on (code repo, no machine id): lore dev (last save 2026-11-02)
Context:
  - context: .lore/LORE.md not rendered yet (lore render)
```
<!-- /example -->

What each block means:

| Block | What it checks | What "good" looks like |
|---|---|---|
| `[db]` | The database file opens and passes SQLite's own quick check. `wal_size` is the size of the database's side journal (a scratch file SQLite writes before it folds changes into the main file). | `ok: true`. A journal over 100 MB is a warning. |
| `[sync]` | The plain files in `.lore/data` and the database agree: nothing waiting to be written out, no conflicts kept aside, no broken files, the git merge helper is installed. | All counts 0, `merge driver: true`. `uncommitted` is only information: it counts files you have not committed yet. |
| `[identity]` | Who lore thinks you are, and where that came from (here: your git email). | `stable: yes`. A made-up, one-time identity is a warning, because your later sessions would not be linked to this one. |
| `Notes` | Facts that are fine, for example that lore's git hooks are installed. | Nothing to do. |
| `Warnings` / `Errors` | What makes the status DEGRADED or BROKEN, each with the command that fixes it. | Absent. |

## Step 2 — Read the exit code

The exit code is the number a command hands back to the shell when it ends. `lore doctor` uses three, and they stay the same between versions so monitoring scripts can rely on them:

<!-- ds:block id=doctor-statuses-y6sro1m4 -->
| Exit code | Status | Meaning |
|---|---|---|
| 0 | HEALTHY | Everything checked is fine. |
| 1 | DEGRADED | lore works, but a warning needs your attention (for example a sync conflict kept aside). |
| 2 | BROKEN | lore cannot use the database (for example the file is damaged). |

In a shell, `echo $?` right after the command prints it.

## Step 3 — Get the same answer as JSON

`--json` prints the report in a fixed, machine-readable shape. Scripts should read `status` (`healthy`, `degraded` or `broken`) instead of parsing the text:

<!-- example: ops#doctor only=2 -->
```console
# alice, in website
$ lore doctor --json | head -8
{
  "schema_version": 1,
  "ok": true,
  "status": "healthy",
  "db_ok": true,
  "wal_size_bytes": 0,
  "identity": {
    "resolved": "human:alice@agency.example",
```
<!-- /example -->

## Step 4 — Repair what doctor can fix

`--fix` — repair what doctor can fix safely: reversed `--supersedes` links written by older versions, and a task lead missing from the task's assignees. Safe to re-run.

Doctor lists those links in its report, then `lore doctor --fix` turns each one around so the old row records the row that replaced it. A second `--fix` finds nothing left to change.

## Try it: a damaged cache does not make lore BROKEN

The cache is rebuilt from `.lore/data` whenever it is damaged or missing, so a damaged cache never leaves lore BROKEN: the next command moves it aside, rebuilds it and doctor reports HEALTHY:

<!-- example: ops#damaged-database only=2 -->
```console
# alice, in website
$ lore doctor | grep '^Status'
Status: HEALTHY
```
<!-- /example -->

When a sync conflict is kept aside doctor says DEGRADED (exit 1) and names `lore sync conflicts`; BROKEN (exit 2) means lore cannot read its notes at all, for example a row file under `.lore/data` that is not valid JSON. The full story of a cache rebuild is in [A damaged cache](backup-restore-repair.md).

## Look up an error code

Every lore error starts with a code such as `E_NOT_FOUND`. [Error codes](../reference/errors.md) explains each one and what to do; the generated [command and error reference](../reference/cli-commands.md#errors) lists every code with its exit status. Add `--json` to a failing command to get the code, exit status and hint as fields.

The [error reference](../reference/errors.md) explains each code and its fix.

## Send a support bundle with a bug report

When you report a problem, attach a support bundle: one JSON file with the doctor report, lore's version, your system type and how many rows of each kind you have. By default it holds **no note text** at all, so it is safe to share:

<!-- example: ops#support-bundle -->
```console
# alice, in website
$ lore support-bundle --out bundle.json
✓ wrote support bundle to bundle.json (1376 bytes)
$ grep -E '"(binary_version|status|rules|memories|sanitized)"' bundle.json
  "binary_version": "dev",
    "binary_version": "dev",
    "status": "healthy",
    "memories": 1,
    "rules": 2,
  "sanitized": true,
```
<!-- /example -->

Without `--out` the bundle is printed to the screen. Read it before you send it; it does contain your identity (your email) and the doctor notes.

## Pitfalls

- **DEGRADED is not an error message.** Many things only degrade the status: a sync conflict kept aside, an uncommitted secret warning, a large journal. Read the `Warnings` block; each line names the command that clears it.
- **`uncommitted` does not count against you.** Files in `.lore/data` that you have not committed yet are listed, not counted as a problem. Commit them with your change.
- **A damaged cache is not reported, it is fixed.** Any lore command that finds `.lore/lore-cache.db` damaged moves it into `.lore/backups/` and rebuilds it, printing one line that says so; doctor then reports HEALTHY. A damaged row file under `.lore/data` is different: that is your notes, so doctor reports it and nothing is rebuilt over it.
- **`--include-content` on the support bundle adds nothing today.** The flag only switches `sanitized` to `false`; no note text is added. Do not rely on it to share note text.
- **The support bundle's `generated_at` is the real time,** even when you run lore with a test clock.
- **Doctor checks one repo.** Run it in each repo you care about. Team checks (stores, live rows) show up in the same report when you use them; see [Troubleshooting for teams](../team/troubleshooting.md).

## Related

- [A damaged cache](backup-restore-repair.md)
- [Use lore in CI](ci.md)
- [The audit log](audit-log.md)
- [Error reference](../reference/errors.md)
- [Troubleshooting for teams](../team/troubleshooting.md)
- [Operations index](README.md)
