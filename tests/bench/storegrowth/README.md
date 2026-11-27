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
| `STOREGROWTH_LIMIT` | `10m` | the longest one measured command may take (a Go duration); a run at 1M commits needs more (`90m`) |
| `STOREGROWTH_KEEP` | unset | a folder: each world is made in a new folder under it and left there with an `env.sh` (its environment, and `cd` into the code repo), to profile a command again on the same store |
| `LORE_BENCH_BIN` | set by the task | the lore binary to measure |

Each size writes `storegrowth-<commits>-<sha>.md` (one table, same rows every run, so two runs diff by eye) and `.json` (every run's wall time) to the report folder. Everything else lives in the test's temp folder and is deleted at exit: a temp HOME (never `~/.lore`), the bare remotes, the clone.

**lore doctor** has its own run (`TestBenchDoctor`, devco:D-1012): per size it seeds and generates the store, clones it with `lore sync`, runs `lore doctor` once to fill the read caches, then times a second run with its peak RSS and checks the two planted changes are still held back. It writes `doctor.md` to the report folder and fails over 350 MB RSS (500 MB before devco:D-1016), or over `STOREGROWTH_DOCTOR_MAX` (a duration) when that is set:

```sh
LORE_BENCH_BIN=<lore> STOREGROWTH_COMMITS=1000,100000 go test -tags bench -run '^TestBenchDoctor$' -timeout 300m ./tests/bench/storegrowth/
```

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
3. **Timings** (median of the runs): first clone (the clone is removed and `lore sync` makes it again), fetch of 100 new remote commits, write and push, `lore ask` and `lore search` cold (the store just changed) and warm, the authority replay cold (no saved replay) and warm, the org file's path-limited `git log`, a 50-row `lore batch apply` and its `lore batch undo`, `lore task history --git` of an edited task, `lore trash list --from-history`, the clone's `.git` size, packs and loose objects after the clone and at the end, the remote commits for 100 writes synced every 20 and for a 500-row batch undo, and the `.git` growth per 1,000 writes (100 one-row writes synced every 20, once with `squash=off` and once with `squash=on`, the setting checked with `lore store show`; the remote and the clone are repacked with `git gc --prune=now` before and after; objects added are exact, the packed size moves with how git deltifies the whole history and can even shrink; the clone's numbers include the commits its reflog still holds after a squashing push). A command over the limit (10 minutes, `STOREGROWTH_LIMIT`) is recorded as `>600 s` and the run goes on.

Not timed yet: landing and promoting a layer (the harness has no layer flow) and the git process count per command.

## Compaction

`TestBenchCompaction` measures `lore store compact` (behind its flag) on the same generated store, keeping the newest 1% of the commits (1M → 10k is the spec's acceptance row): first fetch bytes and a cold `lore sync` before; the compaction itself; an existing clone (copied before the compaction) adopting it with `lore sync`; first fetch bytes and a cold `lore sync` of a fresh clone after; that the planted held changes stay held; `lore store compact --verify`. It sets `compact_archive=on`, the org's `min_lore_version` and `LORE_COMPACT_V2=1` itself, and writes `compaction-<commits>.md` to the report folder.

```sh
go build -o /tmp/lore-bench/lore ./cmd/lore
LORE_BENCH_BIN=/tmp/lore-bench/lore STOREGROWTH_COMMITS=100000 go test -tags bench -run '^TestBenchCompaction$' -timeout 180m ./tests/bench/storegrowth/
LORE_BENCH_BIN=/tmp/lore-bench/lore STOREGROWTH_COMMITS=1000000 STOREGROWTH_LIMIT=90m go test -tags bench -run '^TestBenchCompaction$' -timeout 600m ./tests/bench/storegrowth/
```

Measured at 1M commits, keeping the newest 1%: a fresh clone's first fetch 1,524 MB / 319 s before and 439 MB / 31.5 s after; its cold first `lore sync` 726 s before and 209 s after; the compaction itself 244 s; the planted held changes stayed held. This is why the compaction guide ([docs/team/identity-and-stores.md](../../../docs/team/identity-and-stores.md#folding-old-history-lore-store-compact)) names compaction as the way a store with a very long history gets a short first sync.

`STOREGROWTH_TRACE=1` prints the compaction's output with `LORE_TRACE=1`. Like every big size, run it under the machine's test lock and memory limit.

## Tests

`go test ./tests/bench/storegrowth/` (no tag, no lore binary, in every gate) checks the generator's shape on a 1k store: commit counts, merges, bulk commits, org file changes, the held pair and its author, the extra refs, rows per table and folder, who writes the admin-only tables, and that one plan gives the same tree twice.
