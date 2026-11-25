# Held-out evaluation of lore ask

The recall gate (`tests/bench/recall`) and its set-5 and set-6 cases each test the questions a fix was written for. On three blind benchmark sets in a row "lore ask alone enough" fell (30, 20, 16 of 40) while those cases stayed green: each fix fitted its own questions. This bench judges `lore ask` on questions nobody fitted a fix to. A synthetic store of the benchmark's shape is generated from a fixed seed, a writer who never sees the ranking code writes questions from a dump of it, and the questions are split in two halves: the tune half is kept here (`tune.jsonl`) and may be used while fixing; the report half is kept outside the repository and is only run to report a score before and after a change.

## Run it

```sh
task bench:heldout                                   # the tune half, tests/bench/heldout/tune.jsonl
HELDOUT_FILE=/path/to/report.jsonl task bench:heldout  # any other question file
task bench:heldout:dump                              # the dump for a question writer, tmp/bench/heldout-dump
HELDOUT_DUMP=/path/to/dump task bench:heldout:dump
```

`task bench:heldout` builds `tmp/bin/lore-bench` from `./cmd/lore`, writes the store with that lore into a temp folder (a code repo attaching the store `kb`), asks every question once with `lore ask --query "<question>" --json` from the repo (as the benchmark's answerer does, default budget) and once more as text to time the warm call, writes `tmp/bench/heldout.md` and prints the score line. It fails on a bad question file or a broken setup, never on the score: it is a report. An empty question file runs the setup and reports 0/0. The plain `go test ./tests/bench/heldout/` runs the unit tests of the generator, the dump, the question loader, the scorer and the report; it needs no lore binary.

| variable | default | what it does |
|---|---|---|
| `LORE_HELDOUT_FILE` (task: `HELDOUT_FILE`) | `tests/bench/heldout/tune.jsonl` | the question file; a relative path is read from the repo root |
| `LORE_HELDOUT_REPORT` | `tmp/bench/heldout.md` | where the report is written |
| `LORE_HELDOUT_DUMP` (task: `HELDOUT_DUMP`) | `tmp/bench/heldout-dump` | where `bench:heldout:dump` writes the dump |
| `LORE_BENCH_BIN` | build `./cmd/lore` | the lore binary to measure |

## The store (seed 237, every name made up)

- `kb/app`, the product: 12 as-built notes (`architecturenote`), one per subsystem (an auth service, a job queue, a webhook sender, a catalog import, file storage, a scheduler, a feed builder, a billing bridge, a search indexer, a notification sender, an audit log, a rate limiter), each 5-15 kB with many `##` sections, most of them filler made of the store's common words (worker, command, run, key, table, job, retry, deploy, config); and 162 short rows: memories naming an exact command with its flag, a constant, an environment variable; hotfixes naming an error, its cause and fix; decisions with their reason.
- `kb/marlow`, another client: two long notes and 24 short rows using the same words about that client's own queue, webhooks and imports.
- `kb/org`: 20 `code_repo` resource cards whose names and purposes share the common words of the notes (`webhook_relay`, `job_queue_ui`, `import_runner` …).
- Every row carries `bench_key` (the stable key a question names its answer by), `fact_topic`, `fact_state` and `fact_verified_at`.

The shapes the blind sets 5-7 missed are all in it: a short exact row next to long notes sharing its common words; the subject sentence of a question in a section in the last third of a long note; a resource name sharing a question word; two rows contradicting on a value (per subsystem a memory giving a default and a newer hotfix saying it has none; for every third subsystem the long note repeats the old value); sibling questions (two subsystems with the same command shape); exact facts a long note also names in passing. The same seed gives the same store byte for byte; changing the generator or the seed changes the rows the question files were written from, so a change there needs new questions (the loader refuses an answer sentence the row no longer holds).

## Question files

JSON lines, one question per line (blank lines skipped):

```json
{"id": "Q-001", "question": "…", "answer_key": "app-quillq-m01", "answer_sentence": "an exact substring of that row's title or body", "kind": "exact"}
```

`kind` is one of `exact`, `default`, `subject`, `why`, `conflict`, `sibling`, `resource`, `howto` (the dump's `_FORMAT.md` says what each means; a `resource` question's `answer_key` is `card-<name>` and its sentence is part of the card's purpose). The loader checks every line before anything runs: fields set, ids unique, kind known, `answer_key` a row or card of the store, `answer_sentence` inside that row (case and white space ignored).

## The score

Per question, from `lore ask --json`: **ask alone enough** = the answering row (or card) is in the answer and `answer_sentence` is inside what ask printed for it (the row's title and the body ask printed, cut or whole; a card's field values); **answer shown** = it is in the answer at all; **answer first** = it is the first item; **cards when rows fill the slots** = a `code_repo` card was printed although knowledge rows filled the first three places (not counted for `resource` questions); **warm** = the wall time of the second, text call. The report lists the totals, the totals per kind, and every question that was not enough alone with why (not shown, or shown at place N without the sentence) and the first three items it showed instead, by `bench_key`.
