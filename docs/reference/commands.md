# Command index

**In short:** this page lists every lore command with its one-line help and a link to the guide that explains it. It is made from the real program, so it always matches the version in this repo. For every flag of one command, run `lore <command> --help`.

How to read it:

- A command marked **…** only groups other commands (like a folder); you run the commands under it, for example `lore task add`, not `lore task`.
- **Guide** is the best page that explains the command. *(brief)* means that page only mentions it.
- Most commands also take the shared flags `--db`, `--project`, `--repo` and `--read-only`; see [Flags](flags.md).

The list below is written by `tests/docs/commands_index.py` from `lore --help` and the guide column of the [feature index](features.md). Do not edit it by hand: change the command's help text or the feature index, then run the script.

Three hidden commands are left out on purpose: `lore merge-driver`, `lore sync hook` and `lore task hook commit-msg`. Git runs them from the hooks and the merge setting that lore installs; you never run them by hand.

<!-- commands -->
468 commands, 388 of them leaf commands (the ones you run; a command marked … only groups others).

Jump to: [actor](#lore-actor) · [agent](#lore-agent) · [api](#lore-api) · [architecturenote](#lore-architecturenote) · [audit](#lore-audit) · [backup](#lore-backup) · [behaviour](#lore-behaviour) · [bench](#lore-bench) · [cache](#lore-cache) · [check](#lore-check) · [comment](#lore-comment) · [commit](#lore-commit) · [completion](#lore-completion) · [config](#lore-config) · [context](#lore-context) · [cookbookrecipe](#lore-cookbookrecipe) · [decision](#lore-decision) · [directive](#lore-directive) · [doctor](#lore-doctor) · [errors](#lore-errors) · [explain](#lore-explain) · [external-source](#lore-external-source) · [finding](#lore-finding) · [gate](#lore-gate) · [handoff](#lore-handoff) · [hold](#lore-hold) · [hotfix](#lore-hotfix) · [identity](#lore-identity) · [inbox](#lore-inbox) · [incident](#lore-incident) · [init](#lore-init) · [land](#lore-land) · [learn](#lore-learn) · [learn-from](#lore-learn-from) · [link](#lore-link) · [live](#lore-live) · [memory](#lore-memory) · [meta](#lore-meta) · [migrate](#lore-migrate) · [mission](#lore-mission) · [mount-alias](#lore-mount-alias) · [move](#lore-move) · [org](#lore-org) · [pattern](#lore-pattern) · [pii-pattern](#lore-pii-pattern) · [plan](#lore-plan) · [playbook](#lore-playbook) · [plugin](#lore-plugin) · [policy](#lore-policy) · [project](#lore-project) · [prompt](#lore-prompt) · [querylog](#lore-querylog) · [reminder](#lore-reminder) · [render](#lore-render) · [renderhistory](#lore-renderhistory) · [repair](#lore-repair) · [repo](#lore-repo) · [resource](#lore-resource) · [restore](#lore-restore) · [rule](#lore-rule) · [run](#lore-run) · [search](#lore-search) · [secrets](#lore-secrets) · [session](#lore-session) · [setup](#lore-setup) · [share](#lore-share) · [skill](#lore-skill) · [snapshot](#lore-snapshot) · [spec](#lore-spec) · [sql](#lore-sql) · [store](#lore-store) · [suggestion](#lore-suggestion) · [support-bundle](#lore-support-bundle) · [sync](#lore-sync) · [tables](#lore-tables) · [tag](#lore-tag) · [task](#lore-task) · [task-view](#lore-task-view) · [tasklist](#lore-tasklist) · [tastepref](#lore-tastepref) · [team](#lore-team) · [techdoc](#lore-techdoc) · [tui](#lore-tui) · [vars](#lore-vars) · [version](#lore-version) · [whoami](#lore-whoami) · [why-context](#lore-why-context) · [workflow](#lore-workflow) · [workspace](#lore-workspace)

### lore actor

| Command | What it does | Guide |
|---|---|---|
| `lore actor` … | Inspect actors (humans, agents, hooks, plugins) | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |
| `lore actor deactivate` | Mark someone as gone (active=false, left_at); actors are never deleted | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |
| `lore actor delete` | Refused: actors are never deleted (use `lore actor deactivate`) | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |
| `lore actor list` | List actors | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |
| `lore actor register` | Put yourself (or, with --kind agent, an agent you own) on the org roster | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |
| `lore actor show` | Show actor details | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |

### lore agent

| Command | What it does | Guide |
|---|---|---|
| `lore agent` … | Agent sessions (one actor per session label) | — |
| `lore agent start` | Register an agent actor for this session and print its environment | [team/commands › lore agent start](../../docs/team/commands.md#lore-agent-start) |

### lore api

| Command | What it does | Guide |
|---|---|---|
| `lore api` … | Serve the local REST API (127.0.0.1, bearer token in ~/.lore/api-token) | — |
| `lore api token` … | Manage the local API token | — |
| `lore api token rotate` | Replace the API token (every client holding the old one gets 401) | — |

### lore architecturenote

| Command | What it does | Guide |
|---|---|---|
| `lore architecturenote` … | lore architecturenote commands | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore architecturenote add` | Add a new architecture note | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore architecturenote delete` | Delete a architecture note (a tombstone keeps old copies from returning) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore architecturenote edit` | Edit architecture-note fields | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore architecturenote list` | List architecturenotes | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore architecturenote search` | Full-text search across architecture note title + body | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore architecturenote show` | Show architecture note details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

### lore audit

| Command | What it does | Guide |
|---|---|---|
| `lore audit` … | Inspect and verify the hash-chained audit log of knowledge changes | — |
| `lore audit log` | Show recent audit entries, newest first | [operations/audit-log › Step 2 — Catch a change made outside lore](../../docs/operations/audit-log.md#step-2--catch-a-change-made-outside-lore) |
| `lore audit verify` | Check the audit chain and look for rows changed outside lore | [operations/audit-log › Step 2 — Catch a change made outside lore](../../docs/operations/audit-log.md#step-2--catch-a-change-made-outside-lore) |

### lore backup

| Command | What it does | Guide |
|---|---|---|
| `lore backup` | Create an online backup of the project DB | [operations/backup-restore-repair › Step 1 — Make a backup](../../docs/operations/backup-restore-repair.md#step-1--make-a-backup) |

### lore behaviour

| Command | What it does | Guide |
|---|---|---|
| `lore behaviour` … | lore behaviour commands | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore behaviour add` | Add a new behaviour | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore behaviour delete` | Delete a behaviour (a tombstone keeps old copies from returning) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore behaviour edit` | Edit behaviour fields | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore behaviour list` | List behaviours | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore behaviour search` | Full-text search across behaviour name + body | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore behaviour show` | Show behaviour details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

### lore bench

| Command | What it does | Guide |
|---|---|---|
| `lore bench` … | Benchmark engine — define + run + report on eval tasks | — |
| `lore bench eval` … | Manage benchmark task definitions (templates) | [operations/bench › Step 4 — Copy, retire and delete questions](../../docs/operations/bench.md#step-4--copy-retire-and-delete-questions) |
| `lore bench eval add` | Add a new benchmark task | [operations/bench › Step 4 — Copy, retire and delete questions](../../docs/operations/bench.md#step-4--copy-retire-and-delete-questions) |
| `lore bench eval archive` | Soft-delete (sets archived_at; preserves history) | [operations/bench › Step 4 — Copy, retire and delete questions](../../docs/operations/bench.md#step-4--copy-retire-and-delete-questions) |
| `lore bench eval delete` | Durably delete (refuses if any results reference it) | [operations/bench › Step 4 — Copy, retire and delete questions](../../docs/operations/bench.md#step-4--copy-retire-and-delete-questions) |
| `lore bench eval duplicate` | Clone an existing task with --as=<new-code> | [operations/bench › Step 4 — Copy, retire and delete questions](../../docs/operations/bench.md#step-4--copy-retire-and-delete-questions) |
| `lore bench eval edit` | Update fields on an existing bench task | [operations/bench › Step 4 — Copy, retire and delete questions](../../docs/operations/bench.md#step-4--copy-retire-and-delete-questions) |
| `lore bench eval export` | Dump bench tasks → directory of YAML files (round-trip with import) | [operations/bench › Step 4 — Copy, retire and delete questions](../../docs/operations/bench.md#step-4--copy-retire-and-delete-questions) |
| `lore bench eval import` | Bulk-import bench tasks from a directory of YAML files | [operations/bench › Step 4 — Copy, retire and delete questions](../../docs/operations/bench.md#step-4--copy-retire-and-delete-questions) |
| `lore bench eval list` | List benchmark tasks | [operations/bench › Step 4 — Copy, retire and delete questions](../../docs/operations/bench.md#step-4--copy-retire-and-delete-questions) |
| `lore bench eval show` | Show one bench task with full grader spec + linked body | [operations/bench › Step 4 — Copy, retire and delete questions](../../docs/operations/bench.md#step-4--copy-retire-and-delete-questions) |
| `lore bench eval unarchive` | Restore an archived task | [operations/bench › Step 4 — Copy, retire and delete questions](../../docs/operations/bench.md#step-4--copy-retire-and-delete-questions) |
| `lore bench grader` … | Meta-tools for graders (test / debug / audit) | [operations/bench › Step 2 — Check the marker before any model sees it](../../docs/operations/bench.md#step-2--check-the-marker-before-any-model-sees-it) |
| `lore bench grader audit` | Flag suspect graders (too-strict / too-loose / flaky) | [operations/bench › Step 2 — Check the marker before any model sees it](../../docs/operations/bench.md#step-2--check-the-marker-before-any-model-sees-it) |
| `lore bench grader debug` | Show stored grader_trace for one result (forensic debug) | [operations/bench › Step 2 — Check the marker before any model sees it](../../docs/operations/bench.md#step-2--check-the-marker-before-any-model-sees-it) |
| `lore bench grader test` | Run a grader against a sample output file (no model call) | [operations/bench › Step 2 — Check the marker before any model sees it](../../docs/operations/bench.md#step-2--check-the-marker-before-any-model-sees-it) |
| `lore bench report` … | Analysis on past bench runs | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench report analyze` | Statistical analysis: paired t-test, Cohen's d, 95% CI | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench report by-category` | Pass-rates per category × arm for one run | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench report compare` | Side-by-side: which tasks improved, which regressed | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench report regressions` | Tasks whose with-skill pass-rate dropped > 5pp recently | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench report summary` | Headline numbers for one run (omit arg or pass --latest for the most recent) | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench report trend` | Longitudinal: pass-rates / Δ per run, ordered by time | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench result` … | Inspect + regrade individual benchmark results | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench result compare` | Side-by-side: prompts, outputs, grades | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench result list` | List bench results (filter by --run, --eval, --arm, --grade) | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench result regrade` | Re-grade stored outputs without calling the model again | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench result replay` | Call the model again for a stored run (regrade reuses the stored output) | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench result show` | Show one bench result: prompt sent, output received, grader trace | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench result stats` | Tally pass/fail/error grouped by arm for one run (live-safe) | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench run` … | Execute benchmark runs against a model | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench run cancel` | Mark a running benchmark as aborted | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench run list` | List past bench runs | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench run retry` | Re-run failed (eval, arm) combos from a past run as a new run | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench run show` | Show one bench run with per-task verdict | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |
| `lore bench run start` | Start a benchmark run (synchronous) | [operations/bench › Step 6 — Read the results](../../docs/operations/bench.md#step-6--read-the-results) |

### lore cache

| Command | What it does | Guide |
|---|---|---|
| `lore cache` … | Maintain lore's caches outside the checkout (~/.lore/cache, ~/.lore/render) | — |
| `lore cache prune` | Remove cache files of stores no repo on this machine attaches any more (lists them and asks first) | [team/identity-and-stores › Step 8 — Clean up old caches: lore cache prune](../../docs/team/identity-and-stores.md#step-8--clean-up-old-caches-lore-cache-prune) |

### lore check

| Command | What it does | Guide |
|---|---|---|
| `lore check` … | Leak checks: internal or personal content must never reach the client | — |
| `lore check leaks` | Check staged files and the commit message (hooks), or the tree structure (--ci) | [team/commands › lore check leaks](../../docs/team/commands.md#lore-check-leaks) |
| `lore check pr-body` | Check a pull request body or comment before posting it (agents: always) | [team/commands › lore check pr-body](../../docs/team/commands.md#lore-check-pr-body) |

### lore comment

| Command | What it does | Guide |
|---|---|---|
| `lore comment` … | lore comment commands | [knowledge/comments-and-tags › Try it: read the comments on a row](../../docs/knowledge/comments-and-tags.md#try-it-read-the-comments-on-a-row) *(brief)* |
| `lore comment add` | Add a comment on a target entity | [knowledge/comments-and-tags › Try it: read the comments on a row](../../docs/knowledge/comments-and-tags.md#try-it-read-the-comments-on-a-row) *(brief)* |
| `lore comment delete` | Delete a comment (a tombstone is kept so old copies never return) | [knowledge/comments-and-tags › Try it: read the comments on a row](../../docs/knowledge/comments-and-tags.md#try-it-read-the-comments-on-a-row) *(brief)* |
| `lore comment list` | List comments (optionally filtered by entity) | [knowledge/comments-and-tags › Try it: read the comments on a row](../../docs/knowledge/comments-and-tags.md#try-it-read-the-comments-on-a-row) *(brief)* |
| `lore comment search` | FTS5 search across comment body | [knowledge/comments-and-tags › Try it: read the comments on a row](../../docs/knowledge/comments-and-tags.md#try-it-read-the-comments-on-a-row) *(brief)* |

### lore commit

| Command | What it does | Guide |
|---|---|---|
| `lore commit` … | lore commit commands | — |
| `lore commit show` | Show every entity linked to a given git commit | — |

### lore completion

| Command | What it does | Guide |
|---|---|---|
| `lore completion` | Generate the autocompletion script for the specified shell | [knowledge/first-project › Try it: check that everything is healthy](../../docs/knowledge/first-project.md#try-it-check-that-everything-is-healthy) |

### lore config

| Command | What it does | Guide |
|---|---|---|
| `lore config` … | Get/set keys of ~/.lore/config.yaml | [operations/plugins-and-db-config › Step 2 — Read and write database settings](../../docs/operations/plugins-and-db-config.md#step-2--read-and-write-database-settings) |
| `lore config check` | Validate ~/.lore/config.yaml (orgs, stores, live settings) and list every problem | [reference/config › What lore config check refuses](../../docs/reference/config.md#what-lore-config-check-refuses) |
| `lore config get` | Get a key of ~/.lore/config.yaml (a dotted path, e.g. live.autopush) | [operations/plugins-and-db-config › Step 2 — Read and write database settings](../../docs/operations/plugins-and-db-config.md#step-2--read-and-write-database-settings) |
| `lore config list` | List every key set in ~/.lore/config.yaml | [operations/plugins-and-db-config › Step 2 — Read and write database settings](../../docs/operations/plugins-and-db-config.md#step-2--read-and-write-database-settings) |
| `lore config set` | Set a key of ~/.lore/config.yaml (keeps comments and order) | [operations/plugins-and-db-config › Step 2 — Read and write database settings](../../docs/operations/plugins-and-db-config.md#step-2--read-and-write-database-settings) |

### lore context

| Command | What it does | Guide |
|---|---|---|
| `lore context` | Print internal, global and personal context for tools that cannot follow an import | [operations/ci › Step 5 — Give tools the notes they cannot import](../../docs/operations/ci.md#step-5--give-tools-the-notes-they-cannot-import) |

### lore cookbookrecipe

| Command | What it does | Guide |
|---|---|---|
| `lore cookbookrecipe` … | lore cookbookrecipe commands | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore cookbookrecipe add` | Add a new cookbookrecipe | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore cookbookrecipe delete` | Delete a cookbookrecipe (a tombstone keeps old copies from returning) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore cookbookrecipe edit` | Edit cookbook-recipe fields | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore cookbookrecipe list` | List cookbookrecipes | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore cookbookrecipe search` | Full-text search across cookbookrecipe title + body + language | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore cookbookrecipe show` | Show cookbookrecipe details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

### lore decision

| Command | What it does | Guide |
|---|---|---|
| `lore decision` … | lore decision commands | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision add` | Add a new decision (context + chosen + alternatives + reasoning) | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision archive` | Archive a decision (hidden from render and search; kept) | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision delete` | Delete a decision (a tombstone is kept so old copies never return) | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision edit` | Edit decision fields (only the flags given change) | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision list` | List decisions | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision search` | Full-text search over decisions of every attached store | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision show` | Show decision details | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision unarchive` | Bring an archived decision back | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |

### lore directive

| Command | What it does | Guide |
|---|---|---|
| `lore directive` … | Install/remove the lore agent-directive block in agent-loaded markdown | [knowledge/render-and-agent-files › Step 5 — Add only the directive](../../docs/knowledge/render-and-agent-files.md#step-5--add-only-the-directive) |
| `lore directive install` | Install (or refresh) the directive block at the top of one or more files | [knowledge/render-and-agent-files › Step 5 — Add only the directive](../../docs/knowledge/render-and-agent-files.md#step-5--add-only-the-directive) |
| `lore directive remove` | Strip the directive block from one or more files | [knowledge/render-and-agent-files › Step 5 — Add only the directive](../../docs/knowledge/render-and-agent-files.md#step-5--add-only-the-directive) |
| `lore directive show` | Print the directive block (for piping or review) without writing any file | [knowledge/render-and-agent-files › Step 5 — Add only the directive](../../docs/knowledge/render-and-agent-files.md#step-5--add-only-the-directive) |

### lore doctor

| Command | What it does | Guide |
|---|---|---|
| `lore doctor` | Run health checks; exit 0 healthy, 1 degraded, 2 broken | [operations/doctor › Check lore's health with lore doctor](../../docs/operations/doctor.md#check-lores-health-with-lore-doctor) |

### lore errors

| Command | What it does | Guide |
|---|---|---|
| `lore errors` … | Manage error code registry | — |
| `lore errors list` | List all registered error codes | [operations/doctor › Look up an error code](../../docs/operations/doctor.md#look-up-an-error-code) |

### lore explain

| Command | What it does | Guide |
|---|---|---|
| `lore explain` | Show every copy of a row (live, this branch, the default branch), which one wins and why, and its land decision | [team/commands › lore explain](../../docs/team/commands.md#lore-explain) |

### lore external-source

| Command | What it does | Guide |
|---|---|---|
| `lore external-source` … | lore external-source commands | [knowledge/import-existing-notes › Register an outside source](../../docs/knowledge/import-existing-notes.md#register-an-outside-source) |
| `lore external-source add` | Register a new external source (disabled by default) | [knowledge/import-existing-notes › Register an outside source](../../docs/knowledge/import-existing-notes.md#register-an-outside-source) |
| `lore external-source disable` | disable an external source | [knowledge/import-existing-notes › Register an outside source](../../docs/knowledge/import-existing-notes.md#register-an-outside-source) |
| `lore external-source enable` | enable an external source | [knowledge/import-existing-notes › Register an outside source](../../docs/knowledge/import-existing-notes.md#register-an-outside-source) |
| `lore external-source list` | List registered external sources | [knowledge/import-existing-notes › Register an outside source](../../docs/knowledge/import-existing-notes.md#register-an-outside-source) |

### lore finding

| Command | What it does | Guide |
|---|---|---|
| `lore finding` … | Findings: what one agent learns, every agent of the mission sees | — |
| `lore finding add` | Save a finding pinned to a mission (a live memory every agent sees in its inbox) | [team/commands › lore finding add](../../docs/team/commands.md#lore-finding-add) |

### lore gate

| Command | What it does | Guide |
|---|---|---|
| `lore gate` … | Human gates: stop agents until a person approves | — |
| `lore gate approve` | Approve a gate (a person on the roster, from the identity file, never an agent) | [team/commands › lore gate request](../../docs/team/commands.md#lore-gate-request) |
| `lore gate request` | Put a gate on a task (agents stop at it until a person approves) | [team/commands › lore gate request](../../docs/team/commands.md#lore-gate-request) |

### lore handoff

| Command | What it does | Guide |
|---|---|---|
| `lore handoff` … | lore handoff commands | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |
| `lore handoff ack` | Acknowledge a handoff (sets status_str=acked) | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |
| `lore handoff add` | Leave a handoff for someone (shows in their inbox) | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |
| `lore handoff delete` | Delete a handoff (a tombstone keeps old copies from returning) | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |
| `lore handoff list` | List handoffs | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |
| `lore handoff search` | Full-text search across handoff body + status | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |
| `lore handoff show` | Show handoff details | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |

### lore hold

| Command | What it does | Guide |
|---|---|---|
| `lore hold` … | Hold a shared resource (a test suite, a staging slot) with a lease | — |
| `lore hold claim` | Hold a resource exclusively (same lease, token and renew as a task claim) | [team/agents › Sharing one thing nobody may use twice at once: lore hold](../../docs/team/agents.md#sharing-one-thing-nobody-may-use-twice-at-once-lore-hold) |
| `lore hold release` | Release a resource you hold | [team/agents › Sharing one thing nobody may use twice at once: lore hold](../../docs/team/agents.md#sharing-one-thing-nobody-may-use-twice-at-once-lore-hold) |

### lore hotfix

| Command | What it does | Guide |
|---|---|---|
| `lore hotfix` … | lore hotfix commands | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix add` | Add a new hotfix (a loud recurring warning: we keep hitting this, beware) | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix archive` | Archive a hotfix (hidden from render and search; kept) | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix delete` | Delete a hotfix (a tombstone is kept so old copies never return) | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix edit` | Edit hotfix fields (only the flags given change) | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix list` | List hotfixes | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix search` | Full-text search over hotfixes of every attached store | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix show` | Show hotfix details | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix unarchive` | Bring an archived hotfix back | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |

### lore identity

| Command | What it does | Guide |
|---|---|---|
| `lore identity` … | Manage persisted identity (~/.lore/identity.yaml) | [team/identity-and-stores › Step 2 — Set or remove your own identity: lore identity set and unset](../../docs/team/identity-and-stores.md#step-2--set-or-remove-your-own-identity-lore-identity-set-and-unset) |
| `lore identity anonymize` | Toggle anonymous-mode (anon:<random> per session) | [team/identity-and-stores › Step 2 — Set or remove your own identity: lore identity set and unset](../../docs/team/identity-and-stores.md#step-2--set-or-remove-your-own-identity-lore-identity-set-and-unset) |
| `lore identity set` | Persist an identity: the fallback (~/.lore/identity.yaml) or one org's (--org) | [team/identity-and-stores › Step 2 — Set or remove your own identity: lore identity set and unset](../../docs/team/identity-and-stores.md#step-2--set-or-remove-your-own-identity-lore-identity-set-and-unset) |
| `lore identity show` | Show the resolved actor identity + which step matched | [team/identity-and-stores › Step 2 — Set or remove your own identity: lore identity set and unset](../../docs/team/identity-and-stores.md#step-2--set-or-remove-your-own-identity-lore-identity-set-and-unset) |
| `lore identity unset` | Remove ~/.lore/identity.yaml (and any legacy identity.toml) | [team/identity-and-stores › Step 2 — Set or remove your own identity: lore identity set and unset](../../docs/team/identity-and-stores.md#step-2--set-or-remove-your-own-identity-lore-identity-set-and-unset) |

### lore inbox

| Command | What it does | Guide |
|---|---|---|
| `lore inbox` | What needs you: assignments, handoffs, gates, expired claims, blocked tasks, findings | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |

### lore incident

| Command | What it does | Guide |
|---|---|---|
| `lore incident` … | lore incident commands | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore incident add` | Add a new incident | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore incident delete` | Delete a incident (a tombstone keeps old copies from returning) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore incident list` | List incidents | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore incident search` | Full-text search across incident title + body | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore incident show` | Show incident details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

### lore init

| Command | What it does | Guide |
|---|---|---|
| `lore init` | Initialize a new lore project at the given path (default cwd) | [knowledge/first-project › Step 1 — Set up lore in the repo](../../docs/knowledge/first-project.md#step-1--set-up-lore-in-the-repo) |

### lore land

| Command | What it does | Guide |
|---|---|---|
| `lore land` | Write live rows to main now (finished rows land; open rows are copied and stay live) | [team/commands › lore land](../../docs/team/commands.md#lore-land) |

### lore learn

| Command | What it does | Guide |
|---|---|---|
| `lore learn` … | Manage learn_candidates (background-learning staging) | [knowledge/import-existing-notes › Step 4 — Keep or drop each candidate](../../docs/knowledge/import-existing-notes.md#step-4--keep-or-drop-each-candidate) |
| `lore learn from` | Bootstrap knowledge from existing sources | [knowledge/import-existing-notes › Step 4 — Keep or drop each candidate](../../docs/knowledge/import-existing-notes.md#step-4--keep-or-drop-each-candidate) |
| `lore learn list` | List learn_candidates | [knowledge/import-existing-notes › Step 4 — Keep or drop each candidate](../../docs/knowledge/import-existing-notes.md#step-4--keep-or-drop-each-candidate) |
| `lore learn promote` | Promote a learn_candidate to its target table | [knowledge/import-existing-notes › Step 4 — Keep or drop each candidate](../../docs/knowledge/import-existing-notes.md#step-4--keep-or-drop-each-candidate) |
| `lore learn reject` | Mark a learn_candidate as rejected (single, --ids=a,b,c, or --all) | [knowledge/import-existing-notes › Step 4 — Keep or drop each candidate](../../docs/knowledge/import-existing-notes.md#step-4--keep-or-drop-each-candidate) |

### lore learn-from

| Command | What it does | Guide |
|---|---|---|
| `lore learn-from` | Bootstrap knowledge from existing sources | [knowledge/import-existing-notes › Bring in notes you already have](../../docs/knowledge/import-existing-notes.md#bring-in-notes-you-already-have) *(brief)* |

### lore link

| Command | What it does | Guide |
|---|---|---|
| `lore link` … | lore link commands | [tasks/link-commits › Step 6 — Remove a link](../../docs/tasks/link-commits.md#step-6--remove-a-link) |
| `lore link add` | Link a git commit to an entity (--entity=tsk_… --commit=<sha>) | [tasks/link-commits › Step 6 — Remove a link](../../docs/tasks/link-commits.md#step-6--remove-a-link) |
| `lore link list` | List commit links (filter by --entity or --commit) | [tasks/link-commits › Step 6 — Remove a link](../../docs/tasks/link-commits.md#step-6--remove-a-link) |
| `lore link remove` | Remove a commit link by its ID (a tombstone is kept) | [tasks/link-commits › Step 6 — Remove a link](../../docs/tasks/link-commits.md#step-6--remove-a-link) |

### lore live

| Command | What it does | Guide |
|---|---|---|
| `lore live` … | Inspect and maintain the live ref (refs/lore/live) | — |
| `lore live compact` | Rewrite the live ref to one commit with a higher epoch (keeps every row; a local backup is kept) | [team/commands › lore live compact](../../docs/team/commands.md#lore-live-compact) |
| `lore live diff` | Local, tracking and remote live ref, and the writes not pushed yet | [team/commands › lore live diff](../../docs/team/commands.md#lore-live-diff) |
| `lore live log` | History of the live ref with its epoch | [team/commands › lore live log](../../docs/team/commands.md#lore-live-log) |
| `lore live purge` | Remove rows from live for good (leak cleanup): no clone can push them back | [team/commands › lore live purge](../../docs/team/commands.md#lore-live-purge) |
| `lore live restore` | Put the live rows back on the remote from the local copy after it was deleted or rewritten | [team/commands › lore live restore](../../docs/team/commands.md#lore-live-restore) |
| `lore live show` | List this project's live rows, or print one live file | [team/commands › lore live show](../../docs/team/commands.md#lore-live-show) |

### lore memory

| Command | What it does | Guide |
|---|---|---|
| `lore memory` … | lore memory commands | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory add` | Add a memory (free-form learned knowledge) | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory archive` | Archive a memory (hidden from list and search; kept) | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory delete` | Delete a memory (a tombstone is kept so old copies never return) | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory edit` | Edit memory fields (only the flags given change) | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory invalidate` | Mark a memory invalid as of now (sets valid_until) | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory list` | List memories of every attached store (active by default) | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory move` | Move a memory to another attached store (same id; the old copy is tombstoned) | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory search` | Full-text search over memories of every attached store | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory show` | Show one memory | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory unarchive` | Bring an archived memory back | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |

### lore meta

| Command | What it does | Guide |
|---|---|---|
| `lore meta` … | lore meta commands | — |
| `lore meta add` | Add a metadata key to a place's registry | — |
| `lore meta delete` | Delete a metadata key and its allowed values | — |
| `lore meta describe` | Set a metadata key's description | — |
| `lore meta edit` | Edit a metadata key (strict, internal, allowed values) | — |
| `lore meta keys` | List the metadata keys every visible place defines | — |
| `lore meta rename` | Rename a metadata key (a label change; the id stays) | — |
| `lore meta value` … | lore meta value commands | — |
| `lore meta value add` | Add an allowed value to a metadata key | — |
| `lore meta value rename` | Rename an allowed value (a label change; the val_ id stays) | — |

### lore migrate

| Command | What it does | Guide |
|---|---|---|
| `lore migrate` … | lore migrate commands | — |
| `lore migrate run` | Rewrite v0.1.17 row files (_v: 1) as _v: 2 (the migrated nouns) | — |

### lore mission

| Command | What it does | Guide |
|---|---|---|
| `lore mission` … | lore mission commands | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission add` | Add a new mission | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission apply` | Create a mission and its tasks from a YAML plan; a re-run adds nothing | [tasks/missions-and-plans › Step 6 — Turn a plan file into a mission with tasks](../../docs/tasks/missions-and-plans.md#step-6--turn-a-plan-file-into-a-mission-with-tasks) |
| `lore mission delete` | Delete a mission (its tasks are detached; a tombstone is kept) | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission done` | Mark mission as done | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission edit` | Edit mission fields (only flags you pass are applied) | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission list` | List missions (with attached tasks in --json mode) | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission pause` | Mark mission as paused | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission resume` | Mark mission as active (resume from paused) | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission search` | Full-text search across mission title + body + status | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission show` | Show mission details + attached tasks | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission status` | The whole mission at a glance: ready / waiting / gated / claimed / expired / blocked / done | [tasks/missions-and-plans › Step 3 — See the whole mission at a glance](../../docs/tasks/missions-and-plans.md#step-3--see-the-whole-mission-at-a-glance) |

### lore mount-alias

| Command | What it does | Guide |
|---|---|---|
| `lore mount-alias` … | Inspect repo rename-redirect aliases | — |
| `lore mount-alias list` | List mount aliases (rename redirects) | [sync/multi-repo-projects › Try it — renamed repos](../../docs/sync/multi-repo-projects.md#try-it--renamed-repos) |

### lore move

| Command | What it does | Guide |
|---|---|---|
| `lore move` | Move a row to another project (new id; the original is deleted; asks first) | [team/identity-and-stores › Step 6 — Move a note to where it belongs: lore move](../../docs/team/identity-and-stores.md#step-6--move-a-note-to-where-it-belongs-lore-move) |

### lore org

| Command | What it does | Guide |
|---|---|---|
| `lore org` … | Choose and inspect which org a repo belongs to | — |
| `lore org create` | Start a new org on a lore store: suggest it to teammates, add it with your identity, join its roster | — |
| `lore org import` | Adopt the org entry a team suggests in its store, with your identity, and join its roster | [team/identity-and-stores › Step 4 — Join your company's setup in one command: lore org import](../../docs/team/identity-and-stores.md#step-4--join-your-companys-setup-in-one-command-lore-org-import) |
| `lore org link` | Link this repo to an internal project in the org's store | [team/commands › lore org link](../../docs/team/commands.md#lore-org-link) |
| `lore org set` | Write the committed .lore/org.yaml (only in the org's own repos) | [team/identity-and-stores › Step 5 — Let a repo name its org for everyone: lore org set](../../docs/team/identity-and-stores.md#step-5--let-a-repo-name-its-org-for-everyone-lore-org-set) |
| `lore org unlink` | Remove this repo's link to an internal project in the org's store | [team/commands › lore org link](../../docs/team/commands.md#lore-org-link) |
| `lore org use` | Choose the org entry for this project on this machine (not committed) | [team/commands › lore org use](../../docs/team/commands.md#lore-org-use) |

### lore pattern

| Command | What it does | Guide |
|---|---|---|
| `lore pattern` … | lore pattern commands | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern add` | Add a new pattern | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern archive` | Archive a pattern (soft-delete via archived_at) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern delete` | Delete a pattern (archive is usually what you want; a tombstone keeps old copies from returning) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern edit` | Edit pattern fields (use `--supersedes` on add for audited body changes) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern list` | List patterns | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern search` | Full-text search across pattern title + body | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern show` | Show pattern details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern unarchive` | Unarchive a pattern (clear archived_at) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

### lore pii-pattern

| Command | What it does | Guide |
|---|---|---|
| `lore pii-pattern` … | Manage custom PII/secret detection patterns | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore pii-pattern add` | Replaced by `lore secrets rule add` | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore pii-pattern disable` | Replaced by `lore secrets rule off` | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore pii-pattern enable` | Replaced by `lore secrets rule on` | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore pii-pattern list` | List PII patterns | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |

### lore plan

| Command | What it does | Guide |
|---|---|---|
| `lore plan` … | lore plan commands | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |
| `lore plan add` | Add a new plan | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |
| `lore plan delete` | Delete a plan (its tasks and missions are detached; a tombstone is kept) | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |
| `lore plan edit` | Edit plan fields (only flags you pass are applied) | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |
| `lore plan list` | List plans | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |
| `lore plan search` | Full-text search across plan title + body + status | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |
| `lore plan show` | Show plan details | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |

### lore playbook

| Command | What it does | Guide |
|---|---|---|
| `lore playbook` … | lore playbook commands | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook add` | Add a new playbook | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook archive` | Archive a playbook (soft-delete via archived_at) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook delete` | Delete a playbook (archive is usually what you want; a tombstone keeps old copies from returning) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook edit` | Edit playbook fields | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook list` | List playbooks | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook search` | Full-text search across playbook name + body | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook show` | Show playbook details | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook unarchive` | Unarchive a playbook (clear archived_at) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |

### lore plugin

| Command | What it does | Guide |
|---|---|---|
| `lore plugin` … | Manage trusted-plugin allowlist | [operations/plugins-and-db-config › Step 1 — Trust a plugin by its fingerprint](../../docs/operations/plugins-and-db-config.md#step-1--trust-a-plugin-by-its-fingerprint) |
| `lore plugin list` | List trusted plugins | [operations/plugins-and-db-config › Step 1 — Trust a plugin by its fingerprint](../../docs/operations/plugins-and-db-config.md#step-1--trust-a-plugin-by-its-fingerprint) |
| `lore plugin trust` | Trust a plugin by name + sha256 | [operations/plugins-and-db-config › Step 1 — Trust a plugin by its fingerprint](../../docs/operations/plugins-and-db-config.md#step-1--trust-a-plugin-by-its-fingerprint) |
| `lore plugin untrust` | Remove a trusted-plugin entry | [operations/plugins-and-db-config › Step 1 — Trust a plugin by its fingerprint](../../docs/operations/plugins-and-db-config.md#step-1--trust-a-plugin-by-its-fingerprint) |

### lore policy

| Command | What it does | Guide |
|---|---|---|
| `lore policy` … | lore policy commands | — |
| `lore policy set` | Set a policy rule at a level (may only tighten a higher level) | — |
| `lore policy show` | Show the effective value of every policy rule and where it comes from | — |
| `lore policy unset` | Remove a policy rule's row at a level (the next level up applies again) | — |

### lore project

| Command | What it does | Guide |
|---|---|---|
| `lore project` … | Manage projects | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project archive` | Archive a project (soft-delete via archived_at) | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project delete` | Delete a project (a tombstone; archive is usually what you want) | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project list` | List projects in the current DB | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project shared-init` | Retired (0.1.18): use `lore store init` + `lore store add` | [sync/shared-database-mode-b › Step 1 — Make the store and attach it in each repo](../../docs/sync/shared-database-mode-b.md#step-1--make-the-store-and-attach-it-in-each-repo) |
| `lore project shared-list` | Retired (0.1.18): use `lore store list` | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project show` | Show project details (with attached repos) | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project unarchive` | Unarchive a project (clear archived_at) | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |

### lore prompt

| Command | What it does | Guide |
|---|---|---|
| `lore prompt` … | lore prompt commands | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt add` | Add a new prompt | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt archive` | Archive a prompt (soft-delete via archived_at) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt delete` | Delete a prompt (archive is usually what you want; a tombstone keeps old copies from returning) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt edit` | Edit prompt fields | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt list` | List prompts | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt search` | Full-text search across prompt name + body | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt show` | Show prompt details | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt unarchive` | Unarchive a prompt (clear archived_at) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |

### lore querylog

| Command | What it does | Guide |
|---|---|---|
| `lore querylog` … | Inspect querylogs | — |
| `lore querylog list` | List querylogs | [knowledge/search › Pitfalls](../../docs/knowledge/search.md#pitfalls) |

### lore reminder

| Command | What it does | Guide |
|---|---|---|
| `lore reminder` … | lore reminder commands | [tasks/reminders › Step 2 — Mark reminders done](../../docs/tasks/reminders.md#step-2--mark-reminders-done) |
| `lore reminder add` | Add a reminder (--due YYYY-MM-DD) | [tasks/reminders › Step 2 — Mark reminders done](../../docs/tasks/reminders.md#step-2--mark-reminders-done) |
| `lore reminder delete` | Delete a reminder (a tombstone is kept so old copies never return) | [tasks/reminders › Step 2 — Mark reminders done](../../docs/tasks/reminders.md#step-2--mark-reminders-done) |
| `lore reminder done` | Mark a reminder done (recurring → reschedules) | [tasks/reminders › Step 2 — Mark reminders done](../../docs/tasks/reminders.md#step-2--mark-reminders-done) |
| `lore reminder list` | List pending reminders (or --done to see completed) | [tasks/reminders › Step 2 — Mark reminders done](../../docs/tasks/reminders.md#step-2--mark-reminders-done) |

### lore render

| Command | What it does | Guide |
|---|---|---|
| `lore render` | Render .lore/LORE.md from scoped knowledge and point your AI tools at it | [knowledge/render-and-agent-files › Step 5 — Point another tool](../../docs/knowledge/render-and-agent-files.md#step-5--point-another-tool) |

### lore renderhistory

| Command | What it does | Guide |
|---|---|---|
| `lore renderhistory` … | Inspect renderhistorys | — |
| `lore renderhistory list` | List renderhistorys | [knowledge/render-and-agent-files › Try it: what did the tool read?](../../docs/knowledge/render-and-agent-files.md#try-it-what-did-the-tool-read) |

### lore repair

| Command | What it does | Guide |
|---|---|---|
| `lore repair` | Recover from a corrupted DB | [operations/backup-restore-repair › Step 4 — Repair a damaged database](../../docs/operations/backup-restore-repair.md#step-4--repair-a-damaged-database) |

### lore repo

| Command | What it does | Guide |
|---|---|---|
| `lore repo` … | Manage repos within a project | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo add` | Register a repo within the current project | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo archive` | Archive a repo (soft-delete via archived_at) | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo delete` | Delete a repo (a tombstone; archive is usually what you want) | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo list` | List repos in the current project | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo unarchive` | Unarchive a repo (clear archived_at) | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |

### lore resource

| Command | What it does | Guide |
|---|---|---|
| `lore resource` … | lore resource commands | — |
| `lore resource add` | Add a resource of a kind | — |
| `lore resource archive` | Archive a resource (refs stay; new refs are refused) | — |
| `lore resource check` | Stamp last_checked: a person or agent looked and the facts are right | — |
| `lore resource delete` | Delete a resource (a tombstone is kept) | — |
| `lore resource edit` | Edit a resource's values (stamps last_checked) | — |
| `lore resource field` … | lore resource field commands | — |
| `lore resource field add` | Add a field to a resource kind (a kind_fields settings row) | — |
| `lore resource field edit` | Edit a field of a resource kind | — |
| `lore resource field remove` | Remove a field from a resource kind | — |
| `lore resource field rename` | Rename a field (a label change: rows key values by fld_ id) | — |
| `lore resource kind` … | lore resource kind commands | — |
| `lore resource kind add` | Add a resource kind (a kinds settings row) | — |
| `lore resource kind edit` | Edit a resource kind | — |
| `lore resource kind rename` | Rename a resource kind (a label change; ids stay) | — |
| `lore resource kind show` | Show a resource kind and its fields | — |
| `lore resource kinds` | List resource kinds with their fields and row counts | — |
| `lore resource list` | List resources of a kind | — |
| `lore resource show` | Show one resource | — |
| `lore resource stale` | Resources whose last_checked is older than --days, or never set | — |

### lore restore

| Command | What it does | Guide |
|---|---|---|
| `lore restore` | Restore the project DB from a backup file | [operations/backup-restore-repair › Step 2 — Restore a backup](../../docs/operations/backup-restore-repair.md#step-2--restore-a-backup) |

### lore rule

| Command | What it does | Guide |
|---|---|---|
| `lore rule` … | lore rule commands | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule add` | Add a new rule (a hard constraint: must / should / may) | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule archive` | Archive a rule (hidden from list, render and search; kept) | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule delete` | Delete a rule (a tombstone is kept so old copies never return) | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule edit` | Edit rule fields (only the flags given change) | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule list` | List rules (archived ones only with --archived) | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule search` | Full-text search over rules of every attached store | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule show` | Show rule details | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule unarchive` | Bring an archived rule back | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |

### lore run

| Command | What it does | Guide |
|---|---|---|
| `lore run` … | Log + inspect agent runs (start/step/end/cancel/replay/show/list) | [tasks/run-log › Step 2 — Log the steps](../../docs/tasks/run-log.md#step-2--log-the-steps) |
| `lore run cancel` | Cancel a run (alias for `end --outcome=cancelled`) | [tasks/run-log › Step 2 — Log the steps](../../docs/tasks/run-log.md#step-2--log-the-steps) |
| `lore run end` | Close a run with outcome (success \| partial \| failed \| cancelled) | [tasks/run-log › Step 2 — Log the steps](../../docs/tasks/run-log.md#step-2--log-the-steps) |
| `lore run list` | List runs | [tasks/run-log › Step 2 — Log the steps](../../docs/tasks/run-log.md#step-2--log-the-steps) |
| `lore run replay` | Print the full step-by-step transcript of a run | [tasks/run-log › Step 2 — Log the steps](../../docs/tasks/run-log.md#step-2--log-the-steps) |
| `lore run show` | Show a run's metadata (status, outcome, tokens, cost) | [tasks/run-log › Step 2 — Log the steps](../../docs/tasks/run-log.md#step-2--log-the-steps) |
| `lore run start` | Open a run (records what agent is attempting which task with which model) | [tasks/run-log › Step 2 — Log the steps](../../docs/tasks/run-log.md#step-2--log-the-steps) |
| `lore run step` | Append a step to a run (prompt / tool / verify / reflect / decide / error / note) | [tasks/run-log › Step 2 — Log the steps](../../docs/tasks/run-log.md#step-2--log-the-steps) |

### lore search

| Command | What it does | Guide |
|---|---|---|
| `lore search` … | Unified FTS5 search across all entities (fans out per-entity + RRF-merges) | [knowledge/search › Try it: whole words, prefixes and several words](../../docs/knowledge/search.md#try-it-whole-words-prefixes-and-several-words) |
| `lore search rebuild` | Drop + recreate FTS5 tables and reindex from source rows | [knowledge/search › Keep the index healthy](../../docs/knowledge/search.md#keep-the-index-healthy) |
| `lore search status` | Per-entity FTS5 row counts + drift health | [knowledge/search › Keep the index healthy](../../docs/knowledge/search.md#keep-the-index-healthy) |

### lore secrets

| Command | What it does | Guide |
|---|---|---|
| `lore secrets` … | lore secrets commands | — |
| `lore secrets allow` … | Record one finding as not a secret for one row, field and rule | — |
| `lore secrets allow list` | List the allow entries | — |
| `lore secrets allow rm` | Remove an allow entry (the finding is reported again) | — |
| `lore secrets rule` … | lore secrets rule commands | — |
| `lore secrets rule add` | Add a lore secret rule on top of the defaults | — |
| `lore secrets rule edit` | Change a secret rule (lore defaults and gitleaks ids included) | — |
| `lore secrets rule off` | Switch a secret rule off at a level, with a reason | — |
| `lore secrets rule on` | Switch a secret rule back on at a level | — |
| `lore secrets rules` | List secret rule rows (added, changed or off) with their level | — |

### lore session

| Command | What it does | Guide |
|---|---|---|
| `lore session` … | Inspect sessions | — |
| `lore session list` | List sessions | [tasks/run-log › Pitfalls](../../docs/tasks/run-log.md#pitfalls) |

### lore setup

| Command | What it does | Guide |
|---|---|---|
| `lore setup` | Run one-time migrations after install/upgrade (FTS index + future migrations) | [knowledge/first-project › Step 2 — Run setup once](../../docs/knowledge/first-project.md#step-2--run-setup-once) *(brief)* |

### lore share

| Command | What it does | Guide |
|---|---|---|
| `lore share` | Copy a row to a wider audience (new id; shows the text and asks) | [team/commands › lore share](../../docs/team/commands.md#lore-share) |

### lore skill

| Command | What it does | Guide |
|---|---|---|
| `lore skill` … | Meta-tools for the skill bundle (compile, check) | — |
| `lore skill compile` | Compress the canonical bundle into a smaller .md with a model | [knowledge/render-and-agent-files › Build a skill file](../../docs/knowledge/render-and-agent-files.md#build-a-skill-file) *(brief)* |

### lore snapshot

| Command | What it does | Guide |
|---|---|---|
| `lore snapshot` … | lore snapshot commands | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore snapshot add` | Add a snapshot (--title and --body required) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore snapshot archive` | Archive a snapshot (soft-delete via archived_at) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore snapshot delete` | Delete a snapshot (archive is usually what you want; a tombstone keeps old copies from returning) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore snapshot list` | List snapshots | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore snapshot search` | Full-text search across snapshot title + body | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore snapshot show` | Show snapshot details | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore snapshot unarchive` | Unarchive a snapshot (clear archived_at) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |

### lore spec

| Command | What it does | Guide |
|---|---|---|
| `lore spec` … | Write the generated CLI reference and OpenAPI file | — |
| `lore spec docs` | Write cli-commands.md (commands and errors blocks) | — |
| `lore spec openapi` | Write openapi.json | — |

### lore sql

| Command | What it does | Guide |
|---|---|---|
| `lore sql` … | lore sql commands | — |
| `lore sql run` | Run one read-only SQL query over every table of the repo's lens (sensitive values masked) | — |

### lore store

| Command | What it does | Guide |
|---|---|---|
| `lore store` … | Manage lore stores: lore-only repos holding internal and personal projects | — |
| `lore store add` | Attach an existing lore store to this repo (.lore/stores.yaml) | [sync/shared-database-mode-b › Step 1 — Make the store and attach it in each repo](../../docs/sync/shared-database-mode-b.md#step-1--make-the-store-and-attach-it-in-each-repo) |
| `lore store doctor` | Check every configured store: reachable, marker and files, unpushed work, entries sharing the clone | [team/commands › lore store doctor](../../docs/team/commands.md#lore-store-doctor) |
| `lore store forget` | Delete this machine's clone of a store (refused while it holds unpushed work) | [team/identity-and-stores › Step 7 — Look inside a store, or drop your copy: lore store open and forget](../../docs/team/identity-and-stores.md#step-7--look-inside-a-store-or-drop-your-copy-lore-store-open-and-forget) |
| `lore store init` | Make a git repo or folder a lore store and attach it (me = your personal store) | [team/commands › lore store init](../../docs/team/commands.md#lore-store-init) |
| `lore store list` | List the stores this repo sees, in precedence order | — |
| `lore store open` | Check out a store branch for browsing (detached, under ~/.lore/worktrees) | [team/identity-and-stores › Step 7 — Look inside a store, or drop your copy: lore store open and forget](../../docs/team/identity-and-stores.md#step-7--look-inside-a-store-or-drop-your-copy-lore-store-open-and-forget) |
| `lore store remove` | Detach a store from this repo (its rows stay in the store) | — |
| `lore store show` | List the configured stores, their clones and (with --links) the link index | [team/commands › lore store show](../../docs/team/commands.md#lore-store-show) |

### lore suggestion

| Command | What it does | Guide |
|---|---|---|
| `lore suggestion` … | lore suggestion commands | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore suggestion add` | Add a new suggestion | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore suggestion delete` | Delete a suggestion (a tombstone keeps old copies from returning) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore suggestion edit` | Edit suggestion fields | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore suggestion list` | List suggestions | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore suggestion search` | Full-text search across suggestion title + body + status | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore suggestion show` | Show suggestion details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

### lore support-bundle

| Command | What it does | Guide |
|---|---|---|
| `lore support-bundle` | Produce a sanitized incident-report bundle | [operations/doctor › Send a support bundle with a bug report](../../docs/operations/doctor.md#send-a-support-bundle-with-a-bug-report) *(brief)* |

### lore sync

| Command | What it does | Guide |
|---|---|---|
| `lore sync` … | Sync lore.db with the git-tracked .lore/data files | [sync/how-notes-travel-with-git › Try it — turn sync off for one command](../../docs/sync/how-notes-travel-with-git.md#try-it--turn-sync-off-for-one-command) |
| `lore sync ci-merge` | Merge a base branch into this branch when only lore data conflicts (for CI) | [sync/github-workflow › Step 1 — Try ci-merge on your machine](../../docs/sync/github-workflow.md#step-1--try-ci-merge-on-your-machine) |
| `lore sync conflicts` | List versions kept aside when both sides edited the same row | [sync/conflicts-and-duplicates › Step 3 — The file and your database both changed](../../docs/sync/conflicts-and-duplicates.md#step-3--the-file-and-your-database-both-changed) |
| `lore sync dupes` | List rows recorded twice (same text, different ids) | [sync/conflicts-and-duplicates › Step 4 — The same fact written twice](../../docs/sync/conflicts-and-duplicates.md#step-4--the-same-fact-written-twice) |
| `lore sync export` | Rewrite .lore/data files from lore.db | [sync/trash-and-purge › Step 2 — Rebuild .lore/data from the database](../../docs/sync/trash-and-purge.md#step-2--rebuild-loredata-from-the-database) |
| `lore sync fix-projects` | Collapse several project rows into one (two parallel bootstraps met in a merge) | [sync/conflicts-and-duplicates › Step 5 — Two people set up lore on separate branches](../../docs/sync/conflicts-and-duplicates.md#step-5--two-people-set-up-lore-on-separate-branches) |
| `lore sync install-action` | Add a GitHub workflow that merges lore data in pull requests (opt-in) | [sync/github-workflow › Step 4 — Or let it run by itself, or remove it](../../docs/sync/github-workflow.md#step-4--or-let-it-run-by-itself-or-remove-it) |
| `lore sync install-git` | Install lore's merge driver, .gitattributes rules and hook blocks now | [sync/hooks-and-merge-driver › Step 1 — See what lore set up](../../docs/sync/hooks-and-merge-driver.md#step-1--see-what-lore-set-up) |
| `lore sync merge-rows` | Fold a duplicate row into another (references move, the dropped row is trashed) | [sync/conflicts-and-duplicates › Step 4 — The same fact written twice](../../docs/sync/conflicts-and-duplicates.md#step-4--the-same-fact-written-twice) |
| `lore sync peek` | List the lore rows another branch carries, without checking it out | [sync/branches-peek-promote › Step 2 — Peek at a teammate's branch](../../docs/sync/branches-peek-promote.md#step-2--peek-at-a-teammates-branch) |
| `lore sync promote` | Commit rows onto another local branch (e.g. an urgent rule onto main) | [sync/branches-peek-promote › Step 3 — Promote one rule to main](../../docs/sync/branches-peek-promote.md#step-3--promote-one-rule-to-main) |
| `lore sync purge` | Durably remove rows archived before a cutoff | [sync/trash-and-purge › Step 3 — Purge archived notes for good](../../docs/sync/trash-and-purge.md#step-3--purge-archived-notes-for-good) |
| `lore sync resolve` | Close a conflict, keeping the current version or the saved other one | [sync/conflicts-and-duplicates › Step 3 — The file and your database both changed](../../docs/sync/conflicts-and-duplicates.md#step-3--the-file-and-your-database-both-changed) |
| `lore sync status` | Show sync state: baseline, pending exports, conflicts, broken files | [sync/how-notes-travel-with-git › Step 4 — Run a pass by hand when you want to see it](../../docs/sync/how-notes-travel-with-git.md#step-4--run-a-pass-by-hand-when-you-want-to-see-it) |
| `lore sync trash` … | List rows removed because their file disappeared | [sync/trash-and-purge › Step 1 — Get back a note whose file disappeared](../../docs/sync/trash-and-purge.md#step-1--get-back-a-note-whose-file-disappeared) |
| `lore sync trash restore` | Put a trashed row back (it is re-exported on the next pass) | [sync/trash-and-purge › Step 1 — Get back a note whose file disappeared](../../docs/sync/trash-and-purge.md#step-1--get-back-a-note-whose-file-disappeared) |
| `lore sync uninstall-action` | Remove the workflow added by install-action | [sync/how-notes-travel-with-git › Try it — turn sync off for one command](../../docs/sync/how-notes-travel-with-git.md#try-it--turn-sync-off-for-one-command) |

### lore tables

| Command | What it does | Guide |
|---|---|---|
| `lore tables` | List every data table and its total record count | [knowledge/first-project › Try it: check that everything is healthy](../../docs/knowledge/first-project.md#try-it-check-that-everything-is-healthy) |

### lore tag

| Command | What it does | Guide |
|---|---|---|
| `lore tag` … | lore tag commands | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |
| `lore tag add` | Create a new tag in the current project | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |
| `lore tag attach` | Attach a tag to an entity | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |
| `lore tag detach` | Detach a tag from an entity | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |
| `lore tag list` | List tags in the current project | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |

### lore task

| Command | What it does | Guide |
|---|---|---|
| `lore task` … | lore task commands | — |
| `lore task add` | Add a new task | [tasks/tasks › Step 1 — Add tasks](../../docs/tasks/tasks.md#step-1--add-tasks) |
| `lore task cancel` | Cancel a task | [tasks/tasks › Step 8 — Cancel or delete a task](../../docs/tasks/tasks.md#step-8--cancel-or-delete-a-task) *(brief)* |
| `lore task claim` | Claim a task (or coordinate a mission) on the signals ref, with a lease | [team/commands › lore task claim](../../docs/team/commands.md#lore-task-claim) |
| `lore task deferred` | Show deferred tasks (snoozed; deferred_until in the future) | [tasks/commitment-and-snooze › Step 4 — Snooze a task until a date](../../docs/tasks/commitment-and-snooze.md#step-4--snooze-a-task-until-a-date) |
| `lore task delete` | Delete a task (a tombstone is kept so old copies never return) | [tasks/tasks › Step 8 — Cancel or delete a task](../../docs/tasks/tasks.md#step-8--cancel-or-delete-a-task) |
| `lore task done` | Mark a task done (with the code that finished it) | [tasks/tasks › Step 6 — Finish a task](../../docs/tasks/tasks.md#step-6--finish-a-task) |
| `lore task edit` | Edit task fields (only flags you pass are applied) | [tasks/tasks › Step 5 — Change a task](../../docs/tasks/tasks.md#step-5--change-a-task) |
| `lore task heartbeat` | Renew your claim's lease (alive, not progress) | [team/commands › lore task heartbeat](../../docs/team/commands.md#lore-task-heartbeat) |
| `lore task list` | List tasks | [tasks/tasks › Step 2 — See your list](../../docs/tasks/tasks.md#step-2--see-your-list) |
| `lore task merged` | Mark a done task as merged by hand (when lore cannot see the merge) | [tasks/link-commits › Step 5 — Mark a task merged by hand](../../docs/tasks/link-commits.md#step-5--mark-a-task-merged-by-hand) |
| `lore task next` | Claim the next ready task (resume-first, dependencies, gates, priority) | [team/commands › lore task next](../../docs/team/commands.md#lore-task-next) |
| `lore task note` | Record a progress note on a task (pushes your work branch) | [team/commands › lore task note](../../docs/team/commands.md#lore-task-note) |
| `lore task reassign` | Assign a task to someone else, dropping any claim (human override) | [team/agents › Step 3 — Give the task to someone else: lore task reassign](../../docs/team/agents.md#step-3--give-the-task-to-someone-else-lore-task-reassign) |
| `lore task release` | Give back a claim you hold | [team/agents › Step 1 — Give back a claim you hold: lore task release](../../docs/team/agents.md#step-1--give-back-a-claim-you-hold-lore-task-release) |
| `lore task revoke` | Drop the current claim on a task (human override; the holder is fenced out) | [team/agents › Step 2 — Take a claim away: lore task revoke](../../docs/team/agents.md#step-2--take-a-claim-away-lore-task-revoke) |
| `lore task search` | Full-text search across task title + body (BM25 ranked) | [tasks/tasks › Step 7 — Search your tasks](../../docs/tasks/tasks.md#step-7--search-your-tasks) |
| `lore task show` | Show task details | [team/commands › lore task show](../../docs/team/commands.md#lore-task-show) |
| `lore task someday` | Show someday/maybe tasks (parking lot, no commitment) | [tasks/commitment-and-snooze › Step 2 — Look at proposals and ideas](../../docs/tasks/commitment-and-snooze.md#step-2--look-at-proposals-and-ideas) *(brief)* |
| `lore task start` | Mark task as in_progress | [tasks/tasks › Step 3 — Start a task](../../docs/tasks/tasks.md#step-3--start-a-task) *(brief)* |
| `lore task triage` | Show proposed tasks (suggested, not yet committed) | [tasks/commitment-and-snooze › Step 2 — Look at proposals and ideas](../../docs/tasks/commitment-and-snooze.md#step-2--look-at-proposals-and-ideas) *(brief)* |

### lore task-view

| Command | What it does | Guide |
|---|---|---|
| `lore task-view` … | Manage saved task-list filter views | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task-view add` | Save a task filter as a named view | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task-view delete` | Delete a saved task view | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task-view list` | List saved task views | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |

### lore tasklist

| Command | What it does | Guide |
|---|---|---|
| `lore tasklist` … | lore tasklist commands | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |
| `lore tasklist add` | Add a new tasklist | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |
| `lore tasklist delete` | Delete a tasklist (its tasks are detached; a tombstone is kept) | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |
| `lore tasklist edit` | Edit tasklist fields | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |
| `lore tasklist list` | List tasklists | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |
| `lore tasklist search` | Full-text search across tasklist title + body + status | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |
| `lore tasklist show` | Show tasklist details | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |

### lore tastepref

| Command | What it does | Guide |
|---|---|---|
| `lore tastepref` … | lore tastepref commands | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore tastepref add` | Add a new tastepref | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore tastepref delete` | Delete a tastepref (a tombstone keeps old copies from returning) | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore tastepref edit` | Edit tastepref fields | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore tastepref list` | List tasteprefs | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore tastepref search` | Full-text search across tastepref body + scope | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore tastepref show` | Show tastepref details | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |

### lore team

| Command | What it does | Guide |
|---|---|---|
| `lore team` … | Teams on the org roster (@handle resolves to a person or a team) | [team/agents › Teams](../../docs/team/agents.md#teams) |
| `lore team add` | Create or update a team; @handle then resolves to the team | [team/agents › Teams](../../docs/team/agents.md#teams) |
| `lore team list` | List the roster's teams | [team/agents › Teams](../../docs/team/agents.md#teams) |

### lore techdoc

| Command | What it does | Guide |
|---|---|---|
| `lore techdoc` … | lore techdoc commands | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore techdoc add` | Register external documentation (name + base URL) | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore techdoc delete` | Delete a techdoc (a tombstone keeps old copies from returning) | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore techdoc list` | List techdoc registrations | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore techdoc search` | Full-text search across techdoc name + description + base_url | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore techdoc show` | Show techdoc details | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |

### lore tui

| Command | What it does | Guide |
|---|---|---|
| `lore tui` | Browse the knowledge DB in an interactive terminal UI | [knowledge/tui › Step 1 — Open it](../../docs/knowledge/tui.md#step-1--open-it) *(brief)* |

### lore vars

| Command | What it does | Guide |
|---|---|---|
| `lore vars` … | lore vars commands | — |
| `lore vars list` | List project variables (nearest definition per name) | — |
| `lore vars rename` | Rename a variable (the old name stays an alias) | — |
| `lore vars rm` | Remove a variable (a tombstone is kept) | — |
| `lore vars set` | Set a variable (adds it, or changes its value in that place) | — |
| `lore vars show` | Show one variable | — |
| `lore vars where` | Every place that defines a variable | — |

### lore version

| Command | What it does | Guide |
|---|---|---|
| `lore version` | Print version information | [knowledge/first-project › Before you start](../../docs/knowledge/first-project.md#before-you-start) *(brief)* |

### lore whoami

| Command | What it does | Guide |
|---|---|---|
| `lore whoami` | Show the resolved org, what decided it, your identity and what loads here | [team/commands › lore whoami](../../docs/team/commands.md#lore-whoami) |

### lore why-context

| Command | What it does | Guide |
|---|---|---|
| `lore why-context` | Show the last rendered context (what the coding tool saw) | [knowledge/render-and-agent-files › Try it: what did the tool read?](../../docs/knowledge/render-and-agent-files.md#try-it-what-did-the-tool-read) |

### lore workflow

| Command | What it does | Guide |
|---|---|---|
| `lore workflow` … | lore workflow commands | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workflow add` | Add a new workflow | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workflow delete` | Delete a workflow (a tombstone keeps old copies from returning) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workflow edit` | Edit workflow fields | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workflow list` | List workflows | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workflow search` | Full-text search across workflow name + body | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workflow show` | Show workflow details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

### lore workspace

| Command | What it does | Guide |
|---|---|---|
| `lore workspace` … | lore workspace commands | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workspace add` | Add a new workspace | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workspace delete` | Delete a workspace (a tombstone keeps old copies from returning) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workspace edit` | Edit workspace fields | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workspace list` | List workspaces | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workspace search` | Full-text search across workspace name + body | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workspace show` | Show workspace details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

<!-- /commands -->

## Related

- [Flags](flags.md): the flags many commands share.
- [Environment variables](environment.md), [config files](config.md) and [error codes](errors.md).
- [Feature index](features.md): every feature with its code folder, guide and example.
