# Task tools

**In short:** beyond add, list and done, lore has a set of smaller task tools: a board view, a checklist inside a task, splitting a task into sub-tasks, a check of the sub-task tree, relations between tasks, watching a task, a task's history, templates for work you repeat, repeating tasks and a sweep for tasks nobody touched in months. This page walks through each with one person and one repo.

## What it is and when you want it

[Tasks](tasks.md) covers the everyday commands. The tools here help once the list grows:

- **Board** (`lore task board`): the open tasks in columns by status, priority, assignee, mission, repo or parent. It only reads.
- **Checklist** (`lore task checklist`): small steps inside one task that nobody would ever claim on their own, ticked as you go.
- **Split** (`lore task split`): turn one task into sub-tasks in one go. **Tree check** (`lore task tree check`) finds loops and other damage in the sub-task tree.
- **Relations** (`lore task relate` / `unrelate`): mark a duplicate, say one task waits for another, or point at a task that caused this one.
- **Watch** (`lore task watch` / `unwatch`): changes to a watched task reach your inbox.
- **History** (`lore task history`): who changed what on a task, and when.
- **Templates** (`lore task template`): a saved task tree, such as "a release", filled in with arguments each time.
- **Repeating tasks** (`lore task add --recur`, `lore task recur`): a task that comes back every week or month.
- **Stale tasks** (`lore task stale`): open tasks with no change for 60 days, and a way to cancel them in one batch.

Alice (an agency developer) plans the launch of the client's online shop in `github.com/client/website`.

## Before you start

- lore set up in the repo (see [Your first project](../knowledge/first-project.md)) and the basics from [Tasks](tasks.md).

## Step 1 — See the tasks as a board

`lore task board` groups the tasks by status; `--group-by priority` (or `assignee`, `mission`, `repo`, `parent`) picks another column. It takes the same filters as `lore task list` (`--mission`, `--mine`, `--where` and so on) and never writes anything.

<!-- example: task-tools#board -->
```console
# alice, in website
$ lore task add --title 'Write the launch email' --priority high
lore: wrote task_lists/tlt_7a67…9504 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/task_lists/tlt_7a67…9504.json is unchanged)
lore: wrote tasks/tsk_2ad3…f891 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_2ad3…f891.json is unchanged)
✓ tsk_2ad3…f891 — Write the launch email [high]
  created tasklist tlt_7a67…9504 "default" for tasks added without --tasklist
lore live: (live, kept here: `lore sync` or your next `git push` sends live writes; `lore config set --key live.autopush --value true` sends each write at once)
$ lore task add --title 'Pick the product photos' --priority low
lore: wrote tasks/tsk_162d…9666 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_162d…9666.json is unchanged)
✓ tsk_162d…9666 — Pick the product photos [low]
  on tasklist tlt_7a67…9504 "default" (the default; --tasklist picks another)
$ lore task add --title 'Set up the payment provider' --priority urgent
lore: wrote tasks/tsk_54fe…28f2 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_54fe…28f2.json is unchanged)
✓ tsk_54fe…28f2 — Set up the payment provider [urgent]
  on tasklist tlt_7a67…9504 "default" (the default; --tasklist picks another)
$ lore task start --id tsk_54fe…28f2
lore: wrote tasks/tsk_54fe…28f2 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_54fe…28f2.json is unchanged)
✓ tsk_54fe…28f2 started
$ lore task board
todo (2)
  tsk_2ad3…f891  Write the launch email
  tsk_162d…9666  Pick the product photos
in_progress (1)
  tsk_54fe…28f2  Set up the payment provider
done (0)
cancelled (0)
blocked (0)
$ lore task board --group-by priority
urgent (1)
  tsk_54fe…28f2  Set up the payment provider
high (1)
  tsk_2ad3…f891  Write the launch email
medium (0)
low (1)
  tsk_162d…9666  Pick the product photos
```
<!-- /example -->

## Step 2 — Keep a checklist inside a task

A checklist item is a small step of one task: no owner, no status, no claim. Add the first item with `--text` and more with `--item`; tick and untick by position (`1`, `2`, …) or by `chk_` id. Ticking an item does not change the task's status.

