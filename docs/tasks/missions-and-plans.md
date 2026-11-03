# Missions and plans

**In short:** a mission is a bigger goal that holds many tasks, like "Open the online shop". A plan is a written note about how you will get there. A plan file turns a short list in a text file into a mission with its tasks in one command, and a handoff is a "here is where I stopped" note that shows up in someone's inbox.

## What it is and when you want it

One task is one step. Most real goals need many steps in an order: take the photos, then write the product pages, then send the launch email. A **mission** is the folder that holds those tasks, with a target date and a status (`active`, `paused`, `done` or `cancelled`). `lore mission status` shows the whole goal at a glance: what is ready to start, what waits for something else, and what is done.

A **plan** is a written note with a title and a body, such as "Collect emails on the home page first, then send one newsletter a month." Use it to write down the idea before you split it into tasks.

A **plan file** is a small YAML file (a plain text format of `name: value` lines) that lists a mission and its tasks. `lore plan apply` reads it and creates everything at once. Running it again adds nothing, so you can keep the file in the repo and apply it safely.

A **handoff** is a short note for a person, like a sticky note on their desk: "Two of the three sections are written; the team photo is still missing." It appears in their `lore inbox` until they mark it read.

Alice (an agency developer) plans the client's online shop in `github.com/client/website`.

## Before you start

- lore set up in the repo, and the basics from [Tasks](tasks.md).
- Mission ids look like `msn_7a67…9504`; copy the full id from `lore mission list`.

## Step 1 — Add a mission and its tasks

