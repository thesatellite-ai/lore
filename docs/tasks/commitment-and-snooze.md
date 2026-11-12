# Commitment and snooze

**In short:** not every task is a promise. Some are ideas for one day, some are suggestions that still need a yes, and some are real but not for this week. lore keeps those out of your normal list until you want them: `--commitment` says how sure a task is, and `--defer-until` hides it until a date.

## What it is and when you want it

Your task list is most useful when it only shows what you will really do. Two settings keep the rest out of the way:

- **Commitment** says how sure a task is. `accepted` means "we will do this" (the default for a person). `proposed` means "someone suggested this; it needs a yes", for example a task a coding agent wrote down. `someday` means "a nice idea, no promise", like a parking lot.
- **Snooze** (`--defer-until`) hides a task until a date, like putting a letter in a drawer marked "open on 1 December".

Use them when a coding agent suggests work, when you collect ideas, or when a task is real but cannot start yet.

Alice (an agency developer) works on the client's website, `github.com/client/website`.

## Before you start

- lore set up in the repo, and the basics from [Tasks](tasks.md).

## Step 1 — Add a proposed task and a someday idea

Pass `--commitment` when you add the task. Neither task shows up in `lore task list`: that list shows only accepted tasks that are not snoozed.

<!-- example: tasks#commitment -->
```console
# alice, in website
$ lore task add 'Try a dark mode for the shop' --commitment someday
lore: wrote ~/work/website/.lore/data/tasks/tsk_3b62…e913.json
✓ tsk_3b62…e913 — Try a dark mode for the shop [medium]
lore live: (live, pushed) commit 468e9a8 to origin refs/lore/live
lore live: 1 row(s) left live (main holds them)
$ lore task add 'Add a FAQ page' --commitment proposed
lore: wrote ~/work/website/.lore/data/tasks/tsk_f147…5139.json
✓ tsk_f147…5139 — Add a FAQ page [medium]
lore live: (live, pushed) commit 9ea0ca8 to origin refs/lore/live
$ lore task list
▶ tsk_162d…9666 Fix the typo on the pricing page [medium]
○ tsk_76bd…3f10 Write the welcome email [medium]
○ tsk_d431…313a Send the welcome email [medium]
○ tsk_28f8…03df Check every link on the home page [medium]
```
<!-- /example -->

## Step 2 — Look at proposals and ideas

`lore task triage` lists the proposed tasks: the ones waiting for a yes. `lore task someday` lists the ideas.

<!-- example: tasks#triage -->
```console
# alice, in website
$ lore task triage
○ tsk_f147…5139 Add a FAQ page [medium] (proposed)
$ lore task someday
○ tsk_3b62…e913 Try a dark mode for the shop [medium] (someday)
```
<!-- /example -->

## Step 3 — Say yes to a proposal

To accept a proposal, change its commitment with `lore task edit`. It now shows in the normal list, and triage is empty.

<!-- example: tasks#accept -->
```console
# alice, in website
$ lore task edit tsk_f147…5139 --commitment accepted
lore: wrote ~/work/website/.lore/data/tasks/tsk_f147…5139.json
✓ tsk_f147…5139 updated
lore live: (live, pushed) commit aba91f6 to origin refs/lore/live
$ lore task triage
(no tasks)
```
<!-- /example -->

The shorter way is `lore task accept`, which sets the commitment to `accepted` and can set `--priority` and `--mission` in the same step. To say no, `lore task decline` cancels the task with a close reason (`--reason wont_do` by default; also `duplicate`, `obsolete`, `stale`, `cancelled`) and keeps `--note` as a comment on the task, so the reason for the no is not lost:

<!-- example: tasks#accept-and-decline -->
```console
# alice, in website
$ lore task add 'Add a size guide' --commitment proposed
lore: wrote ~/work/website/.lore/data/tasks/tsk_77cb…76c4.json
✓ tsk_77cb…76c4 — Add a size guide [medium]
lore live: (live, pushed) commit d33587e to origin refs/lore/live
lore live: 1 row(s) left live (main holds them)
$ lore task add 'Rename the shop to Website Pro' --commitment proposed
lore: wrote ~/work/website/.lore/data/tasks/tsk_d191…45c2.json
✓ tsk_d191…45c2 — Rename the shop to Website Pro [medium]
lore live: (live, pushed) commit 34010c1 to origin refs/lore/live
$ lore task triage
○ tsk_77cb…76c4 Add a size guide [medium] (proposed)
○ tsk_d191…45c2 Rename the shop to Website Pro [medium] (proposed)
$ lore task accept tsk_77cb…76c4 --priority high
lore: wrote ~/work/website/.lore/data/tasks/tsk_77cb…76c4.json
✓ tsk_77cb…76c4 updated
lore live: (live, pushed) commit 070bbeb to origin refs/lore/live
$ lore task decline tsk_d191…45c2 --reason wont_do --note 'The client keeps the name'
lore: wrote ~/work/website/.lore/data/tasks/tsk_d191…45c2.json
lore: wrote ~/work/website/.lore/data/comments/cmt_b4d3…11aa.json
✓ tsk_d191…45c2 cancelled
  note kept as a comment
lore live: (live, pushed) commit 2297d41 to origin refs/lore/live
lore live: landed on main (c929695); leaves live once main is fetched
$ lore task triage
(no tasks)
$ lore task list --status cancelled
✗ tsk_54fe…28f2 Update the footer year to 2027 [low] due:2026-11-20
$ lore task list --status cancelled --all
✗ tsk_54fe…28f2 Update the footer year to 2027 [low] due:2026-11-20
✗ tsk_d191…45c2 Rename the shop to Website Pro [medium] (proposed)
$ lore task show tsk_d191…45c2
tsk_d191…45c2
  title:    Rename the shop to Website Pro
  status:   cancelled
  priority: medium
  commit:   proposed
  closed:   wont_do
  notes:
    2026-11-02 10:06  The client keeps the name
created 2026-11-02T10:06:05Z by alice · updated 2026-11-02T10:06:20Z by alice
```
<!-- /example -->

