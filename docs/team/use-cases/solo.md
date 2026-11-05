# Solo developer

**In short:** you work alone in one repo and want no company setup at all. Lore works as before. Your rules, notes and tasks are files in `.lore/data`, saved with your code.

## The situation

One person, one repo. You want lore exactly as before: rules, notes and tasks in `.lore/data`, committed with your code. No company, no store (separate private notes repo), no teammates.

## The config

None. Without `~/.lore/config.yaml` there is no org (company setup). Your identity comes from your git email.

## The commands

In a repo without a remote, nothing is ever pushed. Tasks are still live by default. Live means they go to a hidden list that is not a branch (the hidden ref `refs/lore/live`). Without a remote that list stays on your machine until the tasks land (are written to `main`'s `.lore/data`):

<!-- example: solo#no-remote -->
```console
# alice, in scratch
$ lore init --non-interactive --name=scratch >/dev/null 2>&1 && lore rule add --body 'use pnpm, never npm'
lore sync: imported 3
✓ rul_7a67…9504
  scope: [master] severity: must
$ lore task add 'write the README'
✓ tsk_2ad3…f891 — write the README [medium]
lore live: (live, local: no remote)
$ lore task done tsk_2ad3…f891
✓ tsk_2ad3…f891 done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: (live, local: no remote)
$ lore land --all --dry-run
would land 1 row(s) to main (_root/):
  tsk_2ad3…f891     land  named by lore land
$ git status --short
?? .gitattributes
?? .gitignore
?? .lore/
?? AGENTS.md
```
<!-- /example -->

Once the repo has a remote, the same task is pushed to `refs/lore/live` on the remote. It lands on `main` when it is done. See [Getting started](../getting-started.md#4-your-first-live-task).

Do you want tasks to travel with your branch like every other row? Then turn live off for tasks:

```yaml
# ~/.lore/config.yaml
live:
  defaults: {tasks: false}
```

<!-- example: solo#tasks-with-the-code -->
```console
# alice, in scratch
$ lore task add 'tidy the scripts folder'
✓ tsk_162d…9666 — tidy the scripts folder [medium]
$ git status --short
?? .gitattributes
?? .gitignore
?? .lore/
?? AGENTS.md
```
<!-- /example -->

The task is now a file in `.lore/data` on your branch ("not committed — commit them with your change"). That is exactly how lore worked before.

## What happened

```text
 default (tasks live)                       live.defaults.tasks: false
 task add  ──▶ refs/lore/live             task add  ──▶ .lore/data/tasks/… on your branch
 task done ──▶ lands on main              git commit / merge ──▶ main
 (no remote: stays local until one exists)
```

## Pitfalls

- With no remote, live rows say `(live, local: no remote)` and stay on this machine. `lore land --all --dry-run` shows what would land once there is a remote `main` to land on.
- Rules, decisions and memories were never live by default. They still travel with your branch unless you pass `--live`.
- `lore whoami` shows `org none` and `config … does not exist (no orgs configured)`. That is the expected state, not an error.