<!-- example: task-tools#checklist -->
```console
# alice, in website
$ lore task checklist add --id tsk_2ad3…f891 --text 'Subject line' --item 'Product links' --item 'Discount code' --item 'Send a test to the client'
lore: wrote tasks/tsk_2ad3…f891 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_2ad3…f891.json is unchanged)
✓ 4 checklist items on tsk_2ad3…f891 (chk_05e9…4603, chk_c0f3…b5b8, chk_76bd…3f10, chk_d431…313a)
$ lore task checklist tick --id tsk_2ad3…f891 --item 1
lore: wrote tasks/tsk_2ad3…f891 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_2ad3…f891.json is unchanged)
✓ [x] Subject line (1/4)
$ lore task checklist edit --id tsk_2ad3…f891 --item 3 --text 'Discount code LAUNCH10'
lore: wrote tasks/tsk_2ad3…f891 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_2ad3…f891.json is unchanged)
✓ chk_76bd…3f10 Discount code LAUNCH10
$ lore task checklist list --id tsk_2ad3…f891
1. [x] Subject line  chk_05e9…4603
2. [ ] Product links  chk_c0f3…b5b8
3. [ ] Discount code LAUNCH10  chk_76bd…3f10
4. [ ] Send a test to the client  chk_d431…313a
1/4 done
```
<!-- /example -->

`untick` takes a tick back, and `rm` removes an item. The positions close up after a removal, so read `checklist list` again before using a position:

<!-- example: task-tools#checklist-untick-and-rm -->
```console
# alice, in website
$ lore task checklist untick --id tsk_2ad3…f891 --item 1
lore: wrote tasks/tsk_2ad3…f891 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_2ad3…f891.json is unchanged)
✓ [ ] Subject line (0/4)
$ lore task checklist rm --id tsk_2ad3…f891 --item 2
lore: wrote tasks/tsk_2ad3…f891 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_2ad3…f891.json is unchanged)
✓ removed chk_c0f3…b5b8 Product links
$ lore task checklist list --id tsk_2ad3…f891
1. [ ] Subject line  chk_05e9…4603
2. [ ] Discount code LAUNCH10  chk_76bd…3f10
3. [ ] Send a test to the client  chk_d431…313a
0/3 done
```
<!-- /example -->

If a step turns out to be real work that someone could pick up on its own, `promote` turns it into a sub-task with the same text and removes the item, in one batch:

<!-- example: task-tools#checklist-promote -->
```console
# alice, in website
$ lore task checklist promote --id tsk_2ad3…f891 --item 3
lore: wrote tasks/tsk_b4f4…a5b0 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_b4f4…a5b0.json is unchanged)
lore: wrote tasks/tsk_2ad3…f891 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_2ad3…f891.json is unchanged)
✓ chk_d431…313a became tsk_b4f4…a5b0 "Send a test to the client" under tsk_2ad3…f891
$ lore task list --parent tsk_2ad3…f891
○ tsk_b4f4…a5b0 Send a test to the client [high]
```
<!-- /example -->

A task whose body already has `- [ ]` / `- [x]` lines (copied from an issue, say) can turn them into checklist items with `checklist import --from body`. It shows the items and asks first; `--yes` answers without a terminal:

<!-- example: task-tools#checklist-import -->
```console
# alice, in website
$ lore task add --title 'Check the shop on a phone' --body $'- [ ] iPhone\n- [ ] Android\n- [x] Tablet'
lore: wrote tasks/tsk_28f8…03df on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_28f8…03df.json is unchanged)
✓ tsk_28f8…03df — Check the shop on a phone [medium]
  on tasklist tlt_7a67…9504 "default" (the default; --tasklist picks another)
$ lore task checklist import --id tsk_28f8…03df --from body
ERROR: [E_NEEDS_CONFIRM] add 3 checklist items to tsk_28f8…03df?
  [ ] iPhone
  [ ] Android
  [x] Tablet
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore task checklist import --id tsk_28f8…03df --from body --yes
lore: wrote tasks/tsk_28f8…03df on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_28f8…03df.json is unchanged)
✓ 3 checklist items from the body of tsk_28f8…03df
$ lore task checklist list --id tsk_28f8…03df
1. [ ] iPhone  chk_4c59…6c38
2. [ ] Android  chk_3b62…e913
3. [x] Tablet  chk_f147…5139
1/3 done
```
<!-- /example -->

