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

### Changed

- `lore handoff add` takes `--for @handle` and `--task <id>`.
- `lore task add --tasklist` is optional; the project's default task list is used (created on demand).

### Fixed

- A push that loses a race (`reference already exists`, a failed atomic transaction) is retried instead of reported as a refusal.
