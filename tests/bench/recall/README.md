# Recall gate: lore beats grep (G-1)

A permanent benchmark that asks one personal store 50 questions and asks the same 50 of a curated markdown tree with rg/grep + cat. Lore has to win on every question: one call, a fast answer, a correct answer, and less text to read. It exists because "lore ties grep" is not good enough to be worth using; the gate keeps the win from slipping back.

## Run it

```sh
task bench:recall
```

That builds `tmp/bin/lore-bench` from `./cmd/lore` (`CGO_ENABLED=0`, a release-shaped binary: no test tags), generates the data, runs both arms, writes the report to `tmp/bench/recall.md`, prints the summary line, and fails (non-zero exit) listing every missed bar. A full run takes a few minutes; most of it is lore's calls (two per question) and loading the store.

Extra `go test` flags go after `--`, e.g. `task bench:recall -- -timeout 90m`. To run without task:

```sh
CGO_ENABLED=0 go build -o tmp/bin/lore-bench ./cmd/lore
LORE_BENCH_BIN=$PWD/tmp/bin/lore-bench go test -tags bench -count=1 -run '^TestBenchRecall$' -timeout 60m -v ./tests/bench/recall/
```

The plain `go test ./tests/bench/recall/` (part of `go test ./...`) runs only the unit tests of the bench's own parts: the data set's shape and determinism, 50 questions of the right types, that the grep arm answers every question from the tree (so a lore miss is lore's, not the bench's), grading, and the bar checks. It needs no lore binary.

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

50 questions in `questions.go`: five each of the six types the owner asks every day (who is X; project status; decision and why; what do I owe / who owes me; what happened on day D / in week W; everything about X) and four each of five harder types (paraphrase: the question's words are not the row's; a typo in a name; a relative date against the pinned now; a role or a nickname instead of the name; a fact that changed over time).

Each question lists its required facts and its forbidden facts. Grading (`Grade`) is the same for both arms: correct means every required fact is in the output and no forbidden fact is, unless the question's current value is in the output too (an output that shows the change tells the reader which value is current); a partial answer is wrong. Matching ignores case.

The lore arm makes exactly one lore call per question, from the home folder: the call an agent makes today, chosen per question type in the `loreArm` table at the top of `questions.go` (`lore resource show`, `lore search --full`, `lore task list --meta`, `lore memory list --since --until`). When `lore ask` lands (G-3), switch a type to it there, in one line.

The grep arm runs the commands a careful agent would run, one by one with bash in the tree's root, and counts each as a call: find the files that name the subject, then read the right one; `rg -n` on the one file that holds the answer type; `cat` the journal files of the day or week (the agent works out the date itself). It uses `rg` when it is installed and `grep` with the same meaning when it is not; the report says which.

## What is measured, and the bars

Per question: lore calls (always 1), the cold wall time (the first call after the store changed: before each question one feed row arrives from the side clone, as a sync brings it), the warm wall time (the same call again), the characters lore printed (stdout + stderr of the warm call), correctness; for grep its calls, wall time, characters and correctness. The first read after loading the store (every folder's cache built from nothing) is timed and logged but is not a question.

The run passes when every bar holds (hunt beats README, owner 2026-11-21, raised bar):

- every question: 1 lore call, warm under 50 ms, cold under 300 ms, lore's answer correct (50/50, the 20 harder ones included);
- total characters lore printed fewer than grep's;
- lore correct on at least as many questions as grep;
- typed answers (who is X: resources; what I owe: tasks) at least 10x lighter than grep's.

## The report

`tmp/bench/recall.md`: the run (now, seed, rows, lore version, grep tool, load time), the summary line, PASS or every missed bar, a table per question type (correct, calls, warm and cold max, characters and the lore/grep character ratio), and a table per question (the lore call, its times and characters, what it missed, the grep arm's calls and characters). The summary line is the one the coordinator copies into `GATE PASS lore beats grep: <line>`:

```
calls L/G, warm p50/max ms, cold max ms, chars L/G, correct L/G of 50, harder L/20
```

## Not in the gate yet

The bench is report-only until it passes: it is not in `task check` or the release script. When it passes, add `task bench:recall` (or its `go test -tags bench` line) to the gate steps.
