# Recall gate: lore beats grep (G-1)

A permanent benchmark that asks one personal store 70 questions (50 normal, 20 harder) and asks the same 70 of a curated markdown tree with rg/grep + cat. Accuracy comes first (owner 2026-11-21): the answer must be in the top 3 of what lore returns, the first item right, no stale fact, little noise. Then lore has to win on every question: one call, a fast answer, and less text to read. It exists because "lore ties grep" is not good enough to be worth using; the gate keeps the win from slipping back.

## Run it

```sh
task bench:recall
```

That builds `tmp/bin/lore-bench` from `./cmd/lore` (`CGO_ENABLED=0`, a release-shaped binary: no test tags), generates the data, runs both arms, writes the report to `tmp/bench/recall.md`, prints the summary line, and fails (non-zero exit) listing every missed bar. A full run takes a few minutes; most of it is lore's calls (five per question: cold, three warm, and the `--json` call the accuracy grade reads) and loading the store.

Extra `go test` flags go after `--`, e.g. `task bench:recall -- -timeout 90m`. To run without task:

```sh
CGO_ENABLED=0 go build -o tmp/bin/lore-bench ./cmd/lore
LORE_BENCH_BIN=$PWD/tmp/bin/lore-bench go test -tags bench -count=1 -run '^TestBenchRecall$' -timeout 60m -v ./tests/bench/recall/
```

The plain `go test ./tests/bench/recall/` (part of `go test ./...`) runs only the unit tests of the bench's own parts: the data set's shape and determinism, 70 questions of the right types (each with a subject, and an accuracy bar it can reach), that the grep arm answers every question from the tree (so a lore miss is lore's, not the bench's), grading (text and accuracy), and the bar checks (accuracy first). It needs no lore binary.

## Settings (environment)

| variable | default | what it does |
|---|---|---|
| `LORE_BENCH_BIN` | build `./cmd/lore` into the run folder | the lore binary to measure (an absolute path is safest) |
| `LORE_BENCH_NOW` | today (UTC) | the pinned "now", `YYYY-MM-DD`: every date in the data and every relative-date question ("yesterday", "two Fridays ago") resolves against it; lore gets it as `LORE_NOW` too |
| `LORE_BENCH_SEED` | `46` | the data set's seed; the same seed and now give the same data |
| `LORE_BENCH_REPORT` | `<repo>/tmp/bench/recall.md` | where the report is written |
| `LORE_BENCH_CALL_TIMEOUT` | `20s` | a question's lore call is stopped after this long (a Go duration); far above every bar, so a stopped call changes no verdict (it counts as wrong and its time as the limit), and a slow lore still ends the run |
| `LORE_BENCH_DIR` | a temp folder, removed after the run | keep the run here (HOME with the store clone under `home/.lore/stores`, the bare remote under `srv/`, the side clone the bulk rows come from under `side/`, the markdown tree under `tree/`) to poke at it afterwards; the folder is emptied first |

The bench runs every lore and git it starts (the setup writes, the lore arm, the cold and warm calls) in one fixed environment (`HarnessEnv`: HOME, PATH, TMPDIR, `TZ=UTC`, LANG, NO_COLOR, `LORE_NOW`, `LORE_HUMAN=1`), never the caller's, so it gives the same result from a terminal, CI or inside an agent session, where lore would otherwise detect the agent from the parent processes and apply its agent rules to the setup writes. The blind agent run's `lore` wrapper uses the same environment without `LORE_HUMAN`, so the agent's own calls stay the agent's.

## The data (seeded, synthetic)

One personal store `me` with the folders `work`, `life`, `journal` and `feeds`, about 6,200 rows:

