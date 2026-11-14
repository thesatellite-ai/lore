# lore tests

End-to-end test harness. Two flavors:

- `tests/scenarios/SC-*.sh` — acceptance scenarios (happy path + variants)
- `tests/chaos/CH-*.sh` — failure-mode scenarios (FMEA matrix from PLAN.md Round 23/29)

## Running

```bash
# Build first
cd ../ai_coder_mini_go && task aicoder:build

# Run all scenarios
task aicoder:scenarios

# Run all chaos
task aicoder:chaos

# Run a specific one manually
LORE=$(pwd)/../ai_coder_mini_go/bin/aicoder bash tests/scenarios/SC-1.sh
```

## Conventions

Each script:

1. Has a header comment listing **catches** it tests (e.g., `R14-1`, `R23-44`)
2. Sets `set -e` and a `trap cleanup EXIT` to remove temp dirs
3. Uses `mktemp -d` for ephemeral working dirs
4. Asserts via `grep`, `test`, `[ ... ]`, `jq -e`
5. Emits `PASS: SC-N` on success
6. Calls `fail "<reason>"` and exits non-zero on assertion failure
7. Reads `$LORE` env var for binary path (default: `aicoder` on PATH)

## Regression index

`tests/stories/REGRESSIONS.md` lists every scenario, incident and finding of the 0.1.18 effort with the Go test(s) that guard it. The plain `go test` in `tests/stories/regressguard` reads it on every `go test ./...`: a row naming a test that no longer exists fails the build. A fix lands with its regression test and its row in the same commit.

## Temp folders

A suite leaves nothing in the shared temp folder (`os.TempDir()`): leftover worlds once filled its inodes and later runs failed. Every folder a test makes goes in `t.TempDir()` or is removed when the test or `TestMain` ends. A world that may hold read-only folders (a 0500 `~/.lore`, a locked code folder) is removed with `tmpworld.RemoveAll` (package `tests/tmpworld`), since `os.RemoveAll` cannot empty a read-only folder; a suite whose worlds are named by a fixed prefix calls `tmpworld.SweepStale(prefix, age)` in `TestMain`, so the worlds of a run that was killed before its cleanup are removed by the next run (`tests/machines` does both).

## Adding a new scenario

1. Pick the next free number (e.g., SC-31)
2. Write the script following the template in SC-1.sh
3. Add a row in `.ai/SCENARIOS.md`'s coverage table
4. Add corresponding catch IDs to `.ai/COVERAGE.md`
5. Run `task aicoder:verify-coverage` to confirm linkage

## Coverage gate

`task aicoder:ship-check` requires all scenarios in both directories to pass before allowing a release. `tools/coverage-check` enforces every active catch in `.ai/COVERAGE.md` maps to a task that has at least one test reference.
