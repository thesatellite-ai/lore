# Tasks

**In short:** a task is one thing you need to do, like "Send the invoice reminder before Friday's demo". You add it with `lore task add`, see your list with `lore task list`, and close it with `lore task done`. lore keeps the list next to your code, so your coding tools can read it too.

## What it is and when you want it

A task is a to-do item that lives in your repo. It has a title, and it can have a longer description (the *body*), a priority, a due date and a status. The status tells you where it is: `todo` (not started), `in_progress` (you are on it), `done`, `cancelled` or `blocked`.

You want tasks when you work on a project over many days and want one list that you, your teammates and your coding tools all read. A coding tool can ask lore "what is open?" instead of you explaining it again every session.

This page is for one person. Alice (an agency developer) keeps her to-do list for the client's website, `github.com/client/website`. Working with other people and agents is on the team pages: claims (an agent puts its name on a task), `task next` (lore hands out the next free task) and gates (a step that waits for a person to say yes). See [Related](#related).

## Before you start

- lore set up in the repo: follow [Your first project](../knowledge/first-project.md) up to `lore init`.
- Every example below is a real run. The pages shorten ids, like `tsk_2ad3…f891`; lore prints the full id, and that full id is what you type (copy it from `lore task list`).

## Step 1 — Add tasks

Give each task a short title that says what to do. Add `--priority` (`low`, `medium`, `high` or `urgent`; `medium` if you leave it out), `--due` for a date, and `--body` for the details.

<!-- example: tasks#add -->
```console
# alice, in website
$ lore task add "Send the invoice reminder before Friday's demo" --due 2026-11-06 --priority high
✓ tsk_7a67…9504 — Send the invoice reminder before Friday's demo [high]
lore live: (live, pushed)
$ lore task add 'Fix the typo on the pricing page' --body 'The Pro plan says 9 a month; it should say 19.'
✓ tsk_2ad3…f891 — Fix the typo on the pricing page [medium]
lore live: (live, pushed)
$ lore task add 'Update the footer year'
✓ tsk_162d…9666 — Update the footer year [medium]
lore live: (live, pushed)
```
<!-- /example -->

