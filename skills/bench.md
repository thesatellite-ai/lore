# Bench engine — skill reference

DB-backed evaluation engine. Three primary nouns + analysis/meta layer.

## When to use

- User says "set up a benchmark", "evaluate my AI agent", "compare with/without aicoder"
- User wants to **measure** whether captured rules/decisions/hotfixes are actually helping AI agents
- User asks "is this rule working?", "did my last commit regress the bench?", "which tasks are flaky?"

Don't use bench for: ad-hoc one-shot tests (use `lore render` + manual check), unit tests of code (use `go test`).

## Mental model

```
BenchEval     = task TEMPLATE  (independent of any run; lives in DB)
BenchRun      = one EXECUTION  (model + temperature + claude_md_sha)
BenchResult   = one task × arm × attempt  (the unit of statistics)
```

For a typical run: 30 evals × 2 arms × 3 attempts = **180 BenchResult rows** per BenchRun.

## Command tree

```
lore bench
├── eval
│   ├── add        author a task
│   ├── list       enumerate (--category, --linked-kind, --include-archived)
│   ├── show       full row including grader_spec + linked_body_snapshot
│   ├── edit       update field subset
│   ├── archive    soft-delete (preserves history)
│   ├── unarchive  restore
│   ├── delete     hard-delete (--confirm; refuses if results reference it)
│   ├── duplicate  clone (--as=<new-code>)
│   ├── import     bulk-load from YAML directory
│   └── export     dump DB → YAML for git
├── run
│   ├── start      execute (sync) — the heart of the engine
│   ├── list       past runs with Δ rollup
│   ├── show       per-task verdict
│   ├── cancel     mark running run as aborted
│   └── retry      (stub — use `bench run start --eval-set=…`)
├── result
│   ├── list       filter --run --eval --arm --grade [--json]
│   ├── show       prompt_sent + output_received + grader_trace
│   ├── compare    side-by-side two results
│   ├── stats      arm × grade tally for one run; live-safe (works mid-run)
│   ├── regrade    re-grade stored outputs (NO LLM cost) — crucial
│   │              after fixing a grader bug
│   └── replay     (stub — re-run LLM call entirely)
├── report
│   ├── summary    headline numbers for one run (no arg / --latest = newest)
│   ├── compare    two-run diff: which tasks improved/regressed
│   ├── trend      Δ over time, --since=30d --by-model
│   ├── by-category  category × arm breakdown
│   ├── regressions  > 5pp drops between two latest runs
│   └── analyze    paired t-test + Cohen's d + 95% CI
└── grader
    ├── test       run grader against a sample file (no LLM)
    ├── debug      inspect grader_trace for one stored result
    └── audit      flag too-strict / too-loose / error-prone graders
```

## When the user says X, run Y

| User says | Run |
|---|---|
| "set up a benchmark task for rule rul_<id>" | `lore bench eval add --category=rule-trigger --link=rule:rul_<id> --prompt-file=- --grader-kind=programmatic --grader-cmd='…'` |
| "list benchmark tasks" | `lore bench eval list [--category=…] [--json]` |
| "show E1-001" | `lore bench eval show E1-001 [--json]` |
| "edit the grader" | `lore bench eval edit E1-001 --grader-cmd='…'` |
| "soft-delete a task" | `lore bench eval archive E1-001` |
| "import the YAML test set" | `lore bench eval import --from=bench/tasks/` |
| "run the benchmark" | `lore bench run start --model=claude-sonnet-4-6 --runs-per-arm=3 --parallel=8` |
| "cheap run for development" | `lore bench run start --model=claude-haiku-4-5-20251001 --runs-per-arm=1 --parallel=8` |
| "free local run" | `lore bench run start --model=ollama:qwen3-coder:latest --parallel=12` |
| "fast small-model probe" | `lore bench run start --model=ollama:qwen2.5-coder:1.5b --parallel=20` |
| "live stats while bench is running" | `lore bench result stats` (no args = latest run) |
| "stats for a specific run" | `lore bench result stats --run=X` |
| "tell me about the latest run" | `lore bench report summary` (no args = latest) |
| "tell me about run X" | `lore bench report summary X` |
| "is the Δ significant?" | `lore bench report analyze X` |
| "compare two runs" | `lore bench report compare A B` |
| "show trend over time" | `lore bench report trend --since=30d` |
| "are any tasks regressing?" | `lore bench report regressions` |
| "show me a specific result" | `lore bench result show <id>` |
| "I fixed the grader — re-grade without re-spending" | `lore bench result regrade --run=X` |
| "which graders are broken?" | `lore bench grader audit` |
| "why did this result fail?" | `lore bench grader debug <id>` |
| "test a grader without running LLM" | `lore bench grader test E1-001 --output-file=…` |

## Providers + parallelism

`bench run start` routes per-call based on `--model`:

```
--model=claude-haiku-4-5-20251001    →  Anthropic API if $ANTHROPIC_API_KEY set,
--model=claude-sonnet-4-6                 else local `claude` CLI on PATH
--model=claude-opus-4-7
--model=ollama:qwen3-coder:latest    →  local Ollama (default localhost:11434;
--model=ollama:qwen2.5:32b                override via $OLLAMA_HOST)
--model=ollama:qwen2.5-coder:1.5b
--model=ollama:<anything>            →  anything Ollama can run; cost = $0
```

