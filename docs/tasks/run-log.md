# Run log

**In short:** a run is one timed try at a piece of work: what you or a coding agent set out to do, and how it ended. `lore run start` opens it, `lore run end` closes it with an outcome, and `lore run cancel` drops it. When the run names a task, its length is added to that task's time, which `lore task time show` reads back.

## What it is and when you want it

A task can take several tries, by a person or by an agent. A run marks one of them: it starts when the try starts and ends when it succeeds, fails or is given up. That gives you how long the work really took, per worker, without anyone typing times by hand.

Runs suit coding agents well: an agent opens a run before it starts on a task and closes it when it is done, so the task shows how long the agent worked on it. A person can use runs the same way.

A run has a *goal* (what it tries to do), an optional task and mission, the *agent* doing the work, and an optional *model*. It ends with an outcome: `success`, `partial`, `failed` or `cancelled`.

Alice (an agency developer) times how long fixing a typo on the client's pricing page in `github.com/client/website` takes.

## Before you start

- lore set up in the repo, and a task to work on (see [Tasks](tasks.md)). A run without a task works too, but then its time goes nowhere.

<!-- example: runs#task -->
```console
# alice, in website
$ lore task add 'Fix the typo on the pricing page'
✓ tsk_7a67…9504 — Fix the typo on the pricing page [medium]
lore live: (live, pushed)
```
<!-- /example -->

## Step 1 — Start a run

`--goal` says what the run tries to do. `--task` ties it to a task. `--agent` says who does the work; `manual` means a person. `--model` records the model an agent reports, and `--retry-of <run-id>` marks a new try after a failed one.

<!-- example: runs#run-start -->
```console
# alice, in website
$ lore run start --task tsk_7a67…9504 --agent manual --goal 'Find and fix the wrong price'
run_2ad3…f891 started
```
<!-- /example -->

## Step 2 — End the run

`--outcome` is required. `--summary` says what happened in one line. For a `failed` or `partial` run, `--error` records what went wrong. An agent can add `--tokens-in`, `--tokens-out` and `--cost`. lore prints how long the run took; here alice worked on the fix for ten minutes.

<!-- example: runs#run-end -->
```console
# alice, in website
$ lore run end run_2ad3…f891 --outcome success --summary 'The price now says 19'
run_2ad3…f891 ended  (10m 0s)
lore live: (live, pushed)
```
<!-- /example -->

The `lore live` line is the task: ending the run wrote its ten minutes onto the task row, which travels with git like any other task change.

## Step 3 — Read the time on the task

`lore task time show <task-id>` prints the task's tracked time: the run's span counts as alice's automatic (`AUTO`) time.

<!-- example: runs#task-time -->
```console
# alice, in website
$ lore task time show tsk_7a67…9504
tsk_7a67…9504  Fix the typo on the pricing page   (todo)
effort   10m     elapsed  10m     manual  0m   (included in effort)
WORKER                        AUTO      MANUAL    TOTAL     LAST
alice                         10m       0m        10m       2026-11-02 10:10
```
<!-- /example -->

Spans from runs, claims and status changes add up the same way; `lore task time log`, `edit`, `rm` and `restore` log or fix time by hand (see `lore task time --help`).

## Step 4 — Cancel a run

`lore run cancel` is the same as ending it with `--outcome cancelled`. `--reason` becomes the summary.

<!-- example: runs#run-cancel -->
```console
# alice, in website
$ lore run start --agent manual --goal 'Try a new logo colour'
run_162d…9666 started
$ lore run cancel run_162d…9666 --reason 'The client wants to keep the old logo'
run_162d…9666 cancelled
```
<!-- /example -->

## Pitfalls

<!-- ds:block id=sync-localtables-dcpb69pj -->
**Runs stay on your machine.** The run itself (its goal, outcome, tokens and cost) and sessions are never written to `.lore/data`, so git does not carry them. Only the span a run adds to its task travels. After the cancelled run, which named no task, nothing new is waiting in git:

<!-- example: runs#run-stays-local -->
```console
# alice, in website
$ git status --short
?? .lore/data/task_lists/
```
<!-- /example -->

Other traps:

- **A run that is never ended adds no time.** Its span is written only by `lore run end` or `lore run cancel`. Close every run you open, even a failed one.
- **There is no command to list or show runs.** A run is a timer, not a diary; read its result on the task with `lore task time show`. Notes worth keeping (what was tried, what was decided) belong in a memory or a decision, see [Rules, memories, decisions](../knowledge/rules-memories-decisions.md).
- **`--agent` takes any word.** lore does not check it. Pick one word per tool and stick to it, so time per worker is easy to compare.
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
- [Agents](../team/agents.md): agents claiming tasks, heartbeats and gates; a run times what an agent did, a claim says what it holds.
- [Team commands › lore agent start](../team/commands.md#lore-agent-start): give an agent its own identity before it starts work.