<!-- ds:block id=team-live-kinds-88j26q7s -->
The line `lore live: (live, pushed)` means lore shared the task at once: it put the task on a hidden list on GitHub that is not a branch, so your teammates see it without waiting for your next commit. You do not need to do anything for this. [Concepts › Branch data, live rows and signals](../team/concepts.md#branch-data-live-rows-and-signals) explains it in full.

## Step 2 — See your list

`lore task list` shows the open tasks. The mark at the start of each line is the status: `○` todo, `▶` in progress, `✓` done, `✗` cancelled.

<!-- example: tasks#list -->
```console
# alice, in website
$ lore task list
○ tsk_7a67…9504 Send the invoice reminder before Friday's demo [high] due:2026-11-06
○ tsk_2ad3…f891 Fix the typo on the pricing page [medium]
○ tsk_162d…9666 Update the footer year [medium]
```
<!-- /example -->

## Step 3 — Start a task

`lore task start` moves a task to `in_progress`. Use `--status` on `task list` to see only one status.

<!-- example: tasks#start -->
```console
# alice, in website
$ lore task start tsk_2ad3…f891
✓ tsk_2ad3…f891 started
lore live: (live, pushed)
$ lore task list --status in_progress
▶ tsk_2ad3…f891 Fix the typo on the pricing page [medium]
```
<!-- /example -->

## Step 4 — Look at one task

`lore task show` prints every field, with the body at the end. The `commit:` line is the task's *commitment* (how sure it is; see [Commitment and snooze](commitment-and-snooze.md)), not a git commit.

<!-- example: tasks#show -->
```console
# alice, in website
$ lore task show tsk_2ad3…f891
tsk_2ad3…f891
  title:    Fix the typo on the pricing page
  status:   in_progress
  priority: medium
  commit:   accepted
The Pro plan says 9 a month; it should say 19.
created 2026-11-02T10:00:10Z by act_54fe…28f2 · updated 2026-11-02T10:00:25Z by act_54fe…28f2
```
<!-- /example -->

## Step 5 — Change a task

`lore task edit` changes only the fields you pass. Everything else stays as it was. To remove a value, use the matching `--clear-…` flag, such as `--clear-due`.

<!-- example: tasks#edit -->
```console
# alice, in website
$ lore task edit tsk_162d…9666 --priority low --due 2026-11-20 --title 'Update the footer year to 2027'
✓ tsk_162d…9666 updated
lore live: (live, pushed)
$ lore task show tsk_162d…9666
tsk_162d…9666
  title:    Update the footer year to 2027
  status:   todo
  priority: low
  commit:   accepted
  due:      2026-11-20
created 2026-11-02T10:00:15Z by act_54fe…28f2 · updated 2026-11-02T10:00:40Z by act_54fe…28f2
```
<!-- /example -->

## Step 6 — Finish a task

`lore task done` closes the task. `--lesson` saves what you learned as a memory (a note lore keeps for next time) that points back at the task. If you have nothing to add, pass `--no-lesson` and lore will not remind you.

<!-- example: tasks#done -->
```console
# alice, in website
$ lore task done tsk_7a67…9504 --lesson 'The client pays faster when the reminder goes out two days before the demo'
✓ tsk_7a67…9504 done
  lesson → mem_05e9…4603
lore live: (live, pushed)
lore live: landed on main (601d285); leaves live once main is fetched
$ lore task list
▶ tsk_2ad3…f891 Fix the typo on the pricing page [medium]
○ tsk_162d…9666 Update the footer year to 2027 [low] due:2026-11-20
$ lore task list --all
✓ tsk_7a67…9504 Send the invoice reminder before Friday's demo [high] due:2026-11-06
▶ tsk_2ad3…f891 Fix the typo on the pricing page [medium]
○ tsk_162d…9666 Update the footer year to 2027 [low] due:2026-11-20
```
<!-- /example -->

Done tasks leave the normal list. `--all` shows everything, including done and cancelled tasks.

When a task is done, lore writes it into `main` on GitHub by itself ("landed on main"). That is called *landing*, like filing a finished job in the project folder. It keeps the finished task with the code for good. See [Pitfalls](#pitfalls) for what this means for your next `git push`.

## Step 7 — Search your tasks

`lore task search` looks for words in titles and bodies. By default it only searches open tasks; add `--all` to search done and cancelled ones too.

<!-- example: tasks#search -->
```console
# alice, in website
$ lore task search pricing
tsk_2ad3…f891 [in_progress] Fix the typo on the pricing page
$ lore task search invoice
(no matches)
$ lore task search invoice --all
tsk_7a67…9504 [done] Send the invoice reminder before Friday's demo
```
<!-- /example -->

## Step 8 — Cancel or delete a task

Cancel a task you will not do. It stays in the history, so you can still find it.

<!-- example: tasks#cancel -->
```console
# alice, in website
$ lore task cancel tsk_162d…9666
✓ tsk_162d…9666 cancelled
lore live: (live, pushed)
lore live: landed on main (86dbae4); leaves live once main is fetched
lore live: 2 row(s) left live (main holds them)
$ lore task list --status cancelled
✗ tsk_162d…9666 Update the footer year to 2027 [low] due:2026-11-20
```
<!-- /example -->

Delete only removes a task for good, with no undo. lore asks first (without a terminal it stops with `E_NEEDS_CONFIRM` until you add `--yes`), so a typo cannot delete the wrong task.

<!-- example: tasks#delete -->
```console
# alice, in website
$ lore task add 'Test task, please ignore'
✓ tsk_c0f3…b5b8 — Test task, please ignore [medium]
lore live: (live, pushed)
lore live: 1 row(s) left live (main holds them)
$ lore task delete tsk_c0f3…b5b8
ERROR: [E_NEEDS_CONFIRM] delete task tsk_c0f3…b5b8 Test task, please ignore?
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore task delete tsk_c0f3…b5b8 --yes
✓ tsk_c0f3…b5b8 moved to the trash (restore: lore trash restore tsk_c0f3…b5b8)
lore live: (live, pushed)
```
<!-- /example -->

Prefer cancel. Deleting is for test tasks and mistakes.

## Step 9 — Make one task wait for another

Some work has an order: you cannot send the welcome email before you write it. `--after <id>` says "this task waits until that one is done". You can list several ids, separated by commas.

<!-- example: tasks#after -->
```console
# alice, in website
$ lore task add 'Write the welcome email'
✓ tsk_76bd…3f10 — Write the welcome email [medium]
lore live: (live, pushed)
$ lore task add 'Send the welcome email' --after tsk_76bd…3f10
✓ tsk_d431…313a — Send the welcome email [medium]
lore live: (live, pushed)
$ lore task show tsk_d431…313a
tsk_d431…313a
  title:    Send the welcome email
  status:   todo
  priority: medium
  commit:   accepted
created 2026-11-02T10:01:50Z by act_54fe…28f2 · updated 2026-11-02T10:01:50Z by act_54fe…28f2
```
<!-- /example -->

A task can never wait on itself, not even through other tasks. lore refuses such a loop (a *cycle*) with `E_CYCLE`:

<!-- example: tasks#cycle -->
```console
# alice, in website
$ lore task edit tsk_76bd…3f10 --after tsk_d431…313a
ERROR: [E_CYCLE] dependency cycle: tsk_76bd…3f10 → tsk_d431…313a → tsk_76bd…3f10
  hint: drop one --after edge; a task may not wait on itself, even through others
(exit 19)
```
<!-- /example -->

To see which tasks are ready and which are waiting, put them in a mission and run `lore mission status`; see [Missions and plans](missions-and-plans.md#step-3--see-the-whole-mission-at-a-glance).

## Step 10 — Group tasks in a tasklist

A tasklist is a named list, like a page in a notebook: "Launch week", "Bugs from the demo". Every task belongs to one. If you do not pick one, lore uses the project's default list, which it creates the first time you add a task. Use `--tasklist` on `task add` or `task edit` to pick another.

<!-- example: tasks#tasklist -->
```console
# alice, in website
$ lore tasklist add --title 'Launch week' --body 'Everything that must happen before the shop opens'
✓ tlt_b4f4…a5b0
$ lore task add 'Check every link on the home page' --tasklist tlt_b4f4…a5b0
✓ tsk_28f8…03df — Check every link on the home page [medium]
lore live: (live, pushed)
$ lore task edit tsk_76bd…3f10 --tasklist tlt_b4f4…a5b0
✓ tsk_76bd…3f10 updated
lore live: (live, pushed)
$ lore tasklist list
TA  tlt_4c59…6c38
TA  tlt_b4f4…a5b0
$ lore tasklist search launch
tlt_b4f4…a5b0 Launch week
$ lore tasklist edit tlt_b4f4…a5b0 --status closed
✓ tasklist tlt_b4f4…a5b0 updated
lore live: (live, pushed)
lore live: landed on main (9b40ce7); leaves live once main is fetched
```
<!-- /example -->

`lore tasklist list` prints only ids today (see [Pitfalls](#pitfalls)); `lore tasklist search` prints the titles.

## Step 11 — Save a filter under a name

<!-- ds:block id=sync-syncedtables-fa4jung7 -->
`lore task view add` stores a task filter under a name, such as `open_todo`, so you or a tool can reuse it. The filter uses the same `--where` grammar as `lore task list` (and `--sort`, `--group-by` if you like). `lore task list --view <name>` applies it. A saved view is a row in `.lore/data`, so it travels with git like your tasks.

<!-- example: tasks#task-view -->
```console
# alice, in website
$ lore task view add open_todo --where 'status:todo'
tvw_3b62…e913  open_todo  where "status:todo"
$ lore task view list
tvw_3b62…e913  open_todo  where "status:todo"
$ lore task list --view open_todo
○ tsk_76bd…3f10 Write the welcome email [medium]
○ tsk_d431…313a Send the welcome email [medium]
○ tsk_28f8…03df Check every link on the home page [medium]
```
<!-- /example -->

## Step 12 — Move tasks in and out with files

`lore task export` prints your tasks as a file; `--where` picks which (the `lore task list` grammar), `--with-children` adds their sub-tasks and `--with-checklist` their checklists:

- `--format json` (the default) is the shape `lore task import` reads back, status, estimate and parent included, so export then import round-trips them.
- `--format csv` has the same columns plus a few for reading (`lead`, `created_at`, `completed_at`); import reads past those with one warning each.
- `--format markdown` prints `- [ ] title (id)` lines, nested by parent with `--tree`.
- `--format github` prints a JSON list of issues for your own `gh` scripts: title, body (with `--with-checklist`, the checklist as a GitHub task list), `state` open or closed (`state_reason` completed for done, not_planned for cancelled), labels `priority:<p>` and `status:<s>` (and `proposed`), the mission's title as milestone, the due date, and the lore id and parent id so a script can link sub-issues. It names no assignees (lore people are not GitHub logins) and no internal or sensitive field. It only writes the file: nothing is sent to GitHub.

Here alice prints her open tasks as Markdown and as CSV:

<!-- example: tasks#export -->
```console
# alice, in website
$ lore task export --where 'status:todo' --format markdown
- [ ] Write the welcome email (tsk_76bd…3f10)
- [ ] Send the welcome email (tsk_d431…313a)
- [ ] Check every link on the home page (tsk_28f8…03df)
- [ ] Try a dark mode for the shop (tsk_f147…5139)
- [ ] Add a FAQ page (tsk_2537…6bbb)
- [ ] Renew the SSL certificate (tsk_b69e…5ed7)
- [ ] Add a size guide (tsk_d191…45c2)
$ lore task export --where 'status:todo' --format csv
id,title,body,priority,commitment,due,defer_until,tasklist,mission,plan,role,tasklist_name,mission_name,plan_name,status,estimate,parent,lead,created_at,completed_at
tsk_76bd…3f10,Write the welcome email,,medium,accepted,,,tlt_b4f4…a5b0,,,,Launch week,,,todo,,,,2026-11-02T10:01:45Z,
tsk_d431…313a,Send the welcome email,,medium,accepted,,,tlt_4c59…6c38,,,,default,,,todo,,,,2026-11-02T10:01:50Z,
tsk_28f8…03df,Check every link on the home page,,medium,accepted,,,tlt_b4f4…a5b0,,,,Launch week,,,todo,,,,2026-11-02T10:02:10Z,
tsk_f147…5139,Try a dark mode for the shop,,medium,someday,,,tlt_4c59…6c38,,,,default,,,todo,,,,2026-11-02T10:02:50Z,
tsk_2537…6bbb,Add a FAQ page,,medium,accepted,,,tlt_4c59…6c38,,,,default,,,todo,,,,2026-11-02T10:02:55Z,
tsk_b69e…5ed7,Renew the SSL certificate,,medium,accepted,,,tlt_4c59…6c38,,,,default,,,todo,,,,2026-11-02T10:03:25Z,
tsk_d191…45c2,Add a size guide,,high,accepted,,,tlt_4c59…6c38,,,,default,,,todo,,,,2026-11-02T10:06:00Z,
```
<!-- /example -->

`lore task import <file>` (`--from csv|json`, otherwise read from the extension) adds the file's tasks; `--dry-run` lists what it would create, and it asks before writing (`--yes` in scripts). It reads the columns `status`, `estimate` (in the unit of the store it writes to) and `parent` (another record of the file, before or after it, or a task id the store already has; anything else is dropped with a warning), and `--map-status 'In Review=in_progress,Shipped=done'` renames another tool's status values. A file where a done or cancelled task still has an open sub-task, or where parents form a loop, is refused before anything is written.

Importing the same file twice does not duplicate anything. Each record is matched by its source and external id; a record already imported adds nothing and is listed as "already imported as <id>", and fields whose value changed in the file since are listed as "differs, not written". Import never overwrites a task: change it with `lore task edit`.

Here alice brings in two tasks from another tool's CSV export, whose `status` column says `To Do` and `In Review`. The `id` column becomes the external id, which is how the second import knows both are already there:

<!-- example: tasks#import -->
```console
# alice, in website
$ cat ~/backlog.csv
id,title,priority,status
T-1,Add a cookie banner,high,To Do
T-2,Compress the product photos,medium,In Review
$ lore task import ~/backlog.csv --map-status 'To Do=todo,In Review=in_progress' --dry-run
would import 2 tasks from csv (dry run, nothing written)
  T-1  Add a cookie banner
  T-2  Compress the product photos
$ lore task import ~/backlog.csv --map-status 'To Do=todo,In Review=in_progress' --yes
✓ 2 tasks imported from csv (batch bat_9a34…b418)
lore live: (live, pushed)
lore live: 2 row(s) left live (main holds them)
$ lore task import ~/backlog.csv --map-status 'To Do=todo,In Review=in_progress' --yes
  2 already imported, nothing added for them:
    T-1  Add a cookie banner  (already imported as tsk_bfed…eeea)
    T-2  Compress the product photos  (already imported as tsk_376c…8e1a)
```
<!-- /example -->

## Try it

After the steps above, the open list holds the task in progress and the tasks still to do (this is the same run, a few steps later):

<!-- example: tasks#commitment only=3 -->
```console
# alice, in website
$ lore task list
▶ tsk_2ad3…f891 Fix the typo on the pricing page [medium]
○ tsk_76bd…3f10 Write the welcome email [medium]
○ tsk_d431…313a Send the welcome email [medium]
○ tsk_28f8…03df Check every link on the home page [medium]
```
<!-- /example -->

Every example on this page comes from one script. In a checkout of lore you can run it yourself; it builds lore and works in a throwaway folder, so nothing on your machine changes:

```console
$ tests/docs/run-examples.sh tasks
```

The commands and their output are written to `tests/docs/transcripts/tasks.txt`.

## Pitfalls

**`git push` is rejected after `task done`.** lore writes finished tasks into `main` on GitHub by itself, so GitHub's `main` is now one commit ahead of yours. Pull first, then push:

<!-- example: tasks#push-after-done -->
```console
# alice, in website
$ git commit -qm 'Fix the Pro plan price on the pricing page'
$ git push -q origin main
To /srv/git/github.com/client/website
 ! [rejected]        main -> main (non-fast-forward)
error: failed to push some refs to '/srv/git/github.com/client/website'
hint: Updates were rejected because the tip of your current branch is behind
hint: its remote counterpart. If you want to integrate the remote changes,
hint: use 'git pull' before pushing again.
hint: See the 'Note about fast-forwards' in 'git push --help' for details.
(exit 1)
$ git pull -q --rebase origin main && git push -q origin main
```
<!-- /example -->

Other traps:

- **Search misses a done task.** `lore task search` looks at open tasks only. Add `--all`.
- **Deleting needs a yes.** Without a terminal, `task delete` stops with `E_NEEDS_CONFIRM` until you add `--yes` (the old spelling `--confirm` works the same on a delete; the task goes to the trash, `lore trash restore` brings it back). To keep the task but stop it, use `lore task cancel` instead.
- **`lore tasklist list` prints only `TA` and the id**, not the title. `lore tasklist show <id>` prints the title, status, task count and body; `lore tasklist search <word>` shows titles.
- **`lore task show` does not list the tasks a task in this repo waits for.** Use `lore mission status` for that: it prints "waits for <id> to be done" next to the task. (For a task kept in an org's notes store, `task show` does print an `after:` line.)
- **Ids are long on purpose.** Copy them from `lore task list` or `--json` output rather than typing them.

## Related

- [Commitment and snooze](commitment-and-snooze.md): ideas, proposals and tasks for later.
- [Task tools](task-tools.md): board, checklists, split, relations, watch, history, templates, repeating tasks and the stale sweep.
- [Run log](run-log.md): time spent on a task, by runs and by hand.
- [Missions and plans](missions-and-plans.md): group tasks under a bigger goal.
- [Link commits](link-commits.md): record which commit finished a task.
- [Reminders](reminders.md): dates that come back.
- [Team commands › lore task add](../team/commands.md#lore-task-add) and the other task flags for teams (`--assign`, `--to`).
- [Agents](../team/agents.md): claims, `lore task next`, heartbeats (an agent saying "I am still working") and gates when agents share the work.