Cost for `ollama:*` models is hard-coded to $0 so `--budget-cap` never trips on local runs.

`--parallel=N` (default 8) controls concurrent LLM calls in flight. Tune to your provider:

| Provider | Reasonable `--parallel` |
|---|---|
| Anthropic API (tier 1) | 4–8 |
| Anthropic API (tier 4) | 16–32 |
| Local `claude` CLI | 4–8 (CLI fork overhead) |
| Ollama (single-model) | 8–20 (Ollama internally serializes per model unless `OLLAMA_NUM_PARALLEL` is bumped) |

Workers share a budget counter — `--budget-cap` is checked between calls but parallel runs can overshoot by up to N × per-call-cost before tripping.

## Hard rules

1. **When benchmarking the SKILL bundle itself, the with-skill arm MUST use the actual `skill/SKILL.md`** (or the full bundle including supporting files). Do NOT synthesize a custom distilled CLAUDE.md — that tests a different artifact than the one end users install. The point of the bench is to verify the *shipped* bundle teaches what we think it teaches.
2. **Use haiku for development** (cheapest). Sonnet for serious runs. Opus only when haiku/sonnet show no signal.
2. **`--runs-per-arm=3` minimum** for statistical analysis; 1 is fine for quick smoke tests.
3. **Grader bugs are the dominant error source.** Always inspect failing tasks first via `bench grader debug` before believing the headline number.
4. **`bench result regrade`** is your friend after fixing a grader — re-runs the grader against stored output, no LLM cost.
5. **Budget cap matters.** Set `--budget-cap=5.00` for any run with --runs-per-arm > 1; aborts cleanly if cost runs away.
6. **Same run = one model.** Cross-model comparisons via N separate runs + `bench report compare`. Multi-model arms get complicated.
7. **`bench-CLAUDE.md` ≠ project CLAUDE.md.** The bench loads a synthesized version at repo root for the with-skill arm; default `--claude-md=bench-CLAUDE.md`.

## Worked flow — bootstrap to first signal

```bash
# 1. Generate evals from captured rules
lore rule list --json | jq -r '.data[] | select(.severity == "must") | .id' | while read rid; do
    lore bench eval add --category=rule-trigger --link=rule:$rid \
        --prompt-file=- --grader-kind=programmatic \
        --grader-cmd='! grep -qE "fmt\.Errorf" "$OUTPUT_FILE"' <<<"Write Go code that exercises this rule"
done

# 2. Build the with-skill context
lore render --target=bench-CLAUDE.md

# 3. Free local smoke (Ollama, $0)
lore bench run start --model=ollama:qwen2.5-coder:1.5b \
    --runs-per-arm=1 --parallel=20 --code=smoke-$(date +%Y%m%d)

# 3b. Or paid haiku smoke (~$0.05)
lore bench run start --model=claude-haiku-4-5-20251001 \
    --runs-per-arm=1 --parallel=8 --code=smoke-$(date +%Y%m%d)

# 4. Live tally during the run (works mid-run)
lore bench result stats           # no args = latest

# 5. Final summary after completion
lore bench report summary          # no args = latest
lore bench report analyze --latest

# 6. If the smoke shows signal (Δ > 5pp), do the real run
lore bench run start --model=claude-sonnet-4-6 --runs-per-arm=3 \
    --parallel=8 --budget-cap=5.00 --code=real-$(date +%Y%m%d)

# 6. Statistical verdict
lore bench report analyze real-$(date +%Y%m%d) --json | jq '.data.p_value, .data.cohens_d'
```

## Anti-patterns

| Don't | Do |
|---|---|
| Trust the headline Δ at n=1 | Use `--runs-per-arm=3` and check Cohen's d + p-value |
| Spend opus on smoke tests | Smoke on haiku ($0.05); only escalate when methodology validated |
| Edit a grader and re-run the whole bench | Use `bench result regrade --run=X` (free) |
| Assume a regression is a real regression | Check `bench grader audit` and inspect failed outputs first |
| Mix arm semantics across runs | One run = one set of arm configurations |
| Skip the linked_body snapshot | Always `--link=kind:id` so the test stays linked to its source rule |

## Output contract (stable schema)

```json
{
  "schema_version": 1,
  "kind": "bench.report.analyze",
  "data": {
    "run_id": "brn_019e...",
    "code": "real-20260511",
    "n":          27,
    "mean_delta": 0.187,
    "sd_delta":   0.214,
    "t_stat":     4.53,
    "p_value":    0.0001,
    "cohens_d":   0.87,
    "ci_95":      [0.099, 0.275]
  }
}
```

Every list/show/report command supports `--json` with this envelope shape.

## See also

- **`BENCH_WORKFLOW.md`** at repo root — **step-by-step operator guide** with copy-paste-runnable sections. Start here if you've never run the bench before.
- `BENCH_DESIGN.md` at repo root — full architecture + design rationale
- `EVAL_PLAN.md` — methodology, categories E1–E5, target deltas, anti-patterns
- `bench/results/FINAL_RESULTS.md` — Phase-1 pilot transcript (3 runs, Δ converged from -13.3pp to 0.0pp)
- `bench/results/PILOT_ANALYSIS.md` — root-cause analysis showing 10 grader bugs caught + fixed
