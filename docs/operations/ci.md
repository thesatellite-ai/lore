# Use lore in CI

**In short:** In CI (the machine that builds and tests your code after every push), you usually want lore to read, never write. Turn on read-only mode, hide the "run lore setup" hint, and use `lore doctor`'s exit code to stop the job when something is wrong.

## What it is and when you want it

CI runs your checks on a fresh machine with a fresh clone of the repo. A lore command there should never change notes: nobody reviews what CI writes, and two CI jobs at once would fight over the database. lore has three tools for this:

- <!-- ds:block id=env-read-only-syjpe918 --> **Read-only mode** (`LORE_READ_ONLY=1` or `--read-only`): lore opens the database without taking its write lock (the "one writer at a time" sign on the door) and refuses every command that would change something.
- <!-- ds:block id=env-no-stale-warn-ukunyu8f --> **`LORE_NO_STALE_WARN=1`**: hides the hint lore prints when its search index (the lookup table that makes search fast) is older than the lore version, so CI logs stay quiet.
- <!-- ds:block id=doctor-statuses-y6sro1m4 --> **`lore doctor` exit codes**: 0 healthy, 1 degraded, 2 broken (see [doctor](doctor.md#step-2--read-the-exit-code)).

## Before you start

- The repo has lore set up and `.lore/data` committed. On a fresh clone the first lore command builds `.lore/lore.db` from those files by itself.
- The CI machine has the `lore` binary on its `PATH` (install it the same way as on your laptop).

## Step 1 — Make every write fail

With `LORE_READ_ONLY=1` set, reading works and writing stops with `E_READ_ONLY`. The `--read-only` flag does the same for one command:

<!-- example: ops#read-only -->
```console
# alice, in website
$ lore rule add --body 'Buttons are blue'
ERROR: [E_READ_ONLY] command requires write access but read-only mode is active
  hint: unset --read-only and LORE_READ_ONLY
(exit 23)
$ lore rule list
rul_54fe…28f2 [must/always] Run the tests before you push
rul_2ad3…f891 [must/always] Never put passwords in the code
$ lore rule add --read-only --body 'Buttons are blue'
ERROR: [E_READ_ONLY] command requires write access but read-only mode is active
  hint: unset --read-only and LORE_READ_ONLY
(exit 23)
```
<!-- /example -->

Set the variable once for the whole job (in most CI systems: an `env:` entry at the top of the job).

## Step 2 — Quiet the setup hint

After an upgrade, lore warns on every command that opens its local database (`lore.db`) until someone runs `lore setup` to rebuild the search index; commands that only read the shared files, such as `lore rule list`, stay quiet. In CI that line is noise. `LORE_NO_STALE_WARN=1` hides it; `lore setup` fixes the cause:

<!-- example: ops#stale-warning -->
```console
# alice, in website
$ lore session list
⚠ search index is out of date
  run `lore setup` to rebuild
(no sessions)
$ lore session list
(no sessions)
$ lore setup
⚠ search index is out of date
  run `lore setup` to rebuild
→ running ent schema migration...
→ building FTS5 search index...
✓ setup complete (registry fingerprint: 601d2850ffd047c2)
$ lore session list
(no sessions)
```
<!-- /example -->

## Step 3 — Look at the fresh clone

A CI clone has the `.lore/data` files but not the local database, and not lore's git settings. The first lore command builds the database from the files, even in read-only mode (the database is only a local copy; no note changes), and wires git in the clone: lore's merge helper, the `.gitattributes` check and its hook blocks. That wiring runs after every command in a git checkout, read-only ones included, because it writes only git's own config and hook files, never your notes. There is no separate install step:

<!-- example: ops#ci only=1,2 keep=lore|rul_|Status|merge|exit -->
```console
# alice, in ci-clone
$ lore rule list
rul_54fe…28f2 [must/always] Run the tests before you push
rul_2ad3…f891 [must/always] Never put passwords in the code
$ lore doctor; echo "doctor exit $?"
Status: HEALTHY
  …
doctor exit 0
```
<!-- /example -->

## Step 4 — Fail the job when lore is not healthy

Check the audit log, save the JSON report and act on the exit code. Read `status` from the JSON, not from the text output, because the JSON shape is fixed:

<!-- example: ops#ci only=3,4,5 -->
```console
# alice, in ci-clone
$ lore audit verify
✓ audit chain intact (0 entries)
$ lore doctor --json > doctor.json; echo "doctor exit $?"
doctor exit 0
$ grep '"status"' doctor.json
  "status": "healthy",
```
<!-- /example -->

A complete CI step, as a shell script:

```sh
export LORE_READ_ONLY=1 LORE_NO_STALE_WARN=1
lore audit verify                   # stops on a broken audit chain or an edit made outside lore
lore doctor --json > doctor.json
case $? in
  0) echo "lore is healthy" ;;
  1) echo "lore needs attention:"; cat doctor.json ;;  # degraded: warn, or exit 1 to be strict
  *) echo "lore is broken"; cat doctor.json; exit 1 ;;
esac
```

## Step 5 — Give tools the notes they cannot import

Some tools read one context file and cannot follow an import line to other files. `lore context --internal` prints the internal, personal and global notes that apply in this repo, as one block of text you can pass on. In a repo with no such notes it says so:

<!-- example: ops#ci only=6 -->
```console
# alice, in ci-clone
$ lore context --internal
<!-- generated by lore; internal: never quote this content in client files, commits, PRs or chat -->
(nothing internal or personal loads in this repo)
```
<!-- /example -->

`--table rules` limits it to one kind of row, `--search <text>` to rows that contain a word, and `--json` gives the same rows for scripts. Never paste this output into anything the client can read (the first line says so too); see [Lore for teams](../team/README.md).

## Pitfalls

- **Only `1` turns a switch on.** `LORE_READ_ONLY=true` or `yes` is ignored and lore writes as usual. The same holds for `LORE_NO_STALE_WARN` and the `LORE_ALLOW_*` switches.
- **Read-only still builds the local database.** On a fresh clone the first command builds `.lore/lore.db` from `.lore/data` even with `LORE_READ_ONLY=1`. That is a local copy, not a change to your notes; nothing is written to `.lore/data` or git.
- **There is no `lore sync install-git` in 0.1.18.** Older CI scripts ran it before anything else; drop that line. Any lore command, read-only ones included, wires git in the clone (Step 3). If doctor still says DEGRADED, decide on purpose whether exit 1 fails your job.
- **Do not run `lore setup` in read-only mode.** It is a write and is refused; run it once on a normal machine and commit.

## Related

- [Check lore's health with `lore doctor`](doctor.md)
- [The audit log](audit-log.md)
- [Secrets and personal data](secrets-and-pii.md#safety-switches)
- [Git sync on GitHub](../sync/github-workflow.md)
- [Environment variables](../reference/environment.md)
- [Operations index](README.md)
