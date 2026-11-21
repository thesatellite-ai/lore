# Coding-agent context gate

Lore's core job is to put in front of a coding agent, before it edits a file, every rule it must follow and every trap the team already fell into there. This gate checks exactly that: 30 coding tasks on a synthetic repo, each a file and what to change, each with the rows that must reach the agent written down as ground truth. A rule or hotfix that lore holds but does not show is a broken build or a repeated outage, so the bar is recall 100%; an answer buried in notes that do not apply is not read, so at least 80% of what lore shows has to apply.

## Run it

```sh
task bench:coding
```

That builds `tmp/bin/lore-bench` from `./cmd/lore` (`CGO_ENABLED=0`), writes the repo and its rows through lore, runs both arms for every task, writes the report to `tmp/bench/coding.md`, prints the summary line, and fails (non-zero exit) naming every missed bar. A run takes under a minute. To run without task:

```sh
CGO_ENABLED=0 go build -o tmp/bin/lore-bench ./cmd/lore
LORE_BENCH_BIN=$PWD/tmp/bin/lore-bench go test -tags bench -count=1 -run '^TestBenchCoding$' -timeout 30m -v ./tests/bench/coding/
```

The plain `go test ./tests/bench/coding/` runs only the unit tests of the gate's own parts (the data set's shape, grading, the trap checker, the trap run with a fake agent); it needs no lore binary and starts no agent.

## Settings (environment)

| variable | default | what it does |
|---|---|---|
| `LORE_BENCH_BIN` | build `./cmd/lore` into the run folder | the lore binary to measure (an absolute path is safest) |
| `LORE_BENCH_CODING_REPORT` | `<repo>/tmp/bench/coding.md` | where the report is written |
| `LORE_BENCH_DIR` | a temp folder, removed after the run | keep the run here (HOME under `home/`, the repo under `orbit/`) to poke at it afterwards; the folder is emptied first |

## The data (`data.go`)

One repo, `orbit` (a Go service with a web front end), set up with `lore init`; its rows are written with `lore rule|hotfix|decision|pattern|memory add` into the repo's own store:

- rules on paths (`--activation glob`: migrations, handlers, billing, components, styles, tests, config, generated code, search, queries), rules on a folder (`--scope dir:<folder>`), rules on meaning (`--activation semantic --applies-to "<words>"`: logging, caching, mail), and two always-on rules (they reach the agent through the rendered `.lore/LORE.md`, which the gate checks holds them);
- hotfixes: seven critical traps (a time zone, VAT rounding, a signature compare, an index lock, a connection pool, a cookie, a rate limit), each naming its file or folder or scoped to it, and one low-severity one that applies nowhere;
- decisions, patterns and memories, scoped to folders or not, and noise notes that share words with the tasks on purpose ("the billing team has lunch on Thursdays", "a bigger font on the invoice header").

Each task (`Tasks`) lists `Required` (the must-rules and critical hotfixes that apply: recall over them must be 100%) and `Also` (rows that apply too: decisions, should-rules, scoped notes). A row that is neither, nor an always-on rule, is noise.

## The arms

- **context**: `lore context --for-file <file>` and `lore context --for-prompt "<task>"`, the two calls an agent's hooks make before an edit (the file it opens, the prompt it got); graded on the ids both print together.
- **ask**: `lore ask --query "<task> in <file>"`, the one call an agent makes when it asks. A question naming a file answers first with `lore context --for-file <file> --for-prompt "<the question>"`.

## The bars

- recall: every required row of every task is printed (by id), in each arm;
- precision: at least 80% of the rows each arm prints apply to their task;
- render: every always-on must-rule is in `.lore/LORE.md`.

## The report

`tmp/bench/coding.md`: the run, the summary line, PASS or every missed bar, a table per arm (recall, tasks complete, precision, characters printed) and a table per task (its file, its required rows, and what each arm missed and showed that does not apply). The summary line is the one the coordinator copies:

```
context recall H/R (T/30 tasks), precision A/S (P%); ask recall H/R (T/30 tasks), precision A/S (P%)
```

## Traps (`trap.go`)