A declined task keeps its `proposed` commitment, so `lore task list --status cancelled` still hides it like every proposal; `--all` (or `--include-proposed`) shows it. `lore task show` prints the close reason and the note.

## Step 4 — Snooze a task until a date

`--defer-until YYYY-MM-DD` hides the task until that day. `lore task deferred` lists what is snoozed (the `⏾` mark shows the date), and `lore task list --include-deferred` shows it next to the rest.

<!-- example: tasks#defer -->
```console
# alice, in website
$ lore task add 'Renew the SSL certificate' --defer-until 2026-12-01
lore: wrote ~/work/website/.lore/data/tasks/tsk_2537…6bbb.json
✓ tsk_2537…6bbb — Renew the SSL certificate [medium]
lore live: (live, pushed) commit a8de760 to origin refs/lore/live
$ lore task deferred
○ tsk_2537…6bbb Renew the SSL certificate [medium] ⏾2026-12-01
$ lore task list --include-deferred
▶ tsk_162d…9666 Fix the typo on the pricing page [medium]
○ tsk_76bd…3f10 Write the welcome email [medium]
○ tsk_d431…313a Send the welcome email [medium]
○ tsk_28f8…03df Check every link on the home page [medium]
○ tsk_f147…5139 Add a FAQ page [medium]
○ tsk_2537…6bbb Renew the SSL certificate [medium] ⏾2026-12-01
```
<!-- /example -->

On that day the task comes back into `lore task list` by itself. You do not need to do anything.

## Step 5 — Wake a task up early

`--clear-defer` removes the date, so the task is back in the list at once.

<!-- example: tasks#undefer -->
```console
# alice, in website
$ lore task edit tsk_2537…6bbb --clear-defer
lore: wrote ~/work/website/.lore/data/tasks/tsk_2537…6bbb.json
✓ tsk_2537…6bbb updated
lore live: (live, pushed) commit 9169078 to origin refs/lore/live
$ lore task deferred
(no tasks)
```
<!-- /example -->

## Try it

The full list with the snoozed task, from the same run:

<!-- example: tasks#defer only=3 -->
```console
# alice, in website
$ lore task list --include-deferred
▶ tsk_162d…9666 Fix the typo on the pricing page [medium]
○ tsk_76bd…3f10 Write the welcome email [medium]
○ tsk_d431…313a Send the welcome email [medium]
○ tsk_28f8…03df Check every link on the home page [medium]
○ tsk_f147…5139 Add a FAQ page [medium]
○ tsk_2537…6bbb Renew the SSL certificate [medium] ⏾2026-12-01
```
<!-- /example -->

| Command | Shows |
|---|---|
| `lore task list` | accepted tasks that are not snoozed and not finished |
| `lore task triage` | proposed tasks |
| `lore task someday` | someday ideas |
| `lore task deferred` | snoozed tasks whose date is still ahead |
| `lore task list --all` | everything: finished, proposed, someday and snoozed |
| `lore task list --commitment proposed` | only one commitment |

## Pitfalls

- **Agents must say how sure they are.** When a coding agent adds a task (`LORE_ACTOR=agent:…`, or a detected coding agent), `--commitment` is required; lore refuses the task without it (`E_INVALID_INPUT` naming `--commitment`). A person may leave it out and gets `accepted`, and a sub-task added with `--parent` inherits its parent's commitment, so it needs none.
- **A proposed task is invisible in the normal list.** If you cannot find a task you just added, check `lore task triage` and `lore task someday`.
- **Dates are whole days in `YYYY-MM-DD` form.** `--defer-until 2026-12-01` hides the task until that date; a time of day is not accepted.
- **Decline is only for proposals.** On an accepted or someday task `lore task decline` is refused (`E_REFUSED`) and the task stays open; stop it with `lore task cancel <id> --reason <why>`.
- **A declined task stays proposed.** `decline` cancels it but does not change its commitment, so it only shows in lists with `--all` or `--include-proposed`.
- **Snooze is not a reminder.** A snoozed task comes back quietly; nothing tells you. If you need to be told, add a [reminder](reminders.md).

## Related

- [Tasks](tasks.md): add, list, finish and change tasks.
- [Reminders](reminders.md): dates that tell you, and come back every year if you want.
- [Missions and plans](missions-and-plans.md): group tasks under a bigger goal.
- [Task tools](task-tools.md): repeating tasks, templates, checklists and the stale sweep.
- [Team commands › lore task list](../team/commands.md#lore-task-list): `--mine` and the other team filters.
