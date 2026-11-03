# Changelog

All notable changes to lore are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

Lore for teams: everything in [LORE_TEAM_SPEC.md](LORE_TEAM_SPEC.md) phases P1–P4.

### Added

- **Team config** `~/.lore/config.yaml` (orgs, stores, patterns, repo choices, live/lease/agent settings) with `lore config check`, which names the line of every problem. The old TOML identity file is migrated to `~/.lore/identity.yaml` once; a malformed file names its line and is skipped, never silently replaced.
- **Orgs and identity:** each repo resolves to at most one org, by `LORE_ORG`, `lore org use`, a committed `.lore/org.yaml`, a store link or a remote pattern, and `lore whoami` says which rule decided it. Each org has its own identity; a repo that matches no org never borrows an org's identity. Remotes are read raw from git config (`url.insteadOf` rewrites are ignored).
- **Live rows:** any row can be shared with everyone at once on one hidden ref per repo (`refs/lore/live`, `--live` / `--no-live`; tasks, missions, handoffs and comments are live by default). Finished rows land on `main` by themselves, with `lore land`, or as a ride-along commit on your next push when `main` is protected. `lore live show|diff|log|restore|compact|purge` and `lore explain <id>` show and repair it; `--trace` prints every git command.
- **Merges:** lore's merge driver settles the file first (add/add, an edit beats a delete, tombstones), orders live scalars by a hybrid logical clock, and merges set-like lists (labels, approvers, team members, project links) without conflicts.
- **Stores:** separate lore-only repos for internal and personal notes (`lore store init|show|open|forget|doctor`, checked for a `lore-store.yaml` marker and refused if they hold code). `lore org link` ties a client repo to its internal project for the whole team without writing into the client repo; `lore org import` adopts a team's suggested config and registers you. `--to repo|<org>|<org>/<folder>|me` picks who a row is for; an unknown store folder is refused unless `--create` is given.
- **Leak guards:** a `commit-msg` hook, live writes, landings and `lore check pr-body` refuse internal ids, internal sentences (8 words or more) and secrets in a client repo, and fail closed when the store cannot be read. `lore check leaks --ci` checks structure only and names nothing internal. Output is redacted without a TTY or under a CI marker. `lore share` and `lore move` are the deliberate way across (new id, shows the text, asks first).
- **Cache and render outside the checkout:** external rows are cached in `~/.lore/cache`, internal context is rendered to `~/.lore/render` and imported through `CLAUDE.local.md`; `lore cache prune`, `lore context --internal`.
- **Roster:** `lore actor register|deactivate`, `lore team add|list`, `@handle` assignment to a person or a team, and `lore agent start` for one actor per agent session with its owner.
- **Agent coordination:** claims with a lease, a token and fencing on a second hidden ref (`refs/lore/signals`, always one commit): `lore task claim|next|heartbeat|release`, human overrides `revoke|reassign|cancel`, reclaim after expiry with an attempts limit, a ready queue that respects dependencies (`--after <id>[:merged]`), gates (`lore gate request|approve`, humans only), `lore inbox`, `lore handoff add --for`, `lore finding add`, `lore mission status`, `lore plan apply` (idempotent by key), `lore resource claim|release`, and a `Lore-Task:` commit trailer so a squash merge still counts as finishing the task. In client repos claims and work notes stay in the store; the client row carries only what the client should see.
- **Merged watching:** a done task is marked merged once its commit, its trailer or (optionally) its GitHub pull request is seen on `main`; `lore task merged` marks it by hand.
- Errors print their hint on the line under `ERROR:` and exit with the team error's own status (10–19).
- `lore sync install-action --auto` adds the pull request and push triggers to the workflow (they were the old default). `install-action --json` reports `auto` next to `manual`.

### Changed

- A plan file's `after:` takes the same `:done` / `:merged` suffixes as `--after`, so a plan can make a task in the same repo wait for a merge.
- `lore handoff add` takes `--for @handle` and `--task <id>`.
- `lore task add --tasklist` is optional; the project's default task list is used (created on demand).
- In a repo linked to a store, commands that run the lazy merged check print one warning line on stderr when the store cannot be reached, instead of saying nothing; the command still succeeds.
- `lore sync install-action` writes a workflow that runs only when started by hand (`workflow_dispatch`), so it never spends CI minutes on its own; the file's header says how to turn on automatic runs. `--manual` is still accepted (it is now the default) and `--auto` with `--manual` is refused.
- Re-running `lore sync install-action` without `--auto` or `--manual` keeps the installed workflow's triggers and branches, and the automatic lore-version update never changes triggers, so existing automatic workflows keep running.
- `lore rule list` hides archived rules; `--archived` shows them, marked `[archived]` (as `lore memory list` does).
- `lore identity set` needs an email: `alice@example.com`, `"Alice <alice@example.com>"`, or a name with `--key human:<email>`. A bare name is refused instead of being saved as a key that is not an email.
- Store branch names follow every rule of `git check-ref-format --branch` (a trailing `.`, control characters and `HEAD` are refused; so is `@`).

### Fixed

