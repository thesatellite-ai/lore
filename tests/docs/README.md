# Docs examples (everything outside docs/team)

**In short:** the command output shown on the pages under `docs/` comes from real runs, never from typing. These scripts run each example against the real lore binary in a throwaway folder and paste what it printed into the pages. Run them after you change a command's output, so the pages stay true.

The command output on the pages under `docs/` (except `docs/team`, which has its own runner in `tests/team/docs`) is never typed by hand. Each example runs against the real lore binary in a throwaway world, and the pages paste the output from the transcripts.

- `run-examples.sh` builds lore with `-tags loretest` (so the test clock `LORE_NOW` works), then runs each section file in a fresh world: a fake GitHub of bare repos and one HOME per person, all under a temp dir. Nothing touches your real HOME or the network.
- `lib.sh` holds the helpers a section uses (`person`, `solo_repo`, `lore_repo`, `on`, `on!`, `quiet`, `on_known`, `note`, `id`, `config`).
- `sections/<name>.sh` is one section; its transcript is `transcripts/<name>.txt`.
- `normalize.py` replaces random ids, commit ids, fingerprints, the wall-clock times lore still prints (render history, memory invalidate, audit log, set-aside database names) and the timings `--trace` prints (as `<time>`) with stable stand-ins, so a rerun gives the same transcript. One commit printed at 7, 8 or 40 characters keeps one stand-in; digits-only numbers such as byte counts are left alone.
- `paste.py` rewrites the marked blocks in the pages from the transcripts; `--check` exits 1 when a page is stale.

```sh
tests/docs/run-examples.sh                      # every section
tests/docs/run-examples.sh start search         # some sections
LORE_BIN=/path/to/lore tests/docs/run-examples.sh start   # reuse a built binary
KEEP=1 tests/docs/run-examples.sh start         # keep the worlds to look around
LORE_BIN=/path/to/lore python3 tests/docs/paste.py          # refresh the pages
LORE_BIN=/path/to/lore python3 tests/docs/paste.py --check  # fail if a page is stale
```

A page marks a block with `<!-- example: <section>#<heading> -->` and `<!-- /example -->`, where the heading is a section's `note` text with dashes for spaces. Options: `only=1,3`, `lines=6`, `keep=REGEX`. `<!-- help: <command> -->` pastes a command's `--help`, and `<!-- errors -->` the error code table.

## Sections

| Section | What it shows | Pages |
|---|---|---|
| `start` | one person, one repo: version, init, setup, a first rule, render, commit, doctor, tables, completion | [docs/knowledge/first-project.md](../../docs/knowledge/first-project.md) |
| `knowledge` | every kind of row: add, list, show, edit, supersedes, invalidate, archive, delete, the secret check; comments and tags; learn-from, learn and external-source | [rules-memories-decisions](../../docs/knowledge/rules-memories-decisions.md), [other-row-kinds](../../docs/knowledge/other-row-kinds.md), [comments-and-tags](../../docs/knowledge/comments-and-tags.md), [import-existing-notes](../../docs/knowledge/import-existing-notes.md) |
| `search` | search across kinds, `--tables`, `--json`, archived rows, word forms, status and rebuild, repo scope flags, querylog | [docs/knowledge/search.md](../../docs/knowledge/search.md) |
| `render` | render, render again, `--dry-run`, `--target`, `--out`, `--no-pointer`, directive, why-context, renderhistory | [docs/knowledge/render-and-agent-files.md](../../docs/knowledge/render-and-agent-files.md) |
| `tasks` | one person's tasks (add, list, start, done, edit, search, cancel, delete, after, tasklist, task view), commitment and snooze, reminders and commit links | [tasks](../../docs/tasks/tasks.md), [commitment-and-snooze](../../docs/tasks/commitment-and-snooze.md), [reminders](../../docs/tasks/reminders.md), [link-commits](../../docs/tasks/link-commits.md) |
| `missions` | missions (add, list, show, status, edit, pause, resume, done, delete), plans, plan files with `mission apply`, handoffs and the inbox | [missions-and-plans](../../docs/tasks/missions-and-plans.md) |
| `runs` | run start, step, end, show, replay, cancel and list; runs stay local; `session list` | [run-log](../../docs/tasks/run-log.md) |
| `sync` | notes in `.lore/data` across clones and branches: init, status, peek, promote, merge driver, conflicts and resolve, dupes and merge-rows, trash and restore, export `--all`, purge, hooks, pre-commit refusal, install-action (`--dry-run`, default by-hand only, `--auto`), ci-merge, fix-projects | [docs/sync](../../docs/sync/README.md) |
| `projects` | project and repo commands, notes per repo, mount-alias, one folder store for several repos (`lore store init`, `lore store add`, `--to`), converting a v0.1.17 shared-database (Mode B) repo with `lore migrate run`, project archive and delete, the outside-a-repo error | [multi-repo-projects](../../docs/sync/multi-repo-projects.md), [shared-database-mode-b](../../docs/sync/shared-database-mode-b.md) |
| `ops` | doctor, audit log and verify, backup, restore, repair, rebuild from `.lore/data`, support bundle, secret refusal, pii-pattern, plugin, database config, errors list, read-only, stale warning, a fresh CI clone | [docs/operations](../../docs/operations/README.md) |
| `bench` | bench eval add, show, duplicate, edit, export, import, archive and delete; grader test; empty run and report lists; no model | [bench](../../docs/operations/bench.md) |
| `reference` | errors list, an error and its hint, exit status 19 (`E_CYCLE`), read-only refusal, config check good, bad and typo, invalid config blocks writes, `--db` / `LORE_DB`, database config keys, `default_branch`, `E_SYNC_DATA_IGNORED`, `E_NO_WORK`, the `lore-store.yaml` marker, `lore org use` and `repo_orgs`, `E_ORG_DISAGREES` and `--force-org` | [errors](../../docs/reference/errors.md), [config](../../docs/reference/config.md), [environment](../../docs/reference/environment.md), [flags](../../docs/reference/flags.md) |
| `environment` | the environment variables, each in front of one command: `LORE_LIVE=0`, `LORE_LIVE_AUTOPUSH=0`, `LORE_LIVE_REMOTE`, `LORE_TRACE=1`, `LORE_SYNC_QUIET=1`, `LORE_SYNC_GIT=0`, `LORE_SYNC_ALLOW_SECRETS=1`, `LORE_ALLOW_NETWORK_FS=1`, `LORE_ALLOW_DB_PATH_OUTSIDE=1` and `LORE_HOME` (read by `lore migrate run` on an old pointer), `NO_COLOR` / `FORCE_COLOR` | [environment](../../docs/reference/environment.md), [how-notes-travel-with-git](../../docs/sync/how-notes-travel-with-git.md), [secrets-and-pii](../../docs/operations/secrets-and-pii.md) |

## Known failures

`on_known BUG-…` marks an example that fails today because of a reported lore bug. The run prints it and does not fail; once the bug is fixed the run says `KNOWN FAILURE NOW PASSES` and the mark should be removed.

There are no known failures today.