- 40 people, 36 projects and 6 companies as resources of the kinds `person`, `project`, `company` in `me/work` (people carry role, company, nickname, email; one person moved company, so her resource has a field history; one person is the CFO, one the lawyer, three have nicknames such as Bobby for robert-kim).
- 700 notes: per project six dated status updates, one decision with its reason, and for 20 projects a budget that changed (an older and a newer row, the newer one saying "moved to 55k (was 40k)"); 400 meeting notes naming a person and a project; life notes, three of them facts that changed (dentist day, gym, car insurance).
- 500 journal entries, one a day back from yesterday, each with a `day` date metadata value, `valid_at` set to the day, and references `${res:person/<name>}` / `${res:project/<name>}` into `me/work` (refs across folders).
- 4,800 feed events naming people and projects (noise both arms have to wade through).
- 110 tasks owed by or to someone (`owed_by` / `owed_to` metadata, a `knowledge_refs` row to the person); every tenth is done and must never be presented as an open debt.

Lore writes the store, the kinds and fields, the resources (`lore resource import`, one call per kind), the move of the one person who changed company (`lore resource edit`), and the first row of every folder. The other rows are row files copied from those first rows, committed with plain git in a second clone of the store (another machine) and brought into lore's clone the way a sync brings them (push, then a fast-forward of lore's clone): the shape of a migrated or adopted folder (rows lore did not write itself, which its cache must index when it reads). No commit is made in lore's clone, so its own commit hook does not run. The bench checks that `lore sql run` sees every memory before it asks anything.

The grep arm's tree holds the same facts the way a tidy person keeps notes: `people/<name>.md`, `companies/<name>.md`, `projects/<name>.md` (owner, company, dated log of status and budget), `decisions.md`, `meetings.md`, `life.md`, `journal/YYYY-MM-DD.md`, `feeds/YYYY-MM.md`, `tasks.md` (`- [ ]` open, `- [x]` done).

## The questions

70 questions in `questions.go`. The normal set is 50 of the six types the owner asks every day: who is X (9), project status (9), decision and why (8), what do I owe / who owes me (8), what happened on day D / in week W (8), everything about X (8); the first five of each type are the ones earlier reports asked. The harder set is 20: four each of five harder types (paraphrase: the question's words are not the row's; a typo in a name; a relative date against the pinned now; a role or a nickname instead of the name; a fact that changed over time).

Each question lists its required facts, its forbidden (stale or done) facts, its subject (the person, project or days it is about) and, when the answer takes more than three items (every task owed to someone, every day of a week), how many. Grading (`Grade`) is the same for both arms: correct means every required fact is in the output and no forbidden fact is, unless the question's current value is in the output too (an output that shows the change tells the reader which value is current); a partial answer is wrong. Matching ignores case.

The lore arm makes exactly one lore call per question, from the home folder: `lore ask --store me --query "<question as written>"` for every type (gate G-3, set per type in the `loreArm` table at the top of `questions.go`).

The grep arm runs the commands a careful agent would run, one by one with bash in the tree's root, and counts each as a call: find the files that name the subject, then read the right one; `rg -n` on the one file that holds the answer type; `cat` the journal files of the day or week (the agent works out the date itself). It uses `rg` when it is installed and `grep` with the same meaning when it is not; the report says which.

## What is measured, and the bars

