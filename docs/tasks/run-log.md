# Run log

**In short:** a run is a diary of one try at a piece of work: what you or a coding agent set out to do, each step on the way, and how it ended. `lore run start` opens it, `lore run step` adds a line, `lore run end` closes it, and `lore run replay` reads the whole story back.

## What it is and when you want it

When a fix takes many steps, the end result does not show how you got there. A run keeps that path: "searched for the price, found 9 instead of 19, decided to change only the page, tests pass". Later you, a teammate or another agent can replay it to see what was tried, what was decided and what failed.

Runs suit coding agents well: an agent can log each tool it used and each check it ran, and a person reads the replay afterwards. A person can use runs the same way for a tricky bug.

A run has a *goal* (what it tries to do), an optional task, and *steps*. Each step has a kind:

| Kind | Use it for |
|---|---|
| `tool` | a command or tool that was used, like a search |
| `decide` | a choice that was made, and why |
| `verify` | a check; add `--passed` when it passed |
| `prompt` | a question or instruction given to an agent |
| `reflect` | a thought about what went well or badly |
| `error` | something that failed |
| `note` | anything else (the default) |

A run ends with an outcome: `success`, `partial`, `failed` or `cancelled`.

Alice (an agency developer) logs how she fixed a typo on the client's pricing page in `github.com/client/website`.

## Before you start

- lore set up in the repo, and a task to work on (see [Tasks](tasks.md)). A run without a task works too.

<!-- example: runs#task -->
```console
# alice, in website
$ lore task add 'Fix the typo on the pricing page'
✓ tsk_7a67…9504 — Fix the typo on the pricing page [medium]
lore live: (live, pushed)
```
<!-- /example -->

## Step 1 — Start a run

`--goal` says what the run tries to do. `--task` ties it to a task. `--agent` says who does the work; `manual` means a person. The last line of the output is the bare run id, so a script can keep it: `run=$(lore run start … | tail -1)`.

<!-- example: runs#run-start -->
```console
# alice, in website
$ lore run start --task tsk_7a67…9504 --agent manual --goal 'Find and fix the wrong price'
lore sync: imported 1
✓ run_2ad3…f891 started
run_2ad3…f891
```
<!-- /example -->

## Step 2 — Log the steps

Add one step per thing that happened. `--name` is a short label (the tool's name, for example) and `--summary` is one line. For a longer text, such as a list of changed lines, pipe it in with `--payload-stdin`.

<!-- example: runs#run-step -->
```console
# alice, in website
$ lore run step run_2ad3…f891 --kind tool --name grep --summary 'Found 9 instead of 19 in pricing.html'
✓ step #1 [tool/pending] rst_162d…9666
$ lore run step run_2ad3…f891 --kind decide --summary 'Change pricing.html only; the price list file is right'
✓ step #2 [decide/pending] rst_54fe…28f2
$ lore run step run_2ad3…f891 --kind verify --name tests --passed --summary 'The page tests pass'
✓ step #3 [verify/passed] rst_05e9…4603
$ printf 'Changed line 42 of pricing.html\nfrom: 9 a month\nto:   19 a month\n' | lore run step run_2ad3…f891 --kind note --name diff --summary 'What changed' --payload-stdin
✓ step #4 [note/pending] rst_c0f3…b5b8
```
<!-- /example -->

## Step 3 — End the run

`--outcome` is required. `--summary` says what happened in one line. For a `failed` or `partial` run, `--error` records what went wrong.

```console
$ lore run end <run-id> --outcome success --summary 'The price now says 19'
```

lore prints the outcome and how long the run took.

## Step 4 — Show and replay a run

`lore run show` prints the run's details: its kind, status, task, goal, outcome and how many steps it has (the start and end times are left out here).

<!-- example: runs#run-show keep=^(?!\s+(started|ended):) -->
```console
# alice, in website
$ lore run show run_2ad3…f891
run_2ad3…f891
  kind:     task
  status:   success
  …
  task:     tsk_7a67…9504
  agent:    manual
  goal:     Find and fix the wrong price
  outcome:  success
  summary:  The price now says 19
  steps:    4
```
<!-- /example -->

`lore run replay` prints every step in order, with any long text you piped in:

<!-- example: runs#run-replay -->
```console
# alice, in website
$ lore run replay run_2ad3…f891
run_2ad3…f891  [success]
#1 [tool/pending]  grep
  Found 9 instead of 19 in pricing.html
#2 [decide/pending]
  Change pricing.html only; the price list file is right
#3 [verify/passed]  tests
  The page tests pass
#4 [note/pending]  diff
  What changed
Changed line 42 of pricing.html
from: 9 a month
to:   19 a month
```
<!-- /example -->

## Step 5 — Cancel a run

`lore run cancel` is the same as ending it with `--outcome cancelled`. `--reason` becomes the summary.

<!-- example: runs#run-cancel keep=^(?!\s+(started|ended):) -->
```console
# alice, in website
$ lore run start --agent manual --goal 'Try a new logo colour'
✓ run_76bd…3f10 started
run_76bd…3f10
$ lore run cancel run_76bd…3f10 --reason 'The client wants to keep the old logo'
✓ run_76bd…3f10 cancelled
$ lore run show run_76bd…3f10
run_76bd…3f10
  kind:     general
  status:   cancelled
  …
  agent:    manual
  goal:     Try a new logo colour
  outcome:  cancelled
  summary:  The client wants to keep the old logo
  steps:    0
```
<!-- /example -->

## Try it

`lore run list` shows the 50 newest runs, newest first:

<!-- example: runs#run-list -->
```console
# alice, in website
$ lore run list
run_76bd…3f10
run_2ad3…f891
```
<!-- /example -->

## Pitfalls

<!-- ds:block id=sync-localtables-dcpb69pj -->
**Runs stay on your machine.** Runs, their steps and sessions are never written to `.lore/data`, so git does not carry them and teammates cannot replay your runs. After a full run, `lore sync` has nothing to export and `git status` shows nothing new:

<!-- example: runs#run-stays-local -->
```console
# alice, in website
$ lore sync && git status --short
· already in sync
?? .lore/data/task_lists/
```
<!-- /example -->

Other traps:

- **`lore run list` prints only ids.** Use `lore run show <id>` to see what a run was about, or `lore run list --json`.
- **Steps without `--passed` show as `pending`.** Only `verify` steps use `--passed`; for other kinds `pending` just means "no pass or fail".
- **`--agent` takes any word.** lore does not check it. Pick one word per tool and stick to it, so runs are easy to compare.
- **Run times use the real clock.** The start and end times and the length of a run are your computer's time, so two replays of the same work never show the same times.
- **`lore session list` is empty.** It reads a sessions table that no lore command fills today, so it prints `(no sessions)`. Agent sessions for team work come from `lore agent start` instead (see [Related](#related)):

<!-- example: runs#session-list -->
```console
# alice, in website
$ lore session list
(no sessions)
```
<!-- /example -->

## Related

- [Tasks](tasks.md): the work a run belongs to.
- [Link commits](link-commits.md): the commit that finished the task.
- [Agents](../team/agents.md): agents claiming tasks, heartbeats and gates; a run logs what an agent did, a claim says what it holds.
- [Team commands › lore agent start](../team/commands.md#lore-agent-start): give an agent its own identity before it starts work.
