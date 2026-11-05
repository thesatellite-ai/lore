# Command index

**In short:** this page lists every lore command with its one-line help and a link to the guide that explains it. It is made from the real program, so it always matches the version in this repo. For every flag of one command, run `lore <command> --help`.

How to read it:

- A command marked **…** only groups other commands (like a folder); you run the commands under it, for example `lore task add`, not `lore task`.
- **Guide** is the best page that explains the command. *(brief)* means that page only mentions it.
- Most commands also take the shared flags `--db`, `--project`, `--repo` and `--read-only`; see [Flags](flags.md).

The list below is written by `tests/docs/commands_index.py` from `lore --help` and the guide column of the [feature index](features.md). Do not edit it by hand: change the command's help text or the feature index, then run the script.

Three hidden commands are left out on purpose: `lore merge-driver`, `lore sync hook` and `lore task hook commit-msg`. Git runs them from the hooks and the merge setting that lore installs; you never run them by hand.

<!-- commands -->
504 commands, 428 of them leaf commands (the ones you run; a command marked … only groups others).

Jump to: [actor](#lore-actor) · [agent](#lore-agent) · [api](#lore-api) · [architecturenote](#lore-architecturenote) · [audit](#lore-audit) · [batch](#lore-batch) · [behaviour](#lore-behaviour) · [cache](#lore-cache) · [check](#lore-check) · [comment](#lore-comment) · [commit](#lore-commit) · [completion](#lore-completion) · [config](#lore-config) · [context](#lore-context) · [cookbookrecipe](#lore-cookbookrecipe) · [decision](#lore-decision) · [directive](#lore-directive) · [doctor](#lore-doctor) · [explain](#lore-explain) · [external-source](#lore-external-source) · [gate](#lore-gate) · [handoff](#lore-handoff) · [hold](#lore-hold) · [hotfix](#lore-hotfix) · [identity](#lore-identity) · [inbox](#lore-inbox) · [incident](#lore-incident) · [init](#lore-init) · [land](#lore-land) · [link](#lore-link) · [lint](#lore-lint) · [live](#lore-live) · [machine](#lore-machine) · [memory](#lore-memory) · [meta](#lore-meta) · [migrate](#lore-migrate) · [mission](#lore-mission) · [move](#lore-move) · [org](#lore-org) · [pattern](#lore-pattern) · [pii-pattern](#lore-pii-pattern) · [pin](#lore-pin) · [plan](#lore-plan) · [playbook](#lore-playbook) · [policy](#lore-policy) · [project](#lore-project) · [prompt](#lore-prompt) · [reminder](#lore-reminder) · [render](#lore-render) · [repo](#lore-repo) · [resource](#lore-resource) · [row](#lore-row) · [rule](#lore-rule) · [run](#lore-run) · [scope](#lore-scope) · [search](#lore-search) · [secrets](#lore-secrets) · [session](#lore-session) · [setup](#lore-setup) · [show](#lore-show) · [skill](#lore-skill) · [snapshot](#lore-snapshot) · [spec](#lore-spec) · [sql](#lore-sql) · [store](#lore-store) · [suggestion](#lore-suggestion) · [support-bundle](#lore-support-bundle) · [sync](#lore-sync) · [tag](#lore-tag) · [task](#lore-task) · [tasklist](#lore-tasklist) · [tastepref](#lore-tastepref) · [techdoc](#lore-techdoc) · [trash](#lore-trash) · [tui](#lore-tui) · [upgrade](#lore-upgrade) · [vars](#lore-vars) · [version](#lore-version) · [whoami](#lore-whoami) · [why-context](#lore-why-context) · [workflow](#lore-workflow) · [workspace](#lore-workspace)

### lore actor

| Command | What it does | Guide |
|---|---|---|
| `lore actor` … | The org roster: register, list, show and deactivate people and agents | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |
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
| `lore architecturenote` … | Manage architecture notes | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore architecturenote add` | Add a new architecture note | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore architecturenote delete` | Delete a architecture note (it goes to the trash; lore trash restore brings it back) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
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

### lore batch

| Command | What it does | Guide |
|---|---|---|
| `lore batch` … | Bulk changes: list, show and undo a batch | — |
| `lore batch list` | List bulk actions (batches) run on this machine | — |
| `lore batch show` | Show one batch: its kind, who ran it and the rows it wrote | — |
| `lore batch undo` | Undo a bulk action: restore a delete batch, or move the rows a create batch added to the trash | — |

### lore behaviour

| Command | What it does | Guide |
|---|---|---|
| `lore behaviour` … | Manage behaviours | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore behaviour add` | Add a new behaviour | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore behaviour delete` | Delete a behaviour (it goes to the trash; lore trash restore brings it back) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore behaviour edit` | Edit behaviour fields | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore behaviour list` | List behaviours | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore behaviour search` | Full-text search across behaviour name + body | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore behaviour show` | Show behaviour details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

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
| `lore comment` … | Manage comments on entities | [knowledge/comments-and-tags › Try it: read the comments on a row](../../docs/knowledge/comments-and-tags.md#try-it-read-the-comments-on-a-row) *(brief)* |
| `lore comment add` | Add a comment on a target entity | [knowledge/comments-and-tags › Try it: read the comments on a row](../../docs/knowledge/comments-and-tags.md#try-it-read-the-comments-on-a-row) *(brief)* |
| `lore comment delete` | Delete a comment (it goes to the trash; lore trash restore brings it back) | [knowledge/comments-and-tags › Try it: read the comments on a row](../../docs/knowledge/comments-and-tags.md#try-it-read-the-comments-on-a-row) *(brief)* |
| `lore comment list` | List comments (optionally filtered by entity) | [knowledge/comments-and-tags › Try it: read the comments on a row](../../docs/knowledge/comments-and-tags.md#try-it-read-the-comments-on-a-row) *(brief)* |
| `lore comment search` | FTS5 search across comment body | [knowledge/comments-and-tags › Try it: read the comments on a row](../../docs/knowledge/comments-and-tags.md#try-it-read-the-comments-on-a-row) *(brief)* |

### lore commit

| Command | What it does | Guide |
|---|---|---|
| `lore commit` … | Show the entities linked to a git commit | — |
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
| `lore context` | Print the rules, hotfixes and notes for a file, topic or prompt (the agent's fetch command) | [operations/ci › Step 5 — Give tools the notes they cannot import](../../docs/operations/ci.md#step-5--give-tools-the-notes-they-cannot-import) |

### lore cookbookrecipe

| Command | What it does | Guide |
|---|---|---|
| `lore cookbookrecipe` … | Manage cookbook recipes | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore cookbookrecipe add` | Add a new cookbookrecipe | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore cookbookrecipe delete` | Delete a cookbookrecipe (it goes to the trash; lore trash restore brings it back) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore cookbookrecipe edit` | Edit cookbook-recipe fields | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore cookbookrecipe list` | List cookbookrecipes | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore cookbookrecipe search` | Full-text search across cookbookrecipe title + body + language | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore cookbookrecipe show` | Show cookbookrecipe details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

### lore decision

| Command | What it does | Guide |
|---|---|---|
| `lore decision` … | Manage decisions (architectural records) | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision add` | Add a new decision (context + chosen + alternatives + reasoning) | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision archive` | Archive a decision (hidden from render and search; kept) | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision delete` | Delete a decision (it goes to the trash; lore trash restore brings it back) | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision edit` | Edit decision fields (only the flags given change) | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision list` | List decisions | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision search` | Full-text search over decisions of every attached store | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision show` | Show decision details | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |
| `lore decision unarchive` | Bring an archived decision back | [knowledge/rules-memories-decisions › Step 3 — Add a decision](../../docs/knowledge/rules-memories-decisions.md#step-3--add-a-decision) |

### lore directive

| Command | What it does | Guide |
|---|---|---|
| `lore directive` … | Install or show the lore agent-directive block in agent-loaded markdown | [knowledge/render-and-agent-files › Step 5 — Add only the directive](../../docs/knowledge/render-and-agent-files.md#step-5--add-only-the-directive) |
| `lore directive install` | Install (or refresh) the directive block at the top of one or more files | [knowledge/render-and-agent-files › Step 5 — Add only the directive](../../docs/knowledge/render-and-agent-files.md#step-5--add-only-the-directive) |
| `lore directive show` | Print the directive block (compact; --full for the long form) without writing any file | [knowledge/render-and-agent-files › Step 5 — Add only the directive](../../docs/knowledge/render-and-agent-files.md#step-5--add-only-the-directive) |

### lore doctor

| Command | What it does | Guide |
|---|---|---|
| `lore doctor` | Run health checks; exit 0 healthy, 1 degraded, 2 broken | [operations/doctor › Check lore's health with lore doctor](../../docs/operations/doctor.md#check-lores-health-with-lore-doctor) |

### lore explain

| Command | What it does | Guide |
|---|---|---|
| `lore explain` | Show every copy of a row (live, this branch, the default branch), which one wins and why, and its land decision | [team/commands › lore explain](../../docs/team/commands.md#lore-explain) |

### lore external-source

| Command | What it does | Guide |
|---|---|---|
| `lore external-source` … | Register external knowledge sources (add, enable, disable, list) | [knowledge/import-existing-notes › Register an outside source](../../docs/knowledge/import-existing-notes.md#register-an-outside-source) |
| `lore external-source add` | Register a new external source (disabled by default) | [knowledge/import-existing-notes › Register an outside source](../../docs/knowledge/import-existing-notes.md#register-an-outside-source) |
| `lore external-source disable` | disable an external source | [knowledge/import-existing-notes › Register an outside source](../../docs/knowledge/import-existing-notes.md#register-an-outside-source) |
| `lore external-source enable` | enable an external source | [knowledge/import-existing-notes › Register an outside source](../../docs/knowledge/import-existing-notes.md#register-an-outside-source) |
| `lore external-source list` | List registered external sources | [knowledge/import-existing-notes › Register an outside source](../../docs/knowledge/import-existing-notes.md#register-an-outside-source) |

### lore gate

| Command | What it does | Guide |
|---|---|---|
| `lore gate` … | Human gates: stop agents until a person approves | — |
| `lore gate approve` | Approve a gate (a person on the roster, from the identity file, never an agent) | [team/commands › lore gate request](../../docs/team/commands.md#lore-gate-request) |
| `lore gate request` | Put a gate on a task (agents stop at it until a person approves) | [team/commands › lore gate request](../../docs/team/commands.md#lore-gate-request) |

### lore handoff

| Command | What it does | Guide |
|---|---|---|
| `lore handoff` … | Manage handoffs | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |
| `lore handoff ack` | Acknowledge a handoff (sets status_str=acked) | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |
| `lore handoff add` | Leave a handoff for someone (shows in their inbox) | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |
| `lore handoff delete` | Delete a handoff (it goes to the trash; lore trash restore brings it back) | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |
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
| `lore hotfix` … | Manage hotfixes (loud recurring warnings) | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix add` | Add a new hotfix (a loud recurring warning: we keep hitting this, beware) | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix archive` | Archive a hotfix (hidden from render and search; kept) | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix delete` | Delete a hotfix (it goes to the trash; lore trash restore brings it back) | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix edit` | Edit hotfix fields (only the flags given change) | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix list` | List hotfixes | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix search` | Full-text search over hotfixes of every attached store | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix show` | Show hotfix details | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |
| `lore hotfix unarchive` | Bring an archived hotfix back | [knowledge/other-row-kinds › Step 1 — Add a hotfix](../../docs/knowledge/other-row-kinds.md#step-1--add-a-hotfix) |

### lore identity

| Command | What it does | Guide |
|---|---|---|
| `lore identity` … | Manage persisted identity (identity: in ~/.lore/config.yaml) | [team/identity-and-stores › Step 2 — Set or remove your own identity: lore identity set and unset](../../docs/team/identity-and-stores.md#step-2--set-or-remove-your-own-identity-lore-identity-set-and-unset) |
| `lore identity set` | Persist an identity: identity: in ~/.lore/config.yaml, or one org's (--org) | [team/identity-and-stores › Step 2 — Set or remove your own identity: lore identity set and unset](../../docs/team/identity-and-stores.md#step-2--set-or-remove-your-own-identity-lore-identity-set-and-unset) |
| `lore identity show` | Show the resolved actor identity + which step matched | [team/identity-and-stores › Step 2 — Set or remove your own identity: lore identity set and unset](../../docs/team/identity-and-stores.md#step-2--set-or-remove-your-own-identity-lore-identity-set-and-unset) |
| `lore identity unset` | Clear identity: in ~/.lore/config.yaml (or one org's with --org) | [team/identity-and-stores › Step 2 — Set or remove your own identity: lore identity set and unset](../../docs/team/identity-and-stores.md#step-2--set-or-remove-your-own-identity-lore-identity-set-and-unset) |

### lore inbox

| Command | What it does | Guide |
|---|---|---|
| `lore inbox` | What needs you: assignments, handoffs, gates, expired claims, blocked tasks, findings | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |

### lore incident

| Command | What it does | Guide |
|---|---|---|
| `lore incident` … | Manage incidents | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore incident add` | Add a new incident | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore incident delete` | Delete a incident (it goes to the trash; lore trash restore brings it back) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore incident edit` | Edit incident fields or add --ref pointers (only the flags given change) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
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

### lore link

| Command | What it does | Guide |
|---|---|---|
| `lore link` … | Link git commits to any entity (task / run / mission / decision / …) | [tasks/link-commits › Step 6 — Remove a link](../../docs/tasks/link-commits.md#step-6--remove-a-link) |
| `lore link add` | Link a git commit to an entity (--entity=tsk_… --commit=<sha>) | [tasks/link-commits › Step 6 — Remove a link](../../docs/tasks/link-commits.md#step-6--remove-a-link) |
| `lore link list` | List commit links (filter by --entity or --commit) | [tasks/link-commits › Step 6 — Remove a link](../../docs/tasks/link-commits.md#step-6--remove-a-link) |
| `lore link remove` | Remove a commit link by its ID (a tombstone is kept) | [tasks/link-commits › Step 6 — Remove a link](../../docs/tasks/link-commits.md#step-6--remove-a-link) |

### lore lint

| Command | What it does | Guide |
|---|---|---|
| `lore lint` | Report problems in the attached stores (dangling references, shadowed variables, task cycles, long time spans, duplicate imports, unknown metadata keys, stored secrets) | — |

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

### lore machine

| Command | What it does | Guide |
|---|---|---|
| `lore machine` | List the machine profiles of the attached stores | — |

### lore memory

| Command | What it does | Guide |
|---|---|---|
| `lore memory` … | Manage memories (free-form learned knowledge) | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory add` | Add a memory (free-form learned knowledge) | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory archive` | Archive a memory (hidden from list and search; kept) | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory delete` | Delete a memory (it goes to the trash; lore trash restore brings it back) | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
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
| `lore meta` … | Metadata on any row: define keys, set and unset values, rename, move, export | — |
| `lore meta add` | Add a metadata key to a place's registry | — |
| `lore meta adopt` | Move another place's metadata key rows into this place (ids kept) | — |
| `lore meta delete` | Delete a metadata key and its allowed values | — |
| `lore meta describe` | Set a metadata key's description | — |
| `lore meta edit` | Edit a metadata key (strict, internal, allowed values) | — |
| `lore meta export` | Export metadata values as CSV or JSON lines (id,key,value) | — |
| `lore meta forget` | Remove unused metadata keys for good (tombstones kept) | — |
| `lore meta get` | Show a row's metadata (sensitive values masked unless --show-sensitive) | — |
| `lore meta history` | Dated metadata value changes of one row, from git history (sensitive values masked) | — |
| `lore meta import` | Import metadata values from CSV or JSON lines, as one batch | — |
| `lore meta keys` | List the metadata keys every visible place defines | — |
| `lore meta lint` | List metadata that does not fit its key, and registry problems | — |
| `lore meta move` | Move a metadata key row outward to another place (id kept, values unchanged) | — |
| `lore meta rebuild` | Recreate lost metadata key rows of one place from git history or data rows | — |
| `lore meta rehome` | Re-file the metadata keys a place's rows use that it can no longer see (copy rule: reuse a visible key of that name and type, else a new key row) | — |
| `lore meta rename` | Rename a metadata key (a label change; the id stays) | — |
| `lore meta set` | Set metadata on a row by id | — |
| `lore meta show` | Show a row's metadata (sensitive values masked unless --show-sensitive) | — |
| `lore meta unset` | Remove a metadata key from a row (a parent removes its fields) | — |
| `lore meta value` … | Allowed values of a metadata key (add, rename) | — |
| `lore meta value add` | Add an allowed value to a metadata key | — |
| `lore meta value rename` | Rename an allowed value (a label change; the val_ id stays) | — |

### lore migrate

| Command | What it does | Guide |
|---|---|---|
| `lore migrate` … | Bring a project's data to the current format (status, run, rollback, enable, unlock) | — |
| `lore migrate enable` | Declare a format feature before its first use (raises min_lore_version) | — |
| `lore migrate rollback` | Undo the last migration (local undo before push, new commits after) | — |
| `lore migrate run` | Rewrite v0.1.17 row files (_v: 1) as _v: 2 (the migrated nouns) | — |
| `lore migrate status` | Show the row format of every place, the last migration and the lock | — |
| `lore migrate unlock` | Release this machine's stale migration lock (its process is gone), or with --team a stale team lock | — |

### lore mission

| Command | What it does | Guide |
|---|---|---|
| `lore mission` … | Manage missions (containers for tasks) | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission add` | Add a new mission | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission apply` | Create a mission and its tasks from a YAML plan; a re-run adds nothing | [tasks/missions-and-plans › Step 6 — Turn a plan file into a mission with tasks](../../docs/tasks/missions-and-plans.md#step-6--turn-a-plan-file-into-a-mission-with-tasks) |
| `lore mission delete` | Delete a mission (its tasks go with it: one batch, to the trash) | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission done` | Mark mission as done | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission edit` | Edit mission fields (only flags you pass are applied) | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission list` | List missions (with attached tasks in --json mode) | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission pause` | Mark mission as paused | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission resume` | Mark mission as active (resume from paused) | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission search` | Full-text search across mission title + body + status | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission show` | Show mission details + attached tasks | [tasks/missions-and-plans › Step 4 — Change, find, pause and finish a mission](../../docs/tasks/missions-and-plans.md#step-4--change-find-pause-and-finish-a-mission) |
| `lore mission status` | The whole mission at a glance: ready / waiting / gated / claimed / expired / blocked / done | [tasks/missions-and-plans › Step 3 — See the whole mission at a glance](../../docs/tasks/missions-and-plans.md#step-3--see-the-whole-mission-at-a-glance) |

### lore move

| Command | What it does | Guide |
|---|---|---|
| `lore move` | Move a row to another store (same id for rows on the core; asks first) | [team/identity-and-stores › Step 6 — Move a note to where it belongs: lore move](../../docs/team/identity-and-stores.md#step-6--move-a-note-to-where-it-belongs-lore-move) |

### lore org

| Command | What it does | Guide |
|---|---|---|
| `lore org` … | Choose and inspect which org a repo belongs to | — |
| `lore org create` | Start a new org on a lore store: suggest it to teammates, add it with your identity, join its roster | — |
| `lore org import` | Adopt the org entry a team suggests in its store, with your identity, and join its roster | [team/identity-and-stores › Step 4 — Join your company's setup in one command: lore org import](../../docs/team/identity-and-stores.md#step-4--join-your-companys-setup-in-one-command-lore-org-import) |
| `lore org link` | Link this repo to an internal project in the org's store | [team/commands › lore org link](../../docs/team/commands.md#lore-org-link) |
| `lore org rename` | Rename an org entry on this machine (a label change; the org's id and every row stay) | — |
| `lore org set` | Write the committed .lore/org.yaml (only in the org's own repos) | [team/identity-and-stores › Step 5 — Let a repo name its org for everyone: lore org set](../../docs/team/identity-and-stores.md#step-5--let-a-repo-name-its-org-for-everyone-lore-org-set) |
| `lore org use` | Choose the org entry for this project on this machine (not committed) | [team/commands › lore org use](../../docs/team/commands.md#lore-org-use) |

### lore pattern

| Command | What it does | Guide |
|---|---|---|
| `lore pattern` … | Manage patterns | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern add` | Add a new pattern | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern archive` | Archive a pattern (soft-delete via archived_at) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern delete` | Delete a pattern (to the trash; archive is usually what you want) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern edit` | Edit pattern fields (use `--supersedes` on add for audited body changes) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern list` | List patterns | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern search` | Full-text search across pattern title + body | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern show` | Show pattern details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore pattern unarchive` | Unarchive a pattern (clear archived_at) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

### lore pii-pattern

| Command | What it does | Guide |
|---|---|---|
| `lore pii-pattern` … | Old spelling of lore secrets rule (custom secret detection patterns) | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore pii-pattern add` | Replaced by `lore secrets rule add` | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore pii-pattern disable` | Replaced by `lore secrets rule off` | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore pii-pattern enable` | Replaced by `lore secrets rule on` | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore pii-pattern list` | List PII patterns | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |

### lore pin

| Command | What it does | Guide |
|---|---|---|
| `lore pin` … | Pin how a must rule or critical/high hotfix appears in LORE.md | — |
| `lore pin set` | Pin how a must rule or critical/high hotfix appears in LORE.md: --context full or line | — |
| `lore pin unset` | Remove a pin; render ranks the entry again | — |

### lore plan

| Command | What it does | Guide |
|---|---|---|
| `lore plan` … | Manage plans | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |
| `lore plan add` | Add a new plan | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |
| `lore plan delete` | Move a plan to the trash, with its tasks (one batch; restore with lore trash restore) | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |
| `lore plan edit` | Edit plan fields (only flags you pass are applied) | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |
| `lore plan list` | List plans | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |
| `lore plan search` | Full-text search across plan title + body + status | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |
| `lore plan show` | Show plan details | [tasks/missions-and-plans › Step 5 — Write a plan](../../docs/tasks/missions-and-plans.md#step-5--write-a-plan) |

### lore playbook

| Command | What it does | Guide |
|---|---|---|
| `lore playbook` … | Manage playbooks | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook add` | Add a new playbook | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook archive` | Archive a playbook (soft-delete via archived_at) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook delete` | Delete a playbook (to the trash; archive is usually what you want) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook edit` | Edit playbook fields | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook list` | List playbooks | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook search` | Full-text search across playbook name + body | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook show` | Show playbook details | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore playbook unarchive` | Unarchive a playbook (clear archived_at) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |

### lore policy

| Command | What it does | Guide |
|---|---|---|
| `lore policy` … | Team rules: set, show and unset policies (lower levels may only tighten) | — |
| `lore policy set` | Set a policy rule at a level (may only tighten a higher level) | — |
| `lore policy show` | Show the effective value of every policy rule and where it comes from | — |
| `lore policy unset` | Remove a policy rule's row at a level (the next level up applies again) | — |

### lore project

| Command | What it does | Guide |
|---|---|---|
| `lore project` … | Manage projects | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project archive` | Archive a project (soft-delete via archived_at) | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project delete` | Delete a project (to the trash; archive is usually what you want) | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project list` | List projects in the current DB | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project shared-init` | Retired (0.1.18): use `lore store init` + `lore store add` | [sync/shared-database-mode-b › Step 1 — Make the store and attach it in each repo](../../docs/sync/shared-database-mode-b.md#step-1--make-the-store-and-attach-it-in-each-repo) |
| `lore project shared-list` | Retired (0.1.18): use `lore store list` | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project show` | Show project details (with attached repos) | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project unarchive` | Unarchive a project (clear archived_at) | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |

### lore prompt

| Command | What it does | Guide |
|---|---|---|
| `lore prompt` … | Manage prompts | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt add` | Add a new prompt | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt archive` | Archive a prompt (soft-delete via archived_at) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt delete` | Delete a prompt (to the trash; archive is usually what you want) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt edit` | Edit prompt fields | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt list` | List prompts | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt search` | Full-text search across prompt name + body | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt show` | Show prompt details | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore prompt unarchive` | Unarchive a prompt (clear archived_at) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |

### lore reminder

| Command | What it does | Guide |
|---|---|---|
| `lore reminder` … | Manage time-based reminders | [tasks/reminders › Step 2 — Mark reminders done](../../docs/tasks/reminders.md#step-2--mark-reminders-done) |
| `lore reminder add` | Add a reminder (--due YYYY-MM-DD) | [tasks/reminders › Step 2 — Mark reminders done](../../docs/tasks/reminders.md#step-2--mark-reminders-done) |
| `lore reminder delete` | Delete a reminder (it goes to the trash; lore trash restore brings it back) | [tasks/reminders › Step 2 — Mark reminders done](../../docs/tasks/reminders.md#step-2--mark-reminders-done) |
| `lore reminder done` | Mark a reminder done (recurring → reschedules) | [tasks/reminders › Step 2 — Mark reminders done](../../docs/tasks/reminders.md#step-2--mark-reminders-done) |
| `lore reminder list` | List pending reminders (or --done to see completed) | [tasks/reminders › Step 2 — Mark reminders done](../../docs/tasks/reminders.md#step-2--mark-reminders-done) |

### lore render

| Command | What it does | Guide |
|---|---|---|
| `lore render` | Render .lore/LORE.md from scoped knowledge and point your AI tools at it | [knowledge/render-and-agent-files › Step 5 — Point another tool](../../docs/knowledge/render-and-agent-files.md#step-5--point-another-tool) |

### lore repo

| Command | What it does | Guide |
|---|---|---|
| `lore repo` … | Manage repos within a project | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo add` | Register a repo within the current project | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo archive` | Archive a repo (soft-delete via archived_at) | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo delete` | Delete a repo (to the trash; archive is usually what you want) | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo fork` | This clone is a fork (or --split): give it its own permanent repo id | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo list` | List repos in the current project | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo merge-ids` | Rewrite _o.repo of rows stamped with an alias repo id to the winning id, then clear the aliases (PROVENANCE 11.9) | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo set-url` | Confirm this repo's address: the primary (moved or renamed), a --mirror, or --remove one | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo unarchive` | Unarchive a repo (clear archived_at) | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |

### lore resource

| Command | What it does | Guide |
|---|---|---|
| `lore resource` … | Infrastructure facts: servers, domains, accounts and the refs between them | — |
| `lore resource add` | Add a resource of a kind | — |
| `lore resource adopt` | Move another place's resource kind and field rows into this place (ids kept) | — |
| `lore resource archive` | Archive a resource (refs stay; new refs are refused) | — |
| `lore resource check` | Stamp last_checked: a person or agent looked and the facts are right | — |
| `lore resource clone` | Add a resource with another resource's values (--set overrides) | — |
| `lore resource delete` | Delete a resource, or every resource of a kind matching --where (to the trash) | — |
| `lore resource edit` | Edit a resource's values (stamps last_checked) | — |
| `lore resource export` | Export resources of a kind in the import format (sensitive values masked) | — |
| `lore resource field` … | Fields of a resource kind (add, edit, rename, retype, remove) | — |
| `lore resource field add` | Add a field to a resource kind (a kind_fields settings row) | — |
| `lore resource field edit` | Edit a field of a resource kind | — |
| `lore resource field remove` | Remove a field from a resource kind | — |
| `lore resource field rename` | Rename a field (a label change: rows key values by fld_ id) | — |
| `lore resource field retype` | Change a field's type: every value is checked first, then converted (the field id stays) | — |
| `lore resource graph` | Print the ref tree of a resource (--down: what it uses; --up: what uses it) | — |
| `lore resource history` | Field changes of one resource, newest first, with actor, date and batch (sensitive values masked) | — |
| `lore resource impact` | What breaks if a resource goes down: every resource that depends on it, with open linked tasks and incidents | — |
| `lore resource import` | Import resources from a csv, json or yaml file (one batch, all or nothing) | — |
| `lore resource kind` … | Resource kinds (add, edit, rename, show) | — |
| `lore resource kind add` | Add a resource kind (a kinds settings row) | — |
| `lore resource kind edit` | Edit a resource kind | — |
| `lore resource kind rename` | Rename a resource kind (a label change; ids stay) | — |
| `lore resource kind show` | Show a resource kind and its fields | — |
| `lore resource kinds` | List resource kinds with their fields and row counts | — |
| `lore resource lint` | Find dangling and bare-name refs, missing required fields, deprecated fields, due reminders and duplicates | — |
| `lore resource list` | List resources of a kind | — |
| `lore resource merge` | Fold a duplicate resource into another of the same kind: refs move, values fill, the duplicate is archived | — |
| `lore resource rehome` | List the kinds and refs a place can no longer see after its chain changed | — |
| `lore resource rename` | Rename a resource (ids and refs stay) | — |
| `lore resource repoint` | Move every ref from one resource to another of the same kind, then archive the old one | — |
| `lore resource report` | Report on the resources in view: counts per kind, stale, unchecked, dangling, quarantined, open links, secret references to rotate | — |
| `lore resource show` | Show one resource | — |
| `lore resource stale` | Resources whose last_checked is older than --days, or never set | — |

### lore row

| Command | What it does | Guide |
|---|---|---|
| `lore row` … | Any row by id: show it, or move it to another store | — |
| `lore row move` | Move any row to another store, keeping its id (the old copy is tombstoned) | — |
| `lore row show` | Show any row by id, with --origin for where it came from | — |

### lore rule

| Command | What it does | Guide |
|---|---|---|
| `lore rule` … | Manage rules (hard constraints) | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule add` | Add a new rule (a hard constraint: must / should / may) | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule archive` | Archive a rule (hidden from list, render and search; kept) | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule delete` | Delete a rule (it goes to the trash; lore trash restore brings it back) | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule edit` | Edit rule fields (only the flags given change) | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule list` | List rules (archived ones only with --archived) | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule search` | Full-text search over rules of every attached store | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule show` | Show rule details | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |
| `lore rule unarchive` | Bring an archived rule back | [knowledge/rules-memories-decisions › Step 1 — Add rules](../../docs/knowledge/rules-memories-decisions.md#step-1--add-rules) |

### lore run

| Command | What it does | Guide |
|---|---|---|
| `lore run` … | Log agent runs (start, end, cancel) | [tasks/run-log › Step 2 — Log the steps](../../docs/tasks/run-log.md#step-2--log-the-steps) |
| `lore run cancel` | Cancel a run (alias for `end --outcome=cancelled`) | [tasks/run-log › Step 2 — Log the steps](../../docs/tasks/run-log.md#step-2--log-the-steps) |
| `lore run end` | Close a run with outcome (success \| partial \| failed \| cancelled) | [tasks/run-log › Step 2 — Log the steps](../../docs/tasks/run-log.md#step-2--log-the-steps) |
| `lore run start` | Open a run (records what agent is attempting which task with which model) | [tasks/run-log › Step 2 — Log the steps](../../docs/tasks/run-log.md#step-2--log-the-steps) |

### lore scope

| Command | What it does | Guide |
|---|---|---|
| `lore scope` … | Directory scopes for rules and hotfixes (ids are kept when a folder is renamed) | — |
| `lore scope list` | List every folder scope of this repo with its entries and file | — |
| `lore scope move` | Relabel a folder scope after a folder rename (ids unchanged, no row rewritten) | — |
| `lore scope show` | Show the scope chain for a path: which rules apply and which file serves each level | — |

### lore search

| Command | What it does | Guide |
|---|---|---|
| `lore search` … | Unified full-text search across all entities (fans out per entity + RRF-merges) | [knowledge/search › Try it: whole words, prefixes and several words](../../docs/knowledge/search.md#try-it-whole-words-prefixes-and-several-words) |
| `lore search rebuild` | Drop + recreate FTS5 tables and reindex from source rows | [knowledge/search › Keep the index healthy](../../docs/knowledge/search.md#keep-the-index-healthy) |
| `lore search status` | Per-entity FTS5 row counts + drift health | [knowledge/search › Keep the index healthy](../../docs/knowledge/search.md#keep-the-index-healthy) |

### lore secrets

| Command | What it does | Guide |
|---|---|---|
| `lore secrets` … | Secret references and who holds them (list, check, move, offboarding) | — |
| `lore secrets allow` … | Record one finding as not a secret for one row, field and rule | — |
| `lore secrets allow list` | List the allow entries | — |
| `lore secrets allow rm` | Remove an allow entry (the finding is reported again) | — |
| `lore secrets check` | Verify each referenced entry by its name (the value is read and dropped); adds the store-then-rotate tasks | — |
| `lore secrets link` | Fill a ${secret:unstored} placeholder with the entry it is now stored in | — |
| `lore secrets list` | Every secret reference in this view: entry, holder, row and field (opens no vault) | — |
| `lore secrets move` | Rewrite the holder of matching references (one batch; never copies a secret) | — |
| `lore secrets relabel` | Rewrite each reference's holder label from its holder id (one batch) | — |
| `lore secrets rename-entry` | Rewrite an entry name everywhere (a renamed vault entry; one batch) | — |
| `lore secrets rule` … | Secret detection rules on top of the defaults (add, edit, off, on) | — |
| `lore secrets rule add` | Add a lore secret rule on top of the defaults | — |
| `lore secrets rule edit` | Change a secret rule (lore defaults and gitleaks ids included) | — |
| `lore secrets rule off` | Switch a secret rule off at a level, with a reason | — |
| `lore secrets rule on` | Switch a secret rule back on at a level | — |
| `lore secrets rules` | List secret rule rows (added, changed or off) with their level | — |
| `lore secrets show` | Show a secret reference or a row's references; --reveal prints the value (asks every time) | — |

### lore session

| Command | What it does | Guide |
|---|---|---|
| `lore session` … | Inspect this machine's agent sessions (local only, never synced) | — |
| `lore session list` | List agent sessions on this machine: tool, session, model, version, place | [tasks/run-log › Pitfalls](../../docs/tasks/run-log.md#pitfalls) |

### lore setup

| Command | What it does | Guide |
|---|---|---|
| `lore setup` | Run one-time migrations after install/upgrade (FTS index + future migrations) | [knowledge/first-project › Step 2 — Run setup once](../../docs/knowledge/first-project.md#step-2--run-setup-once) *(brief)* |

### lore show

| Command | What it does | Guide |
|---|---|---|
| `lore show` | Show any row by id (--origin: where it came from) | — |

### lore skill

| Command | What it does | Guide |
|---|---|---|
| `lore skill` … | Meta-tools for the skill bundle | — |
| `lore skill compile` | Compress the canonical bundle into a smaller .md with a model | [knowledge/render-and-agent-files › Build a skill file](../../docs/knowledge/render-and-agent-files.md#build-a-skill-file) *(brief)* |

### lore snapshot

| Command | What it does | Guide |
|---|---|---|
| `lore snapshot` … | Manage snapshots (point-in-time knowledge captures) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore snapshot add` | Add a snapshot (--title and --body required) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore snapshot archive` | Archive a snapshot (soft-delete via archived_at) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
| `lore snapshot delete` | Delete a snapshot (to the trash; archive is usually what you want) | [knowledge/other-row-kinds › What they are and when you want them](../../docs/knowledge/other-row-kinds.md#what-they-are-and-when-you-want-them) *(brief)* |
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
| `lore sql` … | Run one read-only SQL query over every table of the repo's lens (sensitive values masked) | — |
| `lore sql run` | Run one read-only SQL query over every table of the repo's lens (sensitive values masked) | — |

### lore store

| Command | What it does | Guide |
|---|---|---|
| `lore store` … | Manage lore stores: lore-only repos holding internal and personal projects | — |
| `lore store add` | Attach an existing lore store to this repo (.lore/stores.yaml) | [sync/shared-database-mode-b › Step 1 — Make the store and attach it in each repo](../../docs/sync/shared-database-mode-b.md#step-1--make-the-store-and-attach-it-in-each-repo) |
| `lore store doctor` | Check every configured store: reachable, marker and files, unpushed work, entries sharing the clone | [team/commands › lore store doctor](../../docs/team/commands.md#lore-store-doctor) |
| `lore store folder` … | Project folders inside a store | — |
| `lore store folder add` | Create a folder in a store (shown only in repos that attach it) | — |
| `lore store forget` | Delete this machine's clone of a store (refused while it holds unpushed work) | [team/identity-and-stores › Step 7 — Look inside a store, or drop your copy: lore store open and forget](../../docs/team/identity-and-stores.md#step-7--look-inside-a-store-or-drop-your-copy-lore-store-open-and-forget) |
| `lore store init` | Make a git repo or folder a lore store and attach it (me = your personal store) | [team/commands › lore store init](../../docs/team/commands.md#lore-store-init) |
| `lore store list` | List the stores this repo sees, in precedence order | — |
| `lore store open` | Check out a store branch for browsing (detached, under ~/.lore/worktrees) | [team/identity-and-stores › Step 7 — Look inside a store, or drop your copy: lore store open and forget](../../docs/team/identity-and-stores.md#step-7--look-inside-a-store-or-drop-your-copy-lore-store-open-and-forget) |
| `lore store remove` | Detach a store from this repo (its rows stay in the store); asks first | — |
| `lore store set-url` | Record a store's new address (the store repo moved or was renamed); its id and rows stay | — |
| `lore store show` | List the configured stores, their clones and (with --links) the link index | [team/commands › lore store show](../../docs/team/commands.md#lore-store-show) |
| `lore store visibility` | Set whether a store's rows may reach client repos and committed files (internal or shareable) | — |

### lore suggestion

| Command | What it does | Guide |
|---|---|---|
| `lore suggestion` … | Manage suggestions | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore suggestion add` | Add a new suggestion | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore suggestion delete` | Delete a suggestion (it goes to the trash; lore trash restore brings it back) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
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
| `lore sync` … | Sync now: exchange every store, reconcile .lore/data, and print what changed | [sync/how-notes-travel-with-git › Try it — turn sync off for one command](../../docs/sync/how-notes-travel-with-git.md#try-it--turn-sync-off-for-one-command) |
| `lore sync ci-merge` | Merge a base branch into this branch when only lore data conflicts (for CI) | [sync/github-workflow › Step 1 — Try ci-merge on your machine](../../docs/sync/github-workflow.md#step-1--try-ci-merge-on-your-machine) |
| `lore sync conflicts` | List versions kept aside when both sides edited the same row | [sync/conflicts-and-duplicates › Step 3 — The file and your database both changed](../../docs/sync/conflicts-and-duplicates.md#step-3--the-file-and-your-database-both-changed) |
| `lore sync fix-projects` | Collapse several project rows into one (two parallel bootstraps met in a merge) | [sync/conflicts-and-duplicates › Step 5 — Two people set up lore on separate branches](../../docs/sync/conflicts-and-duplicates.md#step-5--two-people-set-up-lore-on-separate-branches) |
| `lore sync install-action` | Add a GitHub workflow that merges lore data in pull requests (opt-in) | [sync/github-workflow › Step 4 — Or let it run by itself, or remove it](../../docs/sync/github-workflow.md#step-4--or-let-it-run-by-itself-or-remove-it) |
| `lore sync merge-rows` | Fold a duplicate row into another (references move, the dropped row is trashed) | [sync/conflicts-and-duplicates › Step 4 — The same fact written twice](../../docs/sync/conflicts-and-duplicates.md#step-4--the-same-fact-written-twice) |
| `lore sync peek` | List the lore rows another branch carries, without checking it out | [sync/branches-peek-promote › Step 2 — Peek at a teammate's branch](../../docs/sync/branches-peek-promote.md#step-2--peek-at-a-teammates-branch) |
| `lore sync promote` | Commit rows onto another local branch (e.g. an urgent rule onto main) | [sync/branches-peek-promote › Step 3 — Promote one rule to main](../../docs/sync/branches-peek-promote.md#step-3--promote-one-rule-to-main) |
| `lore sync purge` | Durably remove rows archived before a cutoff | [sync/trash-and-purge › Step 3 — Purge archived notes for good](../../docs/sync/trash-and-purge.md#step-3--purge-archived-notes-for-good) |
| `lore sync resolve` | Close a conflict, keeping the current version or the saved other one | [sync/conflicts-and-duplicates › Step 3 — The file and your database both changed](../../docs/sync/conflicts-and-duplicates.md#step-3--the-file-and-your-database-both-changed) |
| `lore sync status` | Show sync state: baseline, pending exports, conflicts, broken files | [sync/how-notes-travel-with-git › Step 4 — Run a pass by hand when you want to see it](../../docs/sync/how-notes-travel-with-git.md#step-4--run-a-pass-by-hand-when-you-want-to-see-it) |
| `lore sync trash` … | Moved: lore trash --local (this spelling goes away in the next release) | [sync/trash-and-purge › Step 1 — Get back a note whose file disappeared](../../docs/sync/trash-and-purge.md#step-1--get-back-a-note-whose-file-disappeared) |
| `lore sync trash restore` | Moved: lore trash restore --local <n> | [sync/trash-and-purge › Step 1 — Get back a note whose file disappeared](../../docs/sync/trash-and-purge.md#step-1--get-back-a-note-whose-file-disappeared) |

### lore tag

| Command | What it does | Guide |
|---|---|---|
| `lore tag` … | Manage tags + entity-tag bindings | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |
| `lore tag add` | Create a new tag in the current project | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |
| `lore tag attach` | Attach a tag to an entity | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |
| `lore tag detach` | Detach a tag from an entity | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |
| `lore tag edit` | Change a tag's name, color or topic glob set | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |
| `lore tag list` | List tags in the current project | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |

### lore task

| Command | What it does | Guide |
|---|---|---|
| `lore task` … | Manage tasks (discrete work items) | — |
| `lore task accept` | Accept a proposed task (commitment accepted) | — |
| `lore task add` | Add a new task | [tasks/tasks › Step 1 — Add tasks](../../docs/tasks/tasks.md#step-1--add-tasks) |
| `lore task board` | Show tasks as a board grouped by status, assignee, priority, mission, repo or parent (read-only) | — |
| `lore task cancel` | Cancel a task | [tasks/tasks › Step 8 — Cancel or delete a task](../../docs/tasks/tasks.md#step-8--cancel-or-delete-a-task) *(brief)* |
| `lore task checklist` … | Checklist items inside a task (add, tick, untick, promote to a sub-task) | — |
| `lore task checklist add` | Add checklist items to a task; if you would ever claim it, make it a sub-task | — |
| `lore task checklist edit` | Change the text of a checklist item | — |
| `lore task checklist import` | Turn the - [ ] / - [x] lines of a task's body into checklist items | — |
| `lore task checklist list` | List a task's checklist (positions and chk_ ids) | — |
| `lore task checklist promote` | Turn a checklist item into a sub-task (same text, item removed, one batch) | — |
| `lore task checklist rm` | Remove a checklist item (kept as a tombstone so copies merge) | — |
| `lore task checklist tick` | Tick a checklist item (by position or chk_ id); the task status stays | — |
| `lore task checklist untick` | Untick a checklist item | — |
| `lore task claim` | Claim a task (or coordinate a mission) on the signals ref, with a lease | [team/commands › lore task claim](../../docs/team/commands.md#lore-task-claim) |
| `lore task decline` | Decline a proposed task (cancelled, with a close reason) | — |
| `lore task deferred` | Show deferred tasks (snoozed; deferred_until in the future) | [tasks/commitment-and-snooze › Step 4 — Snooze a task until a date](../../docs/tasks/commitment-and-snooze.md#step-4--snooze-a-task-until-a-date) |
| `lore task delete` | Delete a task and its sub-tasks (it goes to the trash; lore trash restore brings it back) | [tasks/tasks › Step 8 — Cancel or delete a task](../../docs/tasks/tasks.md#step-8--cancel-or-delete-a-task) |
| `lore task done` | Mark a task done (with the code that finished it) | [tasks/tasks › Step 6 — Finish a task](../../docs/tasks/tasks.md#step-6--finish-a-task) |
| `lore task edit` | Edit task fields (only flags you pass are applied) | [tasks/tasks › Step 5 — Change a task](../../docs/tasks/tasks.md#step-5--change-a-task) |
| `lore task export` | Export tasks as CSV, JSON (the import shape) or Markdown | — |
| `lore task heartbeat` | Renew your claim's lease (alive, not progress) | [team/commands › lore task heartbeat](../../docs/team/commands.md#lore-task-heartbeat) |
| `lore task history` | Show a task's activity history (field changes, claims; --git for older versions) | — |
| `lore task import` | Import tasks from a CSV or JSON file (one batch) | — |
| `lore task list` | List tasks | [tasks/tasks › Step 2 — See your list](../../docs/tasks/tasks.md#step-2--see-your-list) |
| `lore task merged` | Mark a done task as merged by hand (when lore cannot see the merge) | [tasks/link-commits › Step 5 — Mark a task merged by hand](../../docs/tasks/link-commits.md#step-5--mark-a-task-merged-by-hand) |
| `lore task next` | Claim the next ready task (resume-first, leaves only, dependencies, gates, urgency) | [team/commands › lore task next](../../docs/team/commands.md#lore-task-next) |
| `lore task note` | Record a progress note on a task (pushes your work branch) | [team/commands › lore task note](../../docs/team/commands.md#lore-task-note) |
| `lore task reassign` | Assign a task to someone else, dropping any claim (human override) | [team/agents › Step 3 — Give the task to someone else: lore task reassign](../../docs/team/agents.md#step-3--give-the-task-to-someone-else-lore-task-reassign) |
| `lore task recur` … | A repeating task's schedule (show, edit, skip, stop) | — |
| `lore task recur edit` | Change the recurrence rule (this and later instances) | — |
| `lore task recur show` | Show a recurring task's rule, series and next dates | — |
| `lore task recur skip` | Close this instance as skipped and create the next one | — |
| `lore task recur stop` | Stop the series: no further instances (this one stays) | — |
| `lore task relate` | Relate a task to another (duplicates, relates-to, follows-up, blocked-by, blocks) | — |
| `lore task release` | Give back a claim you hold | [team/agents › Step 1 — Give back a claim you hold: lore task release](../../docs/team/agents.md#step-1--give-back-a-claim-you-hold-lore-task-release) |
| `lore task revoke` | Drop the current claim on a task (human override; the holder is fenced out) | [team/agents › Step 2 — Take a claim away: lore task revoke](../../docs/team/agents.md#step-2--take-a-claim-away-lore-task-revoke) |
| `lore task search` | Full-text search across task title + body (BM25 ranked) | [tasks/tasks › Step 7 — Search your tasks](../../docs/tasks/tasks.md#step-7--search-your-tasks) |
| `lore task show` | Show task details | [team/commands › lore task show](../../docs/team/commands.md#lore-task-show) |
| `lore task someday` | Show someday/maybe tasks (parking lot, no commitment) | [tasks/commitment-and-snooze › Step 2 — Look at proposals and ideas](../../docs/tasks/commitment-and-snooze.md#step-2--look-at-proposals-and-ideas) *(brief)* |
| `lore task split` | Split a task into sub-tasks (one batch) | — |
| `lore task stale` | List open tasks with no change for N days; --close cancels them (close_reason stale) | — |
| `lore task start` | Mark task as in_progress | [tasks/tasks › Step 3 — Start a task](../../docs/tasks/tasks.md#step-3--start-a-task) *(brief)* |
| `lore task template` … | Task templates (add, edit, rename, show, delete) | — |
| `lore task template add` | Add a task template from a YAML file or an existing task tree | — |
| `lore task template delete` | Delete a task template (trash, like any row) | — |
| `lore task template edit` | Replace a task template's description, args and tree from a YAML file | — |
| `lore task template list` | List task templates (nearest store first) | — |
| `lore task template rename` | Rename a task template (the id stays) | — |
| `lore task template show` | Show a task template in its YAML form | — |
| `lore task time` … | Time spent on a task (show, log, edit, stop) | — |
| `lore task time edit` | Change a time entry (auto entries keep their original times) | — |
| `lore task time log` | Log time on a task by hand | — |
| `lore task time restore` | Undo the removal of a time entry | — |
| `lore task time rm` | Remove a time entry (kept as a tombstone; restore undoes it) | — |
| `lore task time show` | Show a task's tracked time (effort, elapsed, manual, per worker) | — |
| `lore task time stop` | Close your own open status span; the status stays | — |
| `lore task tree` … | Check a task's sub-task tree | — |
| `lore task tree check` | Check the sub-task tree: parent cycles, orphans, open children of closed parents, depth, shared ranks (--fix repairs cycles) | — |
| `lore task triage` | Show proposed tasks (suggested, not yet committed) | [tasks/commitment-and-snooze › Step 2 — Look at proposals and ideas](../../docs/tasks/commitment-and-snooze.md#step-2--look-at-proposals-and-ideas) *(brief)* |
| `lore task unrelate` | Remove a relation added with task relate | — |
| `lore task unwatch` | Stop watching a task | — |
| `lore task view` … | Saved task list views (add, edit, show, delete) | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task view add` | Save a task list filter as a named view | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task view delete` | Delete a saved task view | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task view edit` | Change a saved task view (only the flags you pass) | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task view list` | List saved task views | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task view show` | Show a saved task view | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task watch` | Watch a task (changes reach your inbox) | — |

### lore tasklist

| Command | What it does | Guide |
|---|---|---|
| `lore tasklist` … | Manage tasklists | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |
| `lore tasklist add` | Add a new tasklist | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |
| `lore tasklist delete` | Delete a tasklist and its tasks, with their sub-tasks (one batch, to the trash) | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |
| `lore tasklist edit` | Edit tasklist fields | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |
| `lore tasklist list` | List tasklists | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |
| `lore tasklist search` | Full-text search across tasklist title + body + status | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |
| `lore tasklist show` | Show tasklist details | [tasks/tasks › Step 10 — Group tasks in a tasklist](../../docs/tasks/tasks.md#step-10--group-tasks-in-a-tasklist) |

### lore tastepref

| Command | What it does | Guide |
|---|---|---|
| `lore tastepref` … | Manage taste preferences | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore tastepref add` | Add a new tastepref | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore tastepref delete` | Delete a tastepref (it goes to the trash; lore trash restore brings it back) | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore tastepref edit` | Edit tastepref fields | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore tastepref list` | List tasteprefs | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore tastepref search` | Full-text search across tastepref body + scope | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore tastepref show` | Show tastepref details | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |

### lore techdoc

| Command | What it does | Guide |
|---|---|---|
| `lore techdoc` … | Manage external documentation references | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore techdoc add` | Register external documentation (name + base URL) | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore techdoc delete` | Delete a techdoc (it goes to the trash; lore trash restore brings it back) | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore techdoc list` | List techdoc registrations | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore techdoc search` | Full-text search across techdoc name + description + base_url | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |
| `lore techdoc show` | Show techdoc details | [knowledge/other-row-kinds › Step 3 — Add a taste preference and a tech doc](../../docs/knowledge/other-row-kinds.md#step-3--add-a-taste-preference-and-a-tech-doc) *(brief)* |

### lore trash

| Command | What it does | Guide |
|---|---|---|
| `lore trash` | List the rows in the trash | — |

### lore tui

| Command | What it does | Guide |
|---|---|---|
| `lore tui` | Browse the knowledge DB in an interactive terminal UI | [knowledge/tui › Step 1 — Open it](../../docs/knowledge/tui.md#step-1--open-it) *(brief)* |

### lore upgrade

| Command | What it does | Guide |
|---|---|---|
| `lore upgrade` | Install the newest lore release (or this project's pinned version) | — |

### lore vars

| Command | What it does | Guide |
|---|---|---|
| `lore vars` … | Project variables, used as ${var:<name>} in any text field | — |
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
| `lore workflow` … | Manage workflows | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workflow add` | Add a new workflow | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workflow delete` | Delete a workflow (it goes to the trash; lore trash restore brings it back) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workflow edit` | Edit workflow fields | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workflow list` | List workflows | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workflow search` | Full-text search across workflow name + body | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workflow show` | Show workflow details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

### lore workspace

| Command | What it does | Guide |
|---|---|---|
| `lore workspace` … | Manage workspaces | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workspace add` | Add a new workspace | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workspace delete` | Delete a workspace (it goes to the trash; lore trash restore brings it back) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workspace edit` | Edit workspace fields | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workspace list` | List workspaces | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workspace search` | Full-text search across workspace name + body | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore workspace show` | Show workspace details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

<!-- /commands -->

## Related

- [Flags](flags.md): the flags many commands share.
- [Environment variables](environment.md), [config files](config.md) and [error codes](errors.md).
- [Feature index](features.md): every feature with its code folder, guide and example.