Per question: lore calls (always 1), the cold wall time (the first call after the store changed: before each question one feed row arrives from the side clone, as a sync brings it), the warm wall time (the same call three more times; the warm time is their median, so the bar needs two of the three under 50 ms: on a box shared with other projects one call can wait for a core, and gate runs missed on a single warm call of a different question each time while its CPU time was 20-30 ms; the report lists the three calls next to the median and the summary line keeps the slowest single call as raw max, so a spike is shown, not hidden; the cold call stays one call; a question that misses the warm or the cold bar is timed once more after the last question, a new feed row first, then one cold and three warm calls, and the bars judge that timing: the three warm calls run back to back, so a burst of load lasting seconds slows them all and the cold call too, and one run failed 17 bars on its last ten questions while each call's CPU time stayed 22-36 ms; a lore that is slow both times still fails, the miss line names both timings, the per-question table shows the first calls and cold time before the judged ones, and the summary line keeps the first calls in raw max and ends with `retimed N`, the number of questions timed again), the CPU time (user + system) of the cold call and the median of the warm calls next to their wall times, counting the lore process and the home's read server during the call (the read server answers a served call while the client only forwards it, so the client alone reads ~20 ms whatever the answer costs; on Linux the bench reads the server's CPU from /proc, elsewhere the column is the client's alone; on a loaded box the wall time is mostly waiting for a core, so wall far above CPU is load and CPU that grew is a slowdown; the bars stay on wall time), the characters lore printed (stdout + stderr of the first warm call; any warm call that fails makes the answer wrong), correctness; for grep its calls, wall time, characters and correctness. The first read after loading the store (every folder's cache built from nothing) is timed and logged but is not a question.

Accuracy, first (owner 2026-11-21: retrieval accuracy comes before speed). A third run of the same call with `--json` gives the structured answer; the bench reads it as one ranked list (resources, then tasks, then rows, the order lore prints them) and grades the top of it (`GradeAccuracy` in `accuracy.go`):

- recall@3: every required fact in the first 3 items (the first N when the answer takes N > 3 items); 100% of questions on both sets;
- top-1: the first item holds a required fact and is not stale; at least 90% of all questions;
- stale: an item in the top 3 that presents a forbidden fact as its answer without the current value (a resource field's former value is history, not the answer; a done task is stale); 0 questions;
- noise: an item in the top 3 that holds no required fact and does not name the subject; at most 1 per question.

The grep arm is graded the same way where it applies to a pile of text: present (every required fact in its output) and stale.

The run passes when every accuracy bar and every bar below holds (the raised bar of 2026-11-21):

- every question: 1 lore call, warm (the median of three warm calls) under 50 ms, cold under 300 ms (a question that misses either is timed once more after the run and judged on that timing), lore's answer correct (70/70, the 20 harder ones included);
- total characters lore printed fewer than grep's;
- lore correct on at least as many questions as grep;
- typed answers: who is X (a resource) at least 10x lighter than grep's; what I owe / who owes me (tasks) lighter than grep's. Why not 10x for both: grep's owe answer is the one `tasks.md` line per task (about 150 characters a question), so 10x lighter would be shorter than one task title; lore's owe answer is one line per task too, so it can be lighter but not ten times lighter (devco D-827, decided for the owner, reversible).

The linked-stores shape (`TestBenchLinkedStoresAsk`, `linked_test.go`; bug hunt H-209): a code repo with 300 commits and four stores attached (an org folder of 20 `code_repo` resources with closed-set fields, three project folders in two more stores, a personal store), ~550 rows with `fact_evidence`, `fact_verified_at` and `fact_topic` on every row and 300 comments. Three questions that project rows answer, each timed after one store write: the cold call under 2 s and the warm call (the median of three) under 200 ms, stretched on a slower machine by its speed factor (bug hunt H-213: the evidence answers are kept per HEAD, so a warm call runs no `git log`; H-214: each store's small tables are read once per open and the kept notes keep their word sets, ~80 ms on the dev box; H-215: a warm call starts no git process once the files are two seconds old, each clone's `git status` is kept while its files are unchanged, ~65-70 ms; the calls here follow a write by less than that). A store of this shape took ~15 s per `lore ask` when every candidate row's evidence cost its own `git log`. The Sync-shaped case rides on it (devco D-963): two as-built architecture notes in `kb/app` (`fact_state=live`), a planned memory and another folder's rows with the same words; each of its two questions must be answered with the as-built note first, before the `code_repo` cards and the other folder's rows (`lore ask` read no architecture notes before). `LORE_BENCH_DIR` keeps it in `<dir>/linked`.

The Sync set-5 cases (`TestBenchSet5*`, one file per finding, `set5_<topic>_test.go`; `task bench:recall` runs them all). `set5_resources_test.go` (bug hunt H-227): a product repo's folder with a rule, a long as-built note with `##` sections, a hotfix, decisions and a memory, and eight `code_repo` resources named after the product and its integration whose purpose repeats the questions' words; each of three set-5-shaped questions must show its answering row in the top 3 rows, at most one `code_repo` item and none before a row.

The Sync set-6 cases (`TestBenchSet6*` and `TestSet6*`, one file per finding, `set6_<topic>_test.go`; `task bench:recall` runs them too). `set6_shortexact_test.go` (bug hunt H-231): seven several-KB as-built notes that each hold the store's common words (sync, cli, commands, workflow, run, key, file, worker) in sections of their own, rows sharing those words, filler, and the short memories and hotfix naming the asked command, flag or error (`wkf trigger / reconcile / schedule`, `files resync`, `vault-rekey`, `set-when-blank`); for each of four set-6-shaped questions the answering row is in ask's top 3 and `lore search`'s first row is in ask's top 3. `set6_cards_test.go` (bug hunt H-234, S6-19): five `code_repo` resources with long purposes that a question word is a typo of (`webhooks`, `receivers`), a part of (`paygate_bridge`) or whose field value it says (`provider paygate`), and the note, memories and task that answer; each of three questions must show its answering rows before any `code_repo` line, with at most one such line, and a question naming `hookrcv_ops` keeps its card. The run logs the card count over the questions that name no resource (the sweep). `set6_forms_test.go` (bug hunt H-235): rows answering in one word form (validation, enqueue, job, re-encrypts) next to a rule, an as-built note and a hotfix about something else that write the questions' other words in the questions' own form, and filler; for three sibling questions on one subject (validation rule / validate / validations) and two off-topic-first shapes (enqueuing jobs, encrypted credentials) ask's first row is the answering row and `lore search`'s first row. `set6_excerpt_test.go` (bug hunt H-232): a long as-built job-queue note cut to its best sections keeps the section holding the question's subject (the words few rows hold), not the ones repeating its common words. `set6_notinmemory_test.go` (bug hunt H-233): a long as-built job-queue note whose duplicate-enqueue sentence sits deep in it, 60 short notes sharing the question's common words and 20 code_repo cards; a question writing code identifiers (`Queue.Enqueue`, `EnqueueOptions.DedupKey`) or a second sentence starting with a capital gets no "not in memory" hint and the note is in the top three with the answering sentence printed or `dedup` in its `left_out`; a question naming an identifier no row holds still gets the hint.

## The report

`tmp/bench/recall.md`: the run (now, seed, rows, lore version, grep tool, load time), the box load average at the first question and after the last, the summary line, PASS or every missed bar (accuracy bars first), the accuracy table (per type and per set: recall@3, top-1, stale, noise, grep present and stale), then the speed and size table per question type (correct, calls, warm and cold max, characters and the lore/grep character ratio), and a table per question (the lore call, recall@3, top-1, stale and noise, its times and characters, what it missed, the grep arm's calls and characters). The summary line is the one the coordinator copies into `GATE PASS lore beats grep: <line>`:

```
recall@3 R/70, top-1 T/70, stale S, noise X, calls L/G, warm p50/max ms (raw max ms), cold max ms, chars L/G, correct L/G of 70, harder L/20, warm cpu p50/max ms, retimed R
```

stale counts the questions with a stale fact in the top 3, noise the questions with more than one irrelevant item there, retimed the questions timed again after the run because their first timing missed a speed bar.

## The blind agent run

`task bench:agent` asks every question of two agents on one small model (`claude-haiku-4-5`) and grades their final answers automatically against the question's facts with `GradeAnswer`, the same grade for both agents. It starts real agents and costs money, so it never runs in `task check` or on push; the release script runs it. The same task then runs the coding gate's blind agent trap run (an agent with lore and one without make the edit a critical hotfix forbids; tests/bench/coding/README.md); both parts always run and the task fails when either misses a bar.

- The lore agent starts in the person's home folder with `skills/SKILL.md` appended to its system prompt and the Bash tool; the `lore` first on its PATH is the measured binary as the bench's person. It is told to start with `lore ask --query "<the question>"` and to use other documented lore read commands only when that is not enough.
- The grep agent starts in the markdown tree's root with the Bash tool and rg, grep, cat, ls, find, head.
- Both are told today's (pinned) date and to quote the facts verbatim; neither is given anything of a question's expected facts (`TestAgentPromptsBlind` holds that). The agent CLI runs bare (no hooks, memory, CLAUDE.md discovery or skills of the machine; the coding gate's trap run instead loads its checkout's own settings only, see tests/bench/coding/README.md), with the arm's deny rules; a command outside the arm's tools (any non-lore command for the lore agent, lore for the grep agent), a failed or stopped run, or an answer from another model than asked counts as wrong.
- `GradeAnswer` grades the facts, not the wording: a required fact counts when the answer holds its text or every word of it that carries meaning (case, markdown and punctuation ignored, `elena-ruiz` matches "Elena Ruiz", "owes you" matches "owes me"), so an agent that rewrites "ulrich-brandt owes me the board deck #61" as "ulrich-brandt owes you: the board deck (task #61)" is right, one that names #16 is not; a forbidden (stale or done) fact counts as written, unless the current value is stated too.
- Per question and arm it records right or wrong (and what was missing), the shell commands, tokens in (uncached + cache write + cache read) and out, cost and wall time.

The bar (owner 2026-11-21): the lore agent gets at least as many right as the grep agent on the normal set and more on the harder set (when the grep agent gets every harder question right there is nothing left to beat, so equal and full passes); every run finishes. Every run's answer, commands, tokens and time are kept next to the report as `<report>-agent.json` (e.g. `tmp/bench/recall-agent.json`), so a miss can be read, and a grader change checked, without running the agents again:

```sh
LORE_BENCH_AGENT_ANSWERS=$PWD/tmp/bench/recall-agent.json LORE_BENCH_NOW=<the run's now> go test -tags bench -count=1 -run '^TestBenchAgentRegrade$' -v ./tests/bench/recall/
```

The section `## Blind agent run` goes into `tmp/bench/recall.md` between its own markers, replacing the previous run's section, so it can run before or after `task bench:recall`.

```sh
LORE_BENCH_AGENT='claude' task bench:agent
LORE_BENCH_AGENT_ONLY=Q01,Q36 task bench:agent   # a few questions only
```

| variable | default | what it does |
|---|---|---|
| `LORE_BENCH_AGENT` | `claude` | the command that starts a Claude Code compatible CLI (split at spaces; the harness appends `-p <question> --bare --tools Bash --model ... --output-format stream-json ...`), e.g. a wrapper that picks the account |
| `LORE_BENCH_AGENT_MODEL` | `claude-haiku-4-5` | the model both agents run on |
| `LORE_BENCH_AGENT_JOBS` | `4` | agents at a time |
| `LORE_BENCH_AGENT_TIMEOUT` | `5m` | one agent run is stopped after this (counts as wrong) |
| `LORE_BENCH_AGENT_TURNS` | `12` | the agent's turn limit |
| `LORE_BENCH_AGENT_ONLY` | every question | comma-separated question ids to ask |
| `LORE_BENCH_AGENT_ANSWERS` | unset | for `TestBenchAgentRegrade`: a saved `<report>-agent.json` to grade again with today's grader (no agent, no store, no cost; same `LORE_BENCH_SEED` and `LORE_BENCH_NOW` as the run) |

The data, `LORE_BENCH_NOW`, `LORE_BENCH_SEED`, `LORE_BENCH_DIR` and `LORE_BENCH_REPORT` work as for the scripted run.

## Not in the gate yet

The bench is report-only until it passes: it is not in `task check` or the release script. When it passes, add `task bench:recall` (or its `go test -tags bench` line) to the gate steps.

The repo README's "Why lore over notes files" section quotes this gate's numbers (accuracy first, then calls, characters and time, then the blind agent run and the coding gate, and either the bars it still misses or that it meets every bar). It quotes the first run that met every bar (v0.1.30, 2026-11-21: recall@3 and top-1 70/70, warm p50/max 32/42 ms, cold max 230 ms; agent run 50/50 and 20/20 against grep's 50/50 and 19/20); a later passing run with clearly different numbers is copied in the same way (`TestReadmeQuotesRecallGate` keeps the section and its rerun command in place).