- A merged task's `merged_at` is recorded once on the live ref and lands like any live row, instead of every clone writing its own copy into the working tree (which left checkouts dirty and blocked `git pull`). `lore task merged` takes the same path, and marking an already merged task writes nothing.
- `lore task done` without `--commit` no longer records a HEAD that is already on the default branch, so the task is not marked merged at once by mistake; the done line names the recorded commit.
- Reclaiming an expired task keeps the work branch the earlier holder recorded and pushed, instead of recording the reclaiming agent's own branch.
- `lore task list` filters store tasks like repo tasks (done and cancelled hidden by default; `--status`, `--mission`, `--assignee` and the other filters apply) and prints both in one line format, with the `[org/folder]` label on store rows.
- `lore sync` refreshes the store link index, so a teammate's new `lore org link` shows up at once.
- `lore org link --yes` says "created; pushed" when its push had to retry after a teammate's store write, instead of "already so, nothing changed".
- With a staging org chosen, `lore whoami` counts only the links on that store branch.
- `lore plan apply --help` describes the plan file format (it was cut off after one line).
- `lore share <id> --to repo` no longer refuses its own copy: the copy may carry the shared note's text (and only that note's); it is still refused when it quotes another internal note.
- The `pre-push` hook now runs the leak check on every file version and commit message the push sends to a client repo, so a `git commit --no-verify` no longer gets internal content onto the client's remote; it fails closed when the store cannot be read. `git push --no-verify` still skips every local check.
- Rows written to a client repo (`--to repo` for any kind) are leak-checked before they are written; a refusal prints no success line and writes nothing.
- The `E_LEAK_BLOCKED` hint names the repo row that leaks and how to move or delete it, instead of the internal note it matched.
- `LORE_TRACE=1` / `--trace` no longer prints the build machine's source paths for every database query; trace covers only the git commands for live rows.
- A `..` in `db_path` is refused with `E_BAD_PATH` and a hint naming `LORE_ALLOW_DB_PATH_OUTSIDE=1`, instead of `E_INTERNAL`.
- Landing messages name the real default branch (`default_branch:` or the remote's HEAD) instead of always saying "main".
- A fresh clone in a cloud-synced or network folder is refused with `E_NETWORK_FS` before anything is written; before, `.lore/lore.db` was built first.
- The docs-vs-CLI check treats only `$ ` lines in console examples as commands, so output that contains "; lore …" is no longer flagged.
- Finishing a task whose claim expired (`lore task done` by a person) now fails with the error, and writes nothing, if the done mark cannot be applied, instead of reporting success.
- `lore doctor` reports `--supersedes` links written by older versions (stored on the new row instead of the old one), and `lore doctor --fix` turns them round; running it again changes nothing. Links it cannot decide are listed and left as they are.
- A push that loses a race (`reference already exists`, a failed atomic transaction) is retried instead of reported as a refusal.
- Team writes fail closed: when the store cannot be reached or read, `lore task done`, `lore task cancel` and `lore finding add` stop with an error instead of writing the row into the client repo.
- `lore task list --mine` and `--claimed-by` stop with an error when the claims board cannot be read, instead of showing every task.
- A merged task that could not be recorded says so; "✓ merged" is printed only when the row was written. `lore store forget`, `store show` and `store doctor` report real failures instead of "nothing to forget", "not cloned" or "branch does not exist".
- A purge list that cannot be read stops the live apply, `lore live restore`, the landing pass and the merge driver with a clear error, so purged rows can no longer come back; a malformed purge list on one side of a merge no longer loses the other side's ids.
- Live rows are no longer dropped from lore.db when the live bookkeeping cannot be read after a refused write, and a live row file that cannot be read is no longer treated as having left live.
- `@team` handles resolve in a linked client repo, so assigning a task there to a team is refused with TEAM-030 as the spec says.
- `lore playbook add` and `lore prompt add` work again (new optional `--description`, defaulting to the title); `lore snapshot add` works again (new `--source`, default `manual`, checked against the known source kinds).
- `LORE_PROJECT_ID` and `LORE_REPO` are read when `--db` or `LORE_DB` is used outside a project folder.
- `--supersedes <id>` marks the old row: its `superseded_by_id` names the new row and a replaced decision becomes `superseded`. Unknown or already-replaced ids are refused and nothing is written.
- `lore doctor` no longer warns about a secret in a row that was deleted or edited clean: the recorded sync refusal is cleared when the row goes.

### Documentation

- A new documentation home, [docs/README.md](docs/README.md), with a learning path in thirteen steps (from your first rule to a team with a client, private notes and several agents), a "Start from your goal" table, and a list of every page.
- Step-by-step guides for one person's work: writing notes down, search and render ([docs/knowledge](docs/knowledge/README.md)), tasks, missions, plans, reminders and run logs ([docs/tasks](docs/tasks/README.md)), notes in git, branches, conflicts, hooks and the GitHub workflow ([docs/sync](docs/sync/README.md)), and health checks, backups, the audit log, secrets, CI and `lore bench` ([docs/operations](docs/operations/README.md)).
- Decision guides in [docs/choosing](docs/choosing/README.md): notes in each repo or in one database, with the code or live, how to lay out stores, which org and identity setup, which file your coding tool reads, and when to use agents, claims and gates.
- Reference tables in [docs/reference](docs/reference/README.md) for every command, flag, config key, environment variable and error code, and a [feature index](docs/reference/features.md) that names the guide and a runnable example for each feature.
- Every page now starts with a short "In short" summary, explains each technical word in everyday words the first time it appears, and uses everyday examples ("Run the tests before you push", "We use Stripe for payments"). The team guide uses role names throughout (the agency, the client, Alice, Bob and Carol), and has a new page on [identity and store housekeeping](docs/team/identity-and-stores.md).
- The output on these pages is pasted from real runs of lore in throwaway folders, so it matches the program; the scripts are in [tests/docs](tests/docs/README.md) and tests/team/docs.
- An architecture picture, [docs/assets/architecture.svg](docs/assets/architecture.svg), on the README and the documentation home.