## Step 3 — Split a task into sub-tasks

`lore task split --id <id> --title <title> --title <title> --title …` adds one sub-task per title, in order, as one batch: one commit for all of them, and a sub-task lore refuses (a secret in its text, say) writes none of them. A flag before the first title applies to every sub-task; a flag after a title applies to that one only (here `--priority high` after "Shoot the mugs"). What a sub-task does not set it takes from the parent, and the output says what it inherited. `--dry-run` shows the plan and writes nothing:

<!-- example: task-tools#split -->
```console
# alice, in website
$ lore task split --id tsk_162d…9666 --title 'Shoot the shirts' --title 'Shoot the mugs' --priority high --title 'Edit the photos' --dry-run
would add 3 sub-tasks under tsk_162d…9666 Pick the product photos:
  1  Shoot the shirts
  2  Shoot the mugs  (priority high)
  3  Edit the photos
  inherited: tasklist tlt_7a67…9504, priority low, commitment accepted
nothing written (--dry-run)
$ lore task split --id tsk_162d…9666 --title 'Shoot the shirts' --title 'Shoot the mugs' --priority high --title 'Edit the photos'
lore: wrote tasks/tsk_2537…6bbb on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_2537…6bbb.json is unchanged)
lore: wrote tasks/tsk_b69e…5ed7 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_b69e…5ed7.json is unchanged)
lore: wrote tasks/tsk_0aae…a61f on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_0aae…a61f.json is unchanged)
✓ 3 sub-tasks under tsk_162d…9666 Pick the product photos (batch bat_6a86…ed8c):
  1  tsk_2537…6bbb Shoot the shirts
  2  tsk_b69e…5ed7 Shoot the mugs  (priority high)
  3  tsk_0aae…a61f Edit the photos
  inherited: tasklist tlt_7a67…9504, priority low, commitment accepted
$ lore task list --parent tsk_162d…9666
○ tsk_2537…6bbb Shoot the shirts [low]
○ tsk_b69e…5ed7 Shoot the mugs [high]
○ tsk_0aae…a61f Edit the photos [low]
```
<!-- /example -->

