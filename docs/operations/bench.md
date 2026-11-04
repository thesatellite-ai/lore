# Check that your notes help, with `lore bench`

**In short:** `lore bench` is a small exam for your notes. You write test questions with an automatic marker, then lore asks a model each question twice: once without your notes and once with them. If the answers with your notes pass more often, your notes are doing their job.

## What it is and when you want it

You wrote a rule, "Run the tests before you push". Does an agent that reads your notes actually follow it? `lore bench` lets you measure that instead of guessing. The words it uses:

| Word | Everyday meaning |
|---|---|
| **eval** (test question) | One question, like "Write a short checklist for pushing a change.", plus how to mark the answer. It can point at the rule it checks. |
| **grader** (marker) | What decides pass or fail. The simplest is a shell command: exit 0 means pass. Others compare with a model answer file or ask a model to judge. |
| **arm** | One way of asking. `baseline` asks with no notes; `with_skill` asks with your rendered notes (your notes written out as one file for the agent) in front of the question. |
| **run** | One exam: every question, in every arm, one or more times. |
| **result** | One answer from a run, with its pass or fail. |

You want this after you change your notes in a big way, to check that agents still follow the rules that matter.

## Before you start

- lore is set up in the repo, with the rules you want to test.
- <!-- ds:block id=env-ollama-host-hj0y341b --> To **run** an exam you need a model. This page uses a local one: a model server on your own machine, such as Ollama, at the address in `OLLAMA_HOST` (default `localhost:11434`). Hosted model: see `lore bench --help`.
- Writing questions and checking markers (Steps 1 to 4) needs no model and no network. The examples on this page run without one; Step 5 is the only step that calls a model, so its command is shown but not run here.

## Step 1 — Write a test question

<!-- ds:block id=grader-output-file-ce8ma3vq -->
Alice (an agency developer) writes one question for her rule. The marker is a shell command that searches the answer for the word "test". lore puts the answer in a file and passes its path in `$OUTPUT_FILE`. `--link` ties the question to the rule, so the question shows the rule's text as it was when the question was written:

<!-- example: bench#eval-add -->
```console
# alice, in website
$ lore bench eval add --code TESTS-001 --category rule-trigger --prompt 'Write a short checklist for pushing a change.' --grader-cmd 'grep -qi test "$OUTPUT_FILE"' --link rule:rul_7a67…9504 --notes 'Passes when the answer says to run the tests'
✓ TESTS-001 bve_2ad3…f891 — category=rule-trigger grader=programmatic
$ lore bench eval list
TESTS-001  rule-trigger       Write a short checklist for pushing a change.
$ lore bench eval show TESTS-001
TESTS-001  bve_2ad3…f891
  category:   rule-trigger
  grader:     programmatic
  links to:   rule rul_7a67…9504
  expected:   baseline 30% / with 85%
Prompt:
Write a short checklist for pushing a change.
Grader spec:
  {
    "cmd": "grep -qi test \"$OUTPUT_FILE\""
  }
Linked body (snapshot):
Run the tests before you push
Notes:
Passes when the answer says to run the tests
```
<!-- /example -->

`--category` groups questions (`rule-trigger`, `hotfix-avoid`, `decision-respect`, `convention`, `capture-back` or `custom`); `--code` names it (lore makes one up when you leave it out). `--expected-baseline` and `--expected-with` say how often you expect each arm to pass; later lore flags markers that miss those numbers by far.

## Step 2 — Check the marker before any model sees it

`lore bench grader test` marks a sample answer from a file, with no model. Try one answer that should pass and one that should fail:

<!-- example: bench#grader-test -->
```console
# alice, in website
$ printf '1. Run the tests\n2. Push the branch\n' > good-answer.txt
$ printf 'Just push it.\n' > bad-answer.txt
$ lore bench grader test TESTS-001 --output-file good-answer.txt
TESTS-001  programmatic
  PASS
  trace: map[cmd_exit:0 stderr: stdout:]
$ lore bench grader test TESTS-001 --output-file bad-answer.txt
TESTS-001  programmatic
  FAIL
  trace: map[cmd_exit:1 stderr: stdout:]
```
<!-- /example -->

A marker that passes everything, or nothing, makes the whole exam worthless, so always check both sides.

## Step 3 — Share questions as files

Questions live in your local database and are not shared through git. To share them, export them to YAML files (plain text, one file per question), commit the folder, and let teammates import it. Import skips questions that already exist unless you add `--replace`:

<!-- example: bench#export-import -->
```console
# alice, in website
$ lore bench eval export --to evals
✓ exported 2 tasks to evals
$ ls evals && cat evals/TESTS-001.yaml
TESTS-001.yaml
TESTS-002.yaml
id: TESTS-001
category: rule-trigger
linked_kind: rule
linked_id: rul_7a67…9504
linked_body: Run the tests before you push
prompt: Write a short checklist for pushing a change.
grader:
    kind: programmatic
    cmd: grep -qi test "$OUTPUT_FILE"
expected_pass_with: 0.85
expected_pass_baseline: 0.3
notes: Passes when the answer says to run the tests
$ sed -i 's/short checklist/three-step checklist/' evals/TESTS-001.yaml
$ lore bench eval import --from evals
✓ imported: 0 added, 0 replaced, 2 skipped
$ lore bench eval import --from evals --replace
✓ imported: 0 added, 2 replaced, 0 skipped
$ lore bench eval list
TESTS-001  rule-trigger       Write a three-step checklist for pushing a change.
TESTS-002  rule-trigger       Write a short checklist for pushing a change.
```
<!-- /example -->

