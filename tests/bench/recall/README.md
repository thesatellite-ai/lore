# Recall gate: lore beats grep (G-1)

A permanent benchmark that asks one personal store 70 questions (50 normal, 20 harder) and asks the same 70 of a curated markdown tree with rg/grep + cat. Accuracy comes first (owner 2026-11-21): the answer must be in the top 3 of what lore returns, the first item right, no stale fact, little noise. Then lore has to win on every question: one call, a fast answer, and less text to read. It exists because "lore ties grep" is not good enough to be worth using; the gate keeps the win from slipping back.

## Run it

```sh
task bench:recall
```

That builds `tmp/bin/lore-bench` from `./cmd/lore` (`CGO_ENABLED=0`, a release-shaped binary: no test tags), generates the data, runs both arms, writes the report to `tmp/bench/recall.md`, prints the summary line, and fails (non-zero exit) listing every missed bar. A full run takes a few minutes; most of it is lore's calls (three per question: cold, warm, and the `--json` call the accuracy grade reads) and loading the store.

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

Per question: lore calls (always 1), the cold wall time (the first call after the store changed: before each question one feed row arrives from the side clone, as a sync brings it), the warm wall time (the same call again), the CPU time (user + system) of both calls next to their wall times (on a loaded box the wall time is mostly waiting for a core; the bars stay on wall time), the characters lore printed (stdout + stderr of the warm call), correctness; for grep its calls, wall time, characters and correctness. The first read after loading the store (every folder's cache built from nothing) is timed and logged but is not a question.

Accuracy, first (owner 2026-11-21: retrieval accuracy comes before speed). A third run of the same call with `--json` gives the structured answer; the bench reads it as one ranked list (resources, then tasks, then rows, the order lore prints them) and grades the top of it (`GradeAccuracy` in `accuracy.go`):

- recall@3: every required fact in the first 3 items (the first N when the answer takes N > 3 items); 100% of questions on both sets;
- top-1: the first item holds a required fact and is not stale; at least 90% of all questions;
- stale: an item in the top 3 that presents a forbidden fact as its answer without the current value (a resource field's former value is history, not the answer; a done task is stale); 0 questions;
- noise: an item in the top 3 that holds no required fact and does not name the subject; at most 1 per question.

The grep arm is graded the same way where it applies to a pile of text: present (every required fact in its output) and stale.

The run passes when every accuracy bar and every bar below holds (the raised bar of 2026-11-21):

- every question: 1 lore call, warm under 50 ms, cold under 300 ms, lore's answer correct (70/70, the 20 harder ones included);
- total characters lore printed fewer than grep's;
- lore correct on at least as many questions as grep;
- typed answers (who is X: resources; what I owe: tasks) at least 10x lighter than grep's.

## The report

`tmp/bench/recall.md`: the run (now, seed, rows, lore version, grep tool, load time), the box load average at the first question and after the last, the summary line, PASS or every missed bar (accuracy bars first), the accuracy table (per type and per set: recall@3, top-1, stale, noise, grep present and stale), then the speed and size table per question type (correct, calls, warm and cold max, characters and the lore/grep character ratio), and a table per question (the lore call, recall@3, top-1, stale and noise, its times and characters, what it missed, the grep arm's calls and characters). The summary line is the one the coordinator copies into `GATE PASS lore beats grep: <line>`:

```
recall@3 R/70, top-1 T/70, stale S, noise X, calls L/G, warm p50/max ms, cold max ms, chars L/G, correct L/G of 70, harder L/20, warm cpu p50/max ms
```

stale counts the questions with a stale fact in the top 3, noise the questions with more than one irrelevant item there.

## Not in the gate yet

The bench is report-only until it passes: it is not in `task check` or the release script. When it passes, add `task bench:recall` (or its `go test -tags bench` line) to the gate steps.