`--title` may be repeated, one sub-task each (a flag typed after a `--title` is that sub-task's), `--from <file>` reads one title per line (or a YAML list with priority, assignees and due dates), and `--key <key>` makes a rerun add only the sub-tasks that are not there yet (nothing when all are).

## Step 4 — Check the sub-task tree

`lore task tree check` looks for parent loops, orphans, open sub-tasks under a closed parent, trees that are too deep and shared ranks. A healthy tree prints one line:

<!-- example: task-tools#tree-check -->
```console
# alice, in website
$ lore task tree check
✓ task tree ok (8 tasks)
```
<!-- /example -->

When copies of the notes were merged and two tasks ended up as each other's parent, `--fix` breaks the loop (the smallest id becomes top level). The other findings are only reported; fix them with `lore task edit`.

## Step 5 — Relate tasks to each other

`lore task relate --id <id>` records how a task stands to another:

- `--duplicates <tsk>`: this task is the same as that one. It is cancelled with close reason `duplicate`.
- `--blocked-by <tsk>`: this task waits for that one (the same as `lore task edit --after`). `--blocks <tsk>` writes it the other way round.
- `--follows-up <id>`: this task exists because of that row.
- `--relates-to <id>`: see also; any row id.

<!-- example: task-tools#relate -->
```console
# alice, in website
$ lore task add --title 'Write the launch mail'
lore: wrote tasks/tsk_4f89…b64b on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_4f89…b64b.json is unchanged)
✓ tsk_4f89…b64b — Write the launch mail [medium]
  on tasklist tlt_7a67…9504 "default" (the default; --tasklist picks another)
$ lore task relate --id tsk_4f89…b64b --duplicates tsk_2ad3…f891
lore: wrote ~/work/website/.lore/data/knowledge_refs/kfr_77cb…76c4.json
lore: wrote tasks/tsk_4f89…b64b on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_4f89…b64b.json is unchanged)
✓ tsk_4f89…b64b duplicates tsk_2ad3…f891; tsk_4f89…b64b cancelled (close_reason duplicate)
$ lore task add --title 'Thank-you email after the first order'
lore: wrote tasks/tsk_d191…45c2 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_d191…45c2.json is unchanged)
✓ tsk_d191…45c2 — Thank-you email after the first order [medium]
  on tasklist tlt_7a67…9504 "default" (the default; --tasklist picks another)
$ lore task relate --id tsk_d191…45c2 --follows-up tsk_2ad3…f891 --blocked-by tsk_54fe…28f2
lore: wrote tasks/tsk_d191…45c2 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_d191…45c2.json is unchanged)
lore: wrote ~/work/website/.lore/data/knowledge_refs/kfr_b4d3…11aa.json
✓ tsk_d191…45c2 waits for tsk_54fe…28f2
✓ tsk_d191…45c2 follows up tsk_2ad3…f891
```
<!-- /example -->

`lore task unrelate` takes the same flags and removes the relation:

<!-- example: task-tools#unrelate -->
```console
# alice, in website
$ lore task unrelate --id tsk_d191…45c2 --blocked-by tsk_54fe…28f2
lore: wrote tasks/tsk_d191…45c2 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_d191…45c2.json is unchanged)
✓ tsk_d191…45c2 no longer waits for tsk_54fe…28f2
```
<!-- /example -->

## Step 6 — Watch a task

Whoever creates a task, and everyone assigned to it, watches it already (unless the team policy sets `tasks.auto_watch: false`), so the first `watch` below says so. `unwatch` stops the changes reaching your inbox, and `watch` starts them again:

<!-- example: task-tools#watch -->
```console
# alice, in website
$ lore task watch --id tsk_54fe…28f2
already watching tsk_54fe…28f2
$ lore task unwatch --id tsk_54fe…28f2
lore: wrote tasks/tsk_54fe…28f2 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_54fe…28f2.json is unchanged)
✓ stopped watching tsk_54fe…28f2
$ lore task watch --id tsk_54fe…28f2
lore: wrote tasks/tsk_54fe…28f2 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_54fe…28f2.json is unchanged)
✓ watching tsk_54fe…28f2
```
<!-- /example -->

## Step 7 — Read a task's history

`lore task history --id <id>` lists each change with its time and who made it: status changes, field changes and claims. Older entries are folded; `--git` also reads earlier versions of the row from git history for the full detail.

<!-- example: task-tools#history -->
```console
# alice, in website
$ lore task edit --id tsk_54fe…28f2 --priority high
lore: wrote tasks/tsk_54fe…28f2 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_54fe…28f2.json is unchanged)
✓ tsk_54fe…28f2 updated
$ lore task done --id tsk_54fe…28f2 --no-lesson
lore: wrote tasks/tsk_54fe…28f2 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_54fe…28f2.json is unchanged)
✓ tsk_54fe…28f2 done
$ lore task history --id tsk_54fe…28f2
2026-11-02 10:00  alice               created
2026-11-02 10:00  alice               status todo → in_progress
2026-11-02 10:02  alice               priority urgent → high
2026-11-02 10:02  alice               status in_progress → done (open_items_at_close: 0)
2026-11-02 10:02  alice               close_reason none → completed
```
<!-- /example -->

## Step 8 — Save work you repeat as a template

A template is a task tree in YAML: a title, optional checklist and sub-tasks (`after: [0]` makes a sub-task wait for an earlier sibling by position), and `args` that fill `${arg:name}` placeholders when you use it. Alice writes one for every release of the shop:

<!-- example: task-tools#template-add -->
```console
# alice, in website
$ cat ~/release.yaml
name: release
description: Ship one release of the shop
args:
  version: {type: text, required: true}
task:
  title: "Release ${arg:version}"
  priority: high
  checklist:
    - "CHANGELOG has ${arg:version}"
    - "Tag pushed"
  children:
    - {title: "Bump the version to ${arg:version}"}
    - {title: "Run the tests", after: [0]}
    - {title: "Deploy to the shop", after: [1]}
$ lore task template add --name release --from-file ~/release.yaml
lore: wrote ~/work/website/.lore/data/task_templates/ttp_9a34…b418.json
✓ ttp_9a34…b418 — template release: 1 task, 3 sub-tasks, 2 checklist items
$ lore task template list
release  repo  1 task, 3 sub-tasks, 2 checklist items
$ lore task template show --name release
# ttp_9a34…b418 (repo)
name: release
description: Ship one release of the shop
args:
    version:
        type: text
        required: true
        default: ""
task:
    title: Release ${arg:version}
    body: ""
    priority: high
    repo: ""
    estimate: ""
    gate: ""
    role: ""
    checklist:
        - CHANGELOG has ${arg:version}
        - Tag pushed
    after: []
    children:
        - title: Bump the version to ${arg:version}
          body: ""
          priority: ""
          repo: ""
          estimate: ""
          gate: ""
          role: ""
          checklist: []
          after: []
          children: []
        - title: Run the tests
          body: ""
          priority: ""
          repo: ""
          estimate: ""
          gate: ""
          role: ""
          checklist: []
          after:
            - 0
          children: []
        - title: Deploy to the shop
          body: ""
          priority: ""
          repo: ""
          estimate: ""
          gate: ""
          role: ""
          checklist: []
          after:
            - 1
          children: []
```
<!-- /example -->

`template show` prints the saved form with every field, the empty ones too; that is the shape `template edit --from-file` reads back. `lore task template add --name <name> --from <tsk>` saves an existing task and its sub-tasks as a template instead of a file. Use a template with `lore task add --template <name> --arg name=value`. A required argument that is missing is refused, and `--dry-run` shows the tasks without writing them:

<!-- example: task-tools#template-use -->
```console
# alice, in website
$ lore task add --template release
ERROR: [E_TEMPLATE] task template: argument version is required: --arg version=<value>
(exit 23)
$ lore task add --template release --arg version=1.4 --dry-run
would add 4 tasks (template release):
  Release 1.4
    Bump the version to 1.4
    Run the tests
    Deploy to the shop
$ lore task add --template release --arg version=1.4
lore: wrote 4 live row(s) on this clone's refs/lore/live (not pushed yet; their files are unchanged)
✓ tsk_bfed…eeea — Release 1.4 [high]
  on tasklist tlt_7a67…9504 "default" (the default; --tasklist picks another)
  template release: added 1 task and 3 sub-tasks
```
<!-- /example -->

`rename` keeps the template's id, `edit <name> --from-file <file>` replaces its content, and `delete` moves it to the trash like any row:

<!-- example: task-tools#template-rename-and-delete -->
```console
# alice, in website
$ lore task template rename --name release --new-name shop_release
lore: wrote ~/work/website/.lore/data/task_templates/ttp_9a34…b418.json
✓ ttp_9a34…b418 — template release renamed to shop_release
$ lore task template list
shop_release  repo  1 task, 3 sub-tasks, 2 checklist items
$ lore task template delete --name shop_release --yes
lore: wrote ~/work/website/.lore/data/task_templates/ttp_9a34…b418.json
✓ ttp_9a34…b418 — template shop_release moved to the trash (restore: lore trash restore --id ttp_9a34…b418)
```
<!-- /example -->

## Step 9 — Make a task repeat

`lore task add --recur '<rule>'` makes a task part of a series. Rules read like `every day`, `every weekday`, `every week`, `every month on 1`, `every mon,thu`, `every 3 months`, and `every! 14 days` (counted from when you finish, not from the due date); add `until <date>` or `for N` to end it. `lore task recur show` prints the rule and the next dates:

<!-- example: task-tools#recur -->
```console
# alice, in website
$ lore task add --title 'Back up the shop database' --recur 'every week'
lore: wrote tasks/tsk_376c…8e1a on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_376c…8e1a.json is unchanged)
✓ tsk_376c…8e1a — Back up the shop database [medium]
  recurs: every week → next: 2026-11-02, 2026-11-09, 2026-11-16 (series rcs_67e2…291e, #1)
  on tasklist tlt_7a67…9504 "default" (the default; --tasklist picks another)
$ lore task recur show --id tsk_376c…8e1a
tsk_376c…8e1a Back up the shop database
  rule: every week
  series: rcs_67e2…291e #1 (active)
  next: 2026-11-02, 2026-11-09, 2026-11-16
```
<!-- /example -->

Finishing an instance creates the next one, and `lore task list --series <rcs_…> --all` lists the whole series:

<!-- example: task-tools#recur-done -->
```console
# alice, in website
$ lore task done --id tsk_376c…8e1a --no-lesson
lore: wrote tasks/tsk_376c…8e1a on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_376c…8e1a.json is unchanged)
lore: wrote tasks/tsk_8567…366f on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_8567…366f.json is unchanged)
✓ tsk_376c…8e1a done
  every week due 2026-11-02
  next instance: tsk_8567…366f due 2026-11-09 (series rcs_67e2…291e, #2)
$ lore task list --series rcs_67e2…291e --all --sort due_at
✓ tsk_376c…8e1a Back up the shop database [medium] due:2026-11-02
○ tsk_8567…366f Back up the shop database [medium] due:2026-11-09
```
<!-- /example -->

`recur skip` closes this instance as skipped and creates the next one:

<!-- example: task-tools#recur-skip -->
```console
# alice, in website
$ lore task recur skip --id tsk_8567…366f
lore: wrote tasks/tsk_8567…366f on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_8567…366f.json is unchanged)
lore: wrote tasks/tsk_71f0…c07d on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_71f0…c07d.json is unchanged)
✓ tsk_8567…366f skipped
  every week due 2026-11-09
  next instance: tsk_71f0…c07d due 2026-11-16 (series rcs_67e2…291e, #3)
```
<!-- /example -->

`recur edit` changes the rule for this instance and the later ones:

<!-- example: task-tools#recur-edit -->
```console
# alice, in website
$ lore task recur edit --id tsk_71f0…c07d --rule 'every! 14 days'
lore: wrote tasks/tsk_71f0…c07d on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_71f0…c07d.json is unchanged)
✓ tsk_71f0…c07d updated
  recurs: every! 14 days → next: 2026-11-16, 2026-11-30, 2026-12-14 (series rcs_67e2…291e, #3)
$ lore task recur show --id tsk_71f0…c07d
tsk_71f0…c07d Back up the shop database
  rule: every! 14 days
  series: rcs_67e2…291e #3 (active)
  next: 2026-11-16, 2026-11-30, 2026-12-14
```
<!-- /example -->

`recur stop` ends the series: the current instance stays, no further one is made. It asks first:

<!-- example: task-tools#recur-stop -->
```console
# alice, in website
$ lore task recur stop --id tsk_71f0…c07d
ERROR: [E_NEEDS_CONFIRM] task recur stop tsk_71f0…c07d?
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore task recur stop --id tsk_71f0…c07d --yes
lore: wrote tasks/tsk_71f0…c07d on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_71f0…c07d.json is unchanged)
✓ tsk_71f0…c07d series rcs_67e2…291e stopped (no further instances; this one stays)
$ lore task recur show --id tsk_71f0…c07d
tsk_71f0…c07d Back up the shop database
  rule: every! 14 days
  series: rcs_67e2…291e #3 (stopped)
```
<!-- /example -->

## Step 10 — Sweep out stale tasks

Three months later, many of the launch tasks have not been touched. `lore task stale` lists open tasks with no change for 60 days (`--days N` with N 1 or more, or the `tasks.stale_days` setting; `--days 0` is refused, never read as the default). It leaves out tasks that are claimed, in an active mission, due in the future, with open sub-tasks, or repeating. `--close --dry-run` shows what would be cancelled, and `--close --yes` cancels them with close reason `stale`, as one batch you can look at with `lore batch show`:

<!-- example: task-tools#stale -->
```console
# alice, in website
$ lore task stale
9 tasks with no change for 60 days (excluded: claimed, in an active mission, future due, open sub-tasks, recurring)
  tsk_b4f4…a5b0  Send a test to the client
  tsk_28f8…03df  Check the shop on a phone
  tsk_2537…6bbb  Shoot the shirts
  tsk_b69e…5ed7  Shoot the mugs
  tsk_0aae…a61f  Edit the photos
  tsk_d191…45c2  Thank-you email after the first order
  tsk_ada3…c018  Bump the version to 1.4
  tsk_78c8…5b6c  Run the tests
  tsk_d7c7…7fe6  Deploy to the shop
$ lore task stale --close --dry-run
9 tasks with no change for 60 days (excluded: claimed, in an active mission, future due, open sub-tasks, recurring)
  tsk_b4f4…a5b0  Send a test to the client
  tsk_28f8…03df  Check the shop on a phone
  tsk_2537…6bbb  Shoot the shirts
  tsk_b69e…5ed7  Shoot the mugs
  tsk_0aae…a61f  Edit the photos
  tsk_d191…45c2  Thank-you email after the first order
  tsk_ada3…c018  Bump the version to 1.4
  tsk_78c8…5b6c  Run the tests
  tsk_d7c7…7fe6  Deploy to the shop
would cancel them with close_reason stale (nothing written, --dry-run)
$ lore task stale --close --yes
lore: wrote 9 live row(s) on this clone's refs/lore/live (not pushed yet; their files are unchanged)
✓ 9 tasks cancelled (close_reason stale, batch bat_06b1…3d1c); lore batch show --id bat_06b1…3d1c
$ lore task list
○ tsk_2ad3…f891 Write the launch email [high] 0/0
○ tsk_162d…9666 Pick the product photos [low] 0/0
○ tsk_bfed…eeea Release 1.4 [high] 0/0
○ tsk_71f0…c07d Back up the shop database [medium] due:2026-11-16
```
<!-- /example -->

## Try it

Every example on this page comes from one script. In a checkout of lore you can run it yourself; it builds lore and works in a throwaway folder, so nothing on your machine changes:

```console
$ tests/docs/run-examples.sh task-tools
```

The commands and their output are written to `tests/docs/transcripts/task-tools.txt`. The quickest single check is the board after the first three tasks:

<!-- example: task-tools#board only=5 -->
```console
# alice, in website
$ lore task board
todo (2)
  tsk_2ad3…f891  Write the launch email
  tsk_162d…9666  Pick the product photos
in_progress (1)
  tsk_54fe…28f2  Set up the payment provider
done (0)
cancelled (0)
blocked (0)
```
<!-- /example -->

## Pitfalls

- **A checklist item is not a task.** It has no owner, no status and cannot be claimed. If anyone might ever pick it up alone, make it a sub-task (`checklist promote`, or `lore task split`).
- **Checklist positions move.** After `rm` or `promote` the numbers close up. In scripts use the `chk_` id that `checklist list` prints.
- **`checklist import` and `recur stop` ask first.** Without a terminal they stop with `E_NEEDS_CONFIRM` until you add `--yes`.
- **`relate --duplicates` cancels the task.** It is the duplicate that closes, not the one it points at; pick the order with care.
- **`lore task show` does not print relations.** A follow-up shows as a `ref:` line; a `--blocked-by` relation shows in `lore mission status` ("waits for …"), not in `task show`.
- **A template refuses unknown keys and undeclared arguments.** A typo in the YAML, or a `${arg:x}` with no `x` under `args`, is refused with `E_TEMPLATE` when you add the template, and an unknown `--arg` when you use it.
- **A series moves on only when an instance closes.** A repeating task's next instance only appears when you finish or skip the current one; a series that is never closed never moves on.
- **`task stale --close` cancels in bulk.** Run `--dry-run` first. The batch can be undone with `lore batch undo --id <bat_…>` (see [Trash and purge](../sync/trash-and-purge.md)).

## Related

- [Tasks](tasks.md): add, list, start, finish and change tasks.
- [Commitment and snooze](commitment-and-snooze.md): proposals, ideas and tasks for later.
- [Missions and plans](missions-and-plans.md): group tasks under a goal; plan files with sub-tasks.
- [Run log](run-log.md): time spent on a task.
- [Agents](../team/agents.md): claims and leases, which `task stale` leaves alone.
