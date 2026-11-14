# K1 — a released lore and a preview on one machine

**Who.** Ola, the owner of a project, on one laptop: lore 0.1.17 stays installed as plain `lore` for daily work, and a newer build is tried as `lore-dev`, which runs it with `LORE_HOME=$HOME/.lore-dev`. Every name is made up.

**Why.** Before `LORE_HOME` moved the whole home, the newer build kept its machine state in `~/.lore` too: one run rewrote 0.1.17's `~/.lore/config.yaml` (with `orgs:` and `personal:` blocks 0.1.17 cannot read) and its store clones, and plain `lore` broke.

**Test.** `TestJourneyLoreHomeBesideOldLore` and `TestJourneyLoreHomeMovesTheWholeHome` (tests/journeys/lorehome_test.go); the resolver itself: `TestResolveLoreHome` (adapters/lorehome).

## Steps

1. Day 1: plain `lore` (0.1.17) runs `init`, adds a task and a memory in the repo `legacy`. lore records every byte and file mode under `~/.lore`.
2. The new build, with `LORE_HOME=$HOME/.lore-dev`, runs `init`, `memory add`, `task add`, `render`, `doctor` and `where` in another repo, `fresh`. `lore where` starts with `lore home: <home>/.lore-dev (from LORE_HOME)`.
3. Day 2: plain `lore` lists its memories in `legacy` and finds its note.
4. A whole working day with `LORE_HOME` set and no 0.1.17 (the second journey): solo setup with a personal store on GitHub, an org with a team store on a remote, notes, tasks and rules, `--to me` and `--to <org>` writes, `sync`, `render`, a `git commit` through lore's hooks, `doctor --all` (it prints `Lore home: … (from LORE_HOME)`), `fix --all`.

## What must hold

- `~/.lore` is byte-identical after step 2: no file added, changed or removed, no mode changed.
- With `LORE_HOME` set and no old home, `~/.lore` is never created; `config.yaml`, `stores/`, `cache/` and `state/` are under `LORE_HOME`.
- `LORE_HOME` that is empty, relative or starts with `~` is refused with `E_INVALID_INPUT` and a next step; it is never expanded or guessed.
- `HOME` is untouched: git, ssh and gpg read their own files as before.
- `${LORE_HOME}` in an old shared-database pointer is the resolved home; `LORE_V017_HOME` fills it alone (the 0.1.17 meaning), and the migrate error names that variable.

## Findings it guards

- `owner:LORE_HOME` (REGRESSIONS.md).