## Step 4 — Copy, retire and delete questions

`duplicate` copies a question under a new code. `archive` hides one from new runs but keeps its history; `--include-archived` shows it again. `delete` removes it for good, needs `--confirm`, and refuses while results still point at it:

<!-- example: bench#archive-delete -->
```console
# alice, in website
$ lore bench eval archive TESTS-002
✓ TESTS-002 archived
$ lore bench eval list
TESTS-001  rule-trigger       Write a three-step checklist for pushing a change.
$ lore bench eval list --include-archived
TESTS-001  rule-trigger       Write a three-step checklist for pushing a change.
TESTS-002  rule-trigger       Write a short checklist for pushing a change. [archived]
$ lore bench eval delete TESTS-002
ERROR: [E_INVALID_INPUT] --confirm required to delete (consider `archive` for soft-delete)
(exit 20)
$ lore bench eval delete TESTS-002 --confirm
✓ TESTS-002 deleted
```
<!-- /example -->

## Step 5 — Run the exam with a local model

This step calls the model, so it is not part of the page's examples. Start the model server, pick a model you have pulled, and pass it with the `ollama:` prefix:

```console
$ export OLLAMA_HOST=localhost:11434
$ lore render
$ lore bench run start --model ollama:qwen3-coder:latest --runs-per-arm 3 --parallel 1
```

`lore render` writes the notes file the `with_skill` arm reads (another file can be named with a flag; see `lore bench run start --help`). `--runs-per-arm 3` asks each question three times per arm, because model answers vary; `--parallel` is how many questions are in flight at once (a small local model is happiest with 1). `--eval-set` picks questions (`all`, a category, or codes such as `TESTS-001,TESTS-002`) and `--arms baseline` runs one arm only.

## Step 6 — Read the results

Before any run, the lists are empty and a report says so:

<!-- example: bench#no-runs-yet -->
```console
# alice, in website
$ lore bench run list
(no runs)
$ lore bench result list
(no results)
$ lore bench report summary
ERROR: [E_NOT_FOUND] no bench runs yet for this project — run `lore bench run start` first
(exit 27)
$ lore bench report trend
(no completed runs)
$ lore bench grader audit
✓ all graders look healthy
```
<!-- /example -->

After a run:

| Command | What it shows |
|---|---|
| `lore bench run list` / `run show <run>` | Past runs, and one run with a verdict per question. |
| `lore bench result list --run <run>` / `result show <id>` | Each answer, the prompt that was sent and the marker's trace. |
| `lore bench result stats [--run <run>]` | Pass, fail and error counts per arm (latest run by default). |
| `lore bench report summary` | Headline numbers for the latest run (pass rate per arm and the gain from your notes). |
| `lore bench report by-category <run>` | Pass rates per category and arm. |
| `lore bench report compare <a> <b>` | Which questions got better or worse between two runs. |
| `lore bench report trend` / `regressions` | Pass rates over time; questions whose with-notes pass rate dropped by more than 5 points. |
| `lore bench report analyze <run>` | A statistics check of whether the gain is more than chance. |
| `lore bench grader audit` | Markers that look too strict, too loose or flaky. |
| `lore bench result regrade <id>` or `--run <run> --all` | Mark stored answers again after you fixed a marker, without asking the model again. |
| `lore bench run retry <run>` | Ask again, as a new run, only the questions and arms that failed. |

## Pitfalls

- **The marker must read `$OUTPUT_FILE`.** A command that reads from standard input, such as plain `grep -qi test`, sees nothing and fails every answer, even a good one:

<!-- example: bench#grader-stdin-trap only=2,3 -->
```console
# alice, in website
$ lore bench eval edit TESTS-002 --grader-cmd 'grep -qi test'
✓ TESTS-002 updated
$ lore bench grader test TESTS-002 --output-file good-answer.txt
TESTS-002  programmatic
  FAIL
  trace: map[cmd_exit:1 stderr: stdout:]
```
<!-- /example -->

- **`grader test` exits 0 even on FAIL.** Read the `PASS` / `FAIL` line, not the exit code, in scripts.
- **Always pass `--model` with `ollama:` for a local model.** Without the prefix lore sends the questions to a hosted model instead (see `lore bench --help`). The same holds for `--grader-judge` on markers of kind `llm-judge`: give them an `ollama:` model too, or they call a hosted one.
- **The linked rule text is a snapshot.** If you change the rule later, the question still shows the old text. Edit the question (`lore bench eval edit`) or re-add it.
- **Questions and results are per machine.** They are not in `.lore/data`, and a rebuild of the database from `.lore/data` starts without them. Export questions you want to keep (Step 3).
- **`--budget-cap` only counts estimated cost** for hosted models; with a local model the cost is 0.

## Related

- [Render: give agents the context](../knowledge/render-and-agent-files.md)
- [Rules, memories and decisions](../knowledge/rules-memories-decisions.md)
- [Back up, restore and repair](backup-restore-repair.md)
- [Environment variables](../reference/environment.md)
- [Operations index](README.md)
