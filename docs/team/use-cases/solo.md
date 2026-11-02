# Solo developer

## The situation

One person, one repo. You want lore exactly as before: rules, notes and tasks in `.lore/data`, committed with your code. No company, no store, no teammates.

## The config

None. Without `~/.lore/config.yaml` there is no org, and your identity comes from your git email.

## The commands

In a repo without a remote, nothing is ever pushed. Tasks are still live by default, so they sit on a local `refs/lore/live` until they land:

<!-- example: solo#no-remote -->
```console
# alice, in scratch
$ lore init --non-interactive --name=scratch >/dev/null 2>&1 && lore rule add --body 'use pnpm, never npm'
✓ rul_01a1…3658 rul_01a1…3658 
  scope: [master] severity: must
lore sync: exported 1
lore sync: 4 file(s) in .lore/data not committed — commit them with your change
$ lore task add 'write the README'
✓ tsk_01a1…5d7e tsk_01a1…5d7e — write the README [medium]
lore live: (live, local: no remote)
$ lore task done tsk_01a1…5d7e
✓ tsk_01a1…5d7e done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: (live, local: no remote)
$ lore land --all --dry-run
would land 2 row(s) to main (_root/):
  tlt_01a1…1d5a     land  named by lore land
  tsk_01a1…5d7e     land  named by lore land
$ git status --short
?? .gitattributes
?? .gitignore
?? .lore/
```
<!-- /example -->

Once the repo has a remote, the same task is pushed to `refs/lore/live` on the remote and lands on `main` when it is done; see [Getting started](../getting-started.md#4-your-first-live-task).

If you prefer tasks to travel with your branch like every other row, turn live off for tasks:

```yaml
# ~/.lore/config.yaml
live:
  defaults: {tasks: false}
```

<!-- example: solo#tasks-with-the-code -->
```console
# alice, in scratch
$ lore task add 'tidy the scripts folder'
✓ tsk_01a1…3946 tsk_01a1…3946 — tidy the scripts folder [medium]
lore sync: exported 1
lore sync: 5 file(s) in .lore/data not committed — commit them with your change
$ git status --short
?? .gitattributes
?? .gitignore
?? .lore/
```
<!-- /example -->

The task is now a file in `.lore/data` on your branch ("not committed — commit them with your change"), exactly as lore worked before.

## What happened

```text
 default (tasks live)                       live.defaults.tasks: false
 task add  ──▶ refs/lore/live             task add  ──▶ .lore/data/tasks/… on your branch
 task done ──▶ lands on main              git commit / merge ──▶ main
 (no remote: stays local until one exists)
```

## Gotchas

- With no remote, live rows say `(live, local: no remote)` and stay on this machine; `lore land --all --dry-run` shows what would land once there is a remote `main` to land on.
- Rules, decisions and memories were never live by default; they still travel with your branch unless you pass `--live`.
- `lore whoami` shows `org none` and `config … does not exist (no orgs configured)`. That is the expected state, not an error.
