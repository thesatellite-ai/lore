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

Spans from runs, claims and status changes add up the same way: overlapping automatic spans of one worker count once, while manual entries are always added on top. Steps 5 to 7 log and fix time by hand.

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

## Step 5 — Log time by hand

Work done away from lore (a phone call, a meeting) is logged with `lore task time log <task-id> <duration>`. Durations look like `45m`, `1h30m` or `1.5h`; there is no day unit. `--note` says what it was, `--at` sets the day or time (by default the entry ends now) and `--by` logs it for someone else. The entry counts as `MANUAL` time:

<!-- example: runs#time-log -->
```console
# alice, in website
$ lore task time log tsk_7a67…9504 30m --note 'Call with the client about the prices'
✓ logged tme_54fe…28f2  30m  for alice  on 2026-11-02
  hint: this overlaps run time 2026-11-02T10:00:10.000Z to 2026-11-02T10:10:10.000Z (tme_05e9…4603); if it is the same work, remove one: lore task time rm tme_54fe…28f2
lore live: (live, pushed)
$ lore task time show tsk_7a67…9504
tsk_7a67…9504  Fix the typo on the pricing page   (todo)
effort   40m     elapsed  10m     manual  30m   (included in effort)
WORKER                        AUTO      MANUAL    TOTAL     LAST
alice                         10m       30m       40m       2026-11-02 10:10
```
<!-- /example -->

The hint is lore noticing that the new entry overlaps the run's span. If both are the same work, remove one of them, or the task shows the time twice in its effort.

## Step 6 — Fix or remove an entry

`lore task time edit <tme_…>` changes an entry: `--duration` keeps its start, `--start` / `--end` move it, `--note` and `--by` change the rest. An entry made by a run or a status change keeps its original times; only manual entries take new ones.

<!-- example: runs#time-edit -->
```console
# alice, in website
$ lore task time edit tme_54fe…28f2 --duration 45m
✓ updated tme_54fe…28f2  manual  45m (was 30m)
lore live: (live, pushed)
$ lore task time show tsk_7a67…9504
tsk_7a67…9504  Fix the typo on the pricing page   (todo)
effort   55m     elapsed  10m     manual  45m   (included in effort)
WORKER                        AUTO      MANUAL    TOTAL     LAST
alice                         10m       45m       55m       2026-11-02 10:25
```
<!-- /example -->

`lore task time rm` removes an entry and `restore` brings it back. A removed entry is kept as a tombstone, so two copies of the notes merge cleanly:

<!-- example: runs#time-rm-and-restore -->
```console
# alice, in website
$ lore task time rm tme_54fe…28f2 --yes
✓ removed tme_54fe…28f2
lore live: (live, pushed)
$ lore task time restore tme_54fe…28f2
✓ restored tme_54fe…28f2
lore live: (live, pushed)
```
<!-- /example -->

## Step 7 — Stop the clock without changing the status

While a task is `in_progress`, time runs for whoever started it. `lore task time stop` closes your own open span (here after 20 minutes) and leaves the status as it is, for when you step away from a task you have not finished:

<!-- example: runs#time-stop -->
```console
# alice, in website
$ lore task start tsk_7a67…9504
✓ tsk_7a67…9504 started
lore live: (live, pushed)
$ lore task time stop tsk_7a67…9504
✓ stopped your time on tsk_7a67…9504  (20m, entry tme_c0f3…b5b8); status stays in_progress
lore live: (live, pushed)
$ lore task time show tsk_7a67…9504
tsk_7a67…9504  Fix the typo on the pricing page   (in_progress)
effort   1h 15m     elapsed  30m     manual  45m   (included in effort)
WORKER                        AUTO      MANUAL    TOTAL     LAST
alice                         30m       45m       1h 15m    2026-11-02 10:31
$ lore task show tsk_7a67…9504 | head -4
tsk_7a67…9504
  title:    Fix the typo on the pricing page
  status:   in_progress
  priority: medium
```
<!-- /example -->

## Try it

Every example on this page comes from one script. In a checkout of lore you can run it yourself; it builds lore and works in a throwaway folder, so nothing on your machine changes:

```console
$ tests/docs/run-examples.sh runs
```

The commands and their output are written to `tests/docs/transcripts/runs.txt`. At the end, the task shows the run, the manual entry and the span from `task start` to `task time stop` together:

<!-- example: runs#time-stop only=3 -->
```console
# alice, in website
$ lore task time show tsk_7a67…9504
tsk_7a67…9504  Fix the typo on the pricing page   (in_progress)
effort   1h 15m     elapsed  30m     manual  45m   (included in effort)
WORKER                        AUTO      MANUAL    TOTAL     LAST
alice                         30m       45m       1h 15m    2026-11-02 10:31
```
<!-- /example -->

## Pitfalls

<!-- ds:block id=sync-localtables-ukz59e32 -->
**Runs stay on your machine.** The run itself (its goal, outcome, tokens and cost) and sessions are never written to `.lore/data`, so git does not carry them. Only the span a run adds to its task travels. After the cancelled run, which named no task, nothing new is waiting in git:

<!-- example: runs#run-stays-local -->
```console
# alice, in website
$ git status --short
```
<!-- /example -->

Other traps:

- **A run that is never ended adds no time.** Its span is written only by `lore run end` or `lore run cancel`. Close every run you open, even a failed one.
- **There is no command to list or show runs.** A run is a timer, not a diary; read its result on the task with `lore task time show`. Notes worth keeping (what was tried, what was decided) belong in a memory or a decision, see [Rules, memories, decisions](../knowledge/rules-memories-decisions.md).
- **A manual entry that overlaps a run counts twice in effort.** lore prints a hint when you log it; remove the duplicate with `lore task time rm`.
- **`task time stop` stops only your own span.** Someone else working on the same task keeps their clock running; the status does not change either, so the task still shows as `in_progress`.
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
- [Task tools](task-tools.md): board, checklists, templates and the other task tools.
- [Link commits](link-commits.md): the commit that finished the task.
- [Agents](../team/agents.md): agents claiming tasks, heartbeats and gates; a run times what an agent did, a claim says what it holds.
- [Team commands › lore agent start](../team/commands.md#lore-agent-start): give an agent its own identity before it starts work.