`lore mission add` takes a title, an optional `--target` date and a `--body`. Attach tasks with `--mission <id>` when you add them, or later with `lore task edit <id> --mission <id>`. `--after` makes a task wait for another (see [Tasks › Step 9](tasks.md#step-9--make-one-task-wait-for-another)).

<!-- example: missions#mission-add -->
```console
# alice, in website
$ lore mission add 'Open the online shop' --target 2026-11-30 --body 'The shop goes live with ten products and a launch email.'
✓ msn_7a67…9504 — Open the online shop
lore live: (live, pushed)
$ lore task add 'Take the product photos' --mission msn_7a67…9504
✓ tsk_2ad3…f891 — Take the product photos [medium]
lore live: (live, pushed)
$ lore task add 'Write the product pages' --mission msn_7a67…9504 --after tsk_2ad3…f891
✓ tsk_162d…9666 — Write the product pages [medium]
lore live: (live, pushed)
$ lore task add 'Send the invoice reminder'
✓ tsk_54fe…28f2 — Send the invoice reminder [medium]
lore live: (live, pushed)
$ lore task edit tsk_54fe…28f2 --mission msn_7a67…9504
✓ tsk_54fe…28f2 updated
lore live: (live, pushed)
```
<!-- /example -->

## Step 2 — List and show missions

`lore mission list` shows open missions with how many tasks each holds. `lore mission show` adds the body and the tasks.

<!-- example: missions#mission-list -->
```console
# alice, in website
$ lore mission list
msn_7a67…9504 [active] Open the online shop   (3 tasks)
$ lore mission show msn_7a67…9504
msn_7a67…9504
  title:  Open the online shop
  status: active
  target: 2026-11-30
The shop goes live with ten products and a launch email.
Tasks:
  tsk_2ad3…f891 [todo] Take the product photos
  tsk_162d…9666 [todo] Write the product pages
  tsk_54fe…28f2 [todo] Send the invoice reminder
```
<!-- /example -->

## Step 3 — See the whole mission at a glance

`lore mission status` sorts every task of the mission into a state: `ready` (can start now), `waiting` (waits for another task, and says which), `done`, and the team states `claimed` (an agent has put its name on it), `gated` (waits for a person to say yes), `expired` (the agent's claim ran out) and `blocked` (see [Agents](../team/agents.md)). When the photos are done, the product pages become ready.

<!-- example: missions#mission-status -->
```console
# alice, in website
$ lore mission status msn_7a67…9504
msn_7a67…9504 Open the online shop   3 tasks · 0 done · 0 claimed · 2 ready · 1 waiting · 0 gated · 0 expired · 0 blocked
  ready     tsk_54fe…28f2  Send the invoice reminder                 
  ready     tsk_2ad3…f891  Take the product photos                   
  waiting   tsk_162d…9666  Write the product pages                   waits for tsk_2ad3…f891 to be done (todo)
$ lore task done tsk_2ad3…f891 --no-lesson
✓ tsk_2ad3…f891 done
lore live: (live, pushed)
lore live: landed on main (601d285); leaves live once main is fetched
$ lore mission status msn_7a67…9504
msn_7a67…9504 Open the online shop   3 tasks · 1 done · 0 claimed · 2 ready · 0 waiting · 0 gated · 0 expired · 0 blocked
  done      tsk_2ad3…f891  Take the product photos      alice        not merged yet
  ready     tsk_54fe…28f2  Send the invoice reminder                 
  ready     tsk_162d…9666  Write the product pages                   
```
<!-- /example -->

`lore task list --mission <id>` lists only the open tasks of one mission:

<!-- example: missions#mission-tasks -->
```console
# alice, in website
$ lore task list --mission msn_7a67…9504
○ tsk_162d…9666 Write the product pages [medium]
○ tsk_54fe…28f2 Send the invoice reminder [medium]
```
<!-- /example -->

## Step 4 — Change, find, pause and finish a mission

`lore mission edit` changes only the fields you pass. `lore mission search` finds missions by a word in the title or body.

<!-- example: missions#mission-edit -->
```console
# alice, in website
$ lore mission edit msn_7a67…9504 --target 2026-12-07 --title 'Open the online shop before Christmas'
✓ msn_7a67…9504 updated
lore live: (live, pushed)
lore live: 2 row(s) left live (main holds them)
$ lore mission search christmas
msn_7a67…9504 [active] Open the online shop before Christmas
```
<!-- /example -->

Pause a mission you have put on hold, and resume it when you pick it up again:

<!-- example: missions#pause -->
```console
# alice, in website
$ lore mission pause msn_7a67…9504
✓ msn_7a67…9504 paused
lore live: (live, pushed)
$ lore mission list
msn_7a67…9504 [paused] Open the online shop before Christmas   (3 tasks)
$ lore mission resume msn_7a67…9504
✓ msn_7a67…9504 resumed
lore live: (live, pushed)
```
<!-- /example -->

`lore mission done` closes it. Finished missions leave the normal list; `--all` shows them.

<!-- example: missions#mission-done -->
```console
# alice, in website
$ lore mission done msn_7a67…9504
✓ msn_7a67…9504 completed
lore live: (live, pushed)
lore live: landed on main (86dbae4); leaves live once main is fetched
$ lore mission list
(no missions)
$ lore mission list --all
msn_7a67…9504 [done] Open the online shop before Christmas   (3 tasks)
```
<!-- /example -->

Delete is for mistakes only. It needs `--confirm`:

<!-- example: missions#mission-delete -->
```console
# alice, in website
$ lore mission add 'Test mission, please ignore'
✓ msn_05e9…4603 — Test mission, please ignore
lore live: (live, pushed)
lore live: 1 row(s) left live (main holds them)
$ lore mission delete msn_05e9…4603
ERROR: [E_INVALID_INPUT] --confirm required to hard-delete
  hint: a hard delete cannot be undone; pass --confirm if you really mean it
(exit 1)
$ lore mission delete msn_05e9…4603 --confirm
✓ msn_05e9…4603 deleted (hard)
lore live: (live, pushed)
lore live: landed on main (9b40ce7); leaves live once main is fetched
```
<!-- /example -->

## Step 5 — Write a plan

`lore plan add` saves a written plan. `--title` and `--body` are both required. `lore plan edit --status` takes any word you like, such as `draft` or `approved`.

<!-- example: missions#plan-add -->
```console
# alice, in website
$ lore plan add --title 'Newsletter plan' --body 'Collect emails on the home page first, then send one newsletter a month.'
✓ pln_c0f3…b5b8
lore sync: exported 1
lore sync: 1 file(s) in .lore/data not committed — commit them with your change
$ lore plan list
PL  pln_c0f3…b5b8
$ lore plan search newsletter
pln_c0f3…b5b8 Newsletter plan
$ lore plan edit pln_c0f3…b5b8 --status approved
✓ plan pln_c0f3…b5b8 updated
lore sync: exported 1
lore sync: 1 file(s) in .lore/data not committed — commit them with your change
```
<!-- /example -->

A plan note stays a note: it does not create tasks. To turn a list into tasks, use a plan file (next step).

## Step 6 — Turn a plan file into a mission with tasks

Write the mission and its tasks in a YAML file. Each task has a `key`, a short name you choose, so other tasks can say `after: [form]` ("after the form task").

<!-- example: missions#plan-file -->
```console
# alice, in website
$ cat newsletter.yaml
mission: {key: newsletter, title: "Start the monthly newsletter"}
tasks:
  - {key: form,  title: "Add the sign-up form to the home page", timebox: 90m}
  - {key: first, title: "Write the first newsletter", after: [form]}
  - {key: send,  title: "Send the first newsletter", after: [first]}
```
<!-- /example -->

`lore plan apply` creates the mission and every task, with the order you wrote down:

<!-- example: missions#plan-apply -->
```console
# alice, in website
$ lore plan apply newsletter.yaml
✓ msn_76bd…3f10 Start the monthly newsletter (created)
  tsk_d431…313a Add the sign-up form to the home page  timebox 1h30m
  tsk_b4f4…a5b0 Write the first newsletter    after form
  tsk_28f8…03df Send the first newsletter     after first
lore live: (live, pushed)
$ lore mission status msn_76bd…3f10
msn_76bd…3f10 Start the monthly newsletter   3 tasks · 0 done · 0 claimed · 1 ready · 2 waiting · 0 gated · 0 expired · 0 blocked
  ready     tsk_d431…313a  Add the sign-up form to the home page              0s (timebox 1h30m)
  waiting   tsk_28f8…03df  Send the first newsletter                 waits for tsk_b4f4…a5b0 to be done (todo)
  waiting   tsk_b4f4…a5b0  Write the first newsletter                waits for tsk_d431…313a to be done (todo)
```
<!-- /example -->

Run it again and nothing new is made: lore finds each row again by its key and says `(found)`.

<!-- example: missions#plan-apply-again -->
```console
# alice, in website
$ lore plan apply newsletter.yaml
✓ msn_76bd…3f10 Start the monthly newsletter (found)
  tsk_d431…313a Add the sign-up form to the home page  timebox 1h30m
  tsk_b4f4…a5b0 Write the first newsletter    after form
  tsk_28f8…03df Send the first newsletter     after first
```
<!-- /example -->

### The plan file format

<!-- ds:block id=plan-file-zo3uef01 -->
| Field | Required | What it does |
|---|---|---|
| `mission.title` | yes | the mission's title |
| `mission.key` | no | a short name for the mission; a re-run finds the mission by it (without a key, by its exact title) |
| `mission.to` | no | where the mission goes in a team setup: `repo`, an org or a store folder (see [Team commands › lore plan apply](../team/commands.md#lore-plan-apply)) |
| `approvers` | no | team only: the people who may approve this mission's gates, as `["@alice"]` |
| `tasks[].key` | yes | a short name, unique in the file |
| `tasks[].title` | yes | the task's title |
| `tasks[].after` | no | keys of tasks this one waits for; they must come **earlier** in the file |
| `tasks[].timebox` | no | how long the task should take, like `90m` or `2h` |
| `tasks[].repo` | no | team only: which repo of a group the task belongs to |
| `tasks[].role` | no | team only: a role that `lore task next --role` matches, such as `coordinator` |
| `tasks[].gate` | no | team only: `required` makes the task wait for a person's approval |

## Step 7 — Leave a handoff and read your inbox

A handoff is for someone on the repo's *roster* (the list of people and agents lore knows in this repo). `lore actor register` puts you on it. Then `lore handoff add --for @handle` leaves the note, and `--task` says which task it is about. Here Alice leaves one for herself, for tomorrow morning:

<!-- example: missions#register -->
```console
# alice, in website
$ lore actor register
✓ act_3b62…e913 alice (@alice) already registered (refreshed) in this repo
lore live: (live, pushed)
lore live: landed on main (05513aa); leaves live once main is fetched
```
<!-- /example -->

lore already knew Alice from `lore init`, so it says `already registered (refreshed)`; the handoff below still needs this step, or it is refused (see [Pitfalls](#pitfalls)).

<!-- example: missions#handoff -->
```console
# alice, in website
$ lore task add 'Write the about page'
✓ tsk_f147…5139 — Write the about page [medium]
lore live: (live, pushed)
lore live: 1 row(s) left live (main holds them)
$ lore handoff add --for @alice --task tsk_f147…5139 'Two of the three sections are written; the team photo is still missing'
✓ handoff hnd_2537…6bbb for @alice
lore live: (live, pushed)
$ lore inbox
-  handoff  @alice       tsk_f147…5139  Two of the three sections are written; the team photo is still missing
```
<!-- /example -->

`lore inbox` is the one place that shows what needs you: handoffs, tasks assigned to you, gates waiting for your approval, and more when you work in a team. `lore handoff ack` marks a handoff as read, and it leaves the inbox. `lore handoff search` still finds it.

<!-- example: missions#handoff-ack -->
```console
# alice, in website
$ lore handoff ack hnd_2537…6bbb
✓ hnd_2537…6bbb acked
lore live: (live, pushed)
$ lore inbox
(inbox empty)
$ lore handoff search photo
hnd_2537…6bbb [acked] Two of the three sections are written; the team photo is still missing
```
<!-- /example -->

## Try it

The plan file from Step 6 is all you need. Save it as `newsletter.yaml` in your repo, then:

<!-- example: missions#plan-apply -->
```console
# alice, in website
$ lore plan apply newsletter.yaml
✓ msn_76bd…3f10 Start the monthly newsletter (created)
  tsk_d431…313a Add the sign-up form to the home page  timebox 1h30m
  tsk_b4f4…a5b0 Write the first newsletter    after form
  tsk_28f8…03df Send the first newsletter     after first
lore live: (live, pushed)
$ lore mission status msn_76bd…3f10
msn_76bd…3f10 Start the monthly newsletter   3 tasks · 0 done · 0 claimed · 1 ready · 2 waiting · 0 gated · 0 expired · 0 blocked
  ready     tsk_d431…313a  Add the sign-up form to the home page              0s (timebox 1h30m)
  waiting   tsk_28f8…03df  Send the first newsletter                 waits for tsk_b4f4…a5b0 to be done (todo)
  waiting   tsk_b4f4…a5b0  Write the first newsletter                waits for tsk_d431…313a to be done (todo)
```
<!-- /example -->

## Pitfalls

**In a plan file, `after` must name an earlier task.** A task that waits for a task further down the file is refused. lore creates the mission before it checks the tasks, so the failed run leaves an empty mission behind. Fix the file, then remove that mission with `lore mission delete <id> --confirm`:

<!-- example: missions#plan-errors -->
```console
# alice, in website
$ cat bad.yaml
mission: {title: "Bad plan"}
tasks:
  - {key: b, title: "Second", after: [a]}
  - {key: a, title: "First"}
$ lore plan apply bad.yaml
✓ msn_4c59…6c38 Bad plan (created)
lore live: (live, pushed)
ERROR: [E_INVALID_INPUT] plan: task b is after a, which is not an earlier task of the plan
  hint: after: takes keys of earlier tasks, optionally with :done or :merged (e.g. backend:merged)
(exit 1)
```
<!-- /example -->

**A handoff needs a roster.** `--for @someone` who has never run `lore actor register` in the repo fails:

<!-- example: missions#handoff-unknown -->
```console
# alice, in website
$ lore handoff add --for @dave 'The logo files are in the shared drive'
ERROR: [E_INVALID_INPUT] no one is @dave on this repo's roster
  hint: register first: `lore actor register` (as that person)
(exit 1)
```
<!-- /example -->

Other traps:

- **Changing a title in the plan file does not change the task.** A re-run finds each task by its key and leaves it as it is. Use `lore task edit` to rename it.
- **Misspelled fields are ignored.** A field lore does not know, such as `afer:`, is skipped without a warning. Check the `after` part of the `plan apply` output.
- **`lore plan list` and `lore plan show` print only `PL` and the id**, not the title. Use `lore plan search <word>`, or `lore plan show <id> --json`, to read it.
- **`lore mission done` does not check the tasks.** You can close a mission that still has open tasks; they stay open.

## Related

- [Tasks](tasks.md): add, list, order and finish tasks.
- [Commitment and snooze](commitment-and-snooze.md): proposals and ideas.
- [Team use case › Agents on a mission](../team/use-cases/agents-mission.md): the same plan file with agents, gates and approvers.
- [Team commands › lore mission add](../team/commands.md#lore-mission-add), [lore mission status](../team/commands.md#lore-mission-status), [lore handoff add](../team/commands.md#lore-handoff-add) and [lore inbox](../team/commands.md#lore-inbox).