`Traps()` lists, for each critical hotfix a task hits, the prompt an agent gets, the files to look at and the wrong edit the hotfix forbids (`time.Now()` in billing/invoice.go, `math.Round(` in billing/tax.go, a signature compared with `==`, a `CREATE INDEX` without `CONCURRENTLY`, `pool.Get()` without a deferred `Close()`, `SameSite=Strict`, login counters in a process map or `sync.Map`). `Trap.FellIn(diff)` reads the agent's `git diff` and says whether it fell in, with the line that did.

## The blind agent trap run (`trapagent.go`, `task bench:agent`)

The context arms show what lore prints; the trap run shows what it changes in an agent's work. For each trap, two agents on one small model (`claude-haiku-4-5`, the recall gate's blind agent harness: bare CLI, no hooks, memory, CLAUDE.md or skills of the machine) get the trap's prompt and make the edit, each in its own checkout of orbit (`RepoFiles`: the trap files hold small, plausible code to edit, and the helpers the hotfixes name, `clock.UTC` and `money.RoundHalfEven`, exist; nothing in the checkout says what a hotfix says):

- **lore agent**: the checkout with the store (every row, written by `lore init` and `lore <kind> add`), `skills/SKILL.md` in its system prompt, and the measured `lore` first on its PATH;
- **plain agent**: the same files with no store, no lore skill, and `lore` denied.

Both have Bash, Read, Edit, Write, Glob and Grep, are told to make the change without asking, committing or building, and are told nothing of the trap. Each checkout's diff (new files included, the `.lore/` folder left out) is graded by `Trap.FellIn`.

The bar (owner 2026-11-21: the agent with lore avoids the trap the hotfix describes where the agent without lore falls into it): the lore agent edits the trap's files and falls into no trap, and every lore run finishes; at least one trap shows the difference (the plain agent fell in where the lore agent did not), else the run shows nothing and fails. A trap the plain agent did not fall into (or whose plain run failed) proves nothing, and the report says so on its row; a plain agent that ran lore makes its row not blind and fails the run.

The section `## Blind agent trap run` goes into `tmp/bench/coding.md` between its own markers (the context gate keeps it when it rewrites the report): the summary line, PASS or every missed bar, and a row per trap (each agent's outcome: fell in with the line, avoided, avoided and applied the hotfix, no edit or failed; the lore agent's lore calls; whether the trap shows anything). Every run's commands, answer, tokens, cost, time and diff are kept next to it as `<report>-traps.json`. The summary line:

```
traps lore/plain: fell in L/P of N, edited L/P, applied the hotfix L/P, failed L/P; shown S (lore avoided, plain fell in), prove nothing U; lore calls C; tokens in L/P out L/P, wall L/P s, cost $L/$P
```

It starts real agents and costs money, so it runs only in `task bench:agent` (after the recall gate's blind run; both always run), never in `task check` or on push. To run only the traps:

```sh
CGO_ENABLED=0 go build -o tmp/bin/lore-bench ./cmd/lore
LORE_BENCH_BIN=$PWD/tmp/bin/lore-bench LORE_BENCH_AGENT='claude' go test -tags bench -count=1 -run '^TestBenchTraps$' -timeout 60m -v ./tests/bench/coding/
LORE_BENCH_TRAP_ONLY=T05,T06 ...   # a few traps only
```

| variable | default | what it does |
|---|---|---|
| `LORE_BENCH_AGENT` | `claude` | the command that starts a Claude Code compatible CLI (extra words are its arguments, e.g. a launcher that picks a login) |
| `LORE_BENCH_AGENT_MODEL` | `claude-haiku-4-5` | the model both agents run on |
| `LORE_BENCH_AGENT_JOBS` | `4` | agents at a time |
| `LORE_BENCH_AGENT_TIMEOUT` | `5m` | one agent run is stopped after this (counts as failed) |
| `LORE_BENCH_TRAP_TURNS` | `25` | the agent's turn limit |
| `LORE_BENCH_TRAP_ONLY` | every trap | comma-separated trap task ids (`T05,T06`) |

`LORE_BENCH_BIN`, `LORE_BENCH_CODING_REPORT` and `LORE_BENCH_DIR` work as for the gate (the run keeps each trap's two checkouts under `traps/<task>/` there).

## Not in the gate yet

The bench is report-only: it is not in `task check` or the release script. When it passes on the dev branch, add `task bench:coding` to the gate steps.
