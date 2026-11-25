# Store growth bench

How lore behaves on a team store with 1k, 100k or 1M commits, measured the same way every time, so every store growth change (packing, blobless clones, fewer commits, history index, compaction) has a before and an after number.

## Run it

```sh
task bench:store                                         # 1,000 commits, each timing 3 times
STOREGROWTH_COMMITS=1000,100000 task bench:store         # two sizes, one report each
STOREGROWTH_COMMITS=1000000 task bench:store            # 1M: one run per timing
```

Manual only: it never runs in `task check` or on push. A big size is a heavy run (1M commits: minutes of generation, gigabytes of temp space): on a shared machine start it under the machine's test lock and memory limit.

| Variable | Default | Meaning |
|---|---|---|
| `STOREGROWTH_COMMITS` | `1000` | sizes, comma separated (at least 100) |
| `STOREGROWTH_REPEAT` | `3` | runs per timing; sizes of 1M and up run once |
| `STOREGROWTH_OUT` | `tmp/bench` | report folder |
| `LORE_BENCH_BIN` | set by the task | the lore binary to measure |

Each size writes `storegrowth-<commits>-<sha>.md` (one table, same rows every run, so two runs diff by eye) and `.json` (every run's wall time) to the report folder. Everything else lives in the test's temp folder and is deleted at exit: a temp HOME (never `~/.lore`), the bare remotes, the clone.

## What it builds

1. **Seed, with a real lore.** A code repo, `lore store init`, `lore org create` (Mira is the admin), `lore member add` for Tomas and Erin, an agent registered with `lore actor register`, one row of every table (memory, decision, rule, task, comment, hotfix, playbook) and one store folder per 6,000 expected rows. The row files lore wrote are the templates of every generated row, so the row format is always the current one.
2. **History, with `git fast-import`** (`Generate` / `Import`, no lore process per commit, deterministic for a seed):
   - 98.5% of commits change one row file: a new row (70%), an edit of a recent or random row (27%) or a delete (3%);
   - 0.5% are bulk commits of 20-200 new rows (an import), 1% are landings (a side commit merged with a merge commit);
   - rows are written by the commit's author, a fifth of the admin's by the agent; must rules and high hotfixes only by the admin (lore holds them back from anyone else);
   - every 5,000 commits the admin adds a client pattern to `lore-org.yaml`;
   - at the middle commit Tomas, a member, changes `lore-org.yaml` and makes himself admin in his own stint: both must stay held back, and the harness fails when `lore doctor` stops naming either;
   - at the end `refs/lore/signals/main`, 15 layer refs and 30 layer-trash refs.
   
   This is about 1.3 rows per commit and one file changed per commit, the shape of the busiest measured stores.
3. **Timings** (median of the runs): first clone (the clone is removed and `lore sync` makes it again), fetch of 100 new remote commits, write and push, `lore ask` and `lore search` cold (the store just changed) and warm, the authority replay cold (no saved replay) and warm, the org file's path-limited `git log`, a 50-row `lore batch apply` and its `lore batch undo`, `lore task history --git` of an edited task, `lore trash list --from-history`, and the clone's `.git` size, packs and loose objects after the clone and at the end. A command over 10 minutes is recorded as `>600 s` and the run goes on.

Not timed yet: landing and promoting a layer (the harness has no layer flow) and the git process count per command.

## Tests

`go test ./tests/bench/storegrowth/` (no tag, no lore binary, in every gate) checks the generator's shape on a 1k store: commit counts, merges, bulk commits, org file changes, the held pair and its author, the extra refs, rows per table and folder, who writes the admin-only tables, and that one plan gives the same tree twice.
