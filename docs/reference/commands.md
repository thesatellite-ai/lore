# Command index

**In short:** this page lists every lore command with its one-line help and a link to the guide that explains it. It is made from the real program, so it always matches the version in this repo. For every flag of one command, run `lore <command> --help`.

How to read it:

- A command marked **…** only groups other commands (like a folder); you run the commands under it, for example `lore task add`, not `lore task`.
- **Guide** is the best page that explains the command. *(brief)* means that page only mentions it.
- Most commands also take the shared flags `--db`, `--project`, `--repo` and `--read-only`; see [Flags](flags.md).

The list below is written by `tests/docs/commands_index.py` from `lore --help` and the guide column of the [feature index](features.md). Do not edit it by hand: change the command's help text or the feature index, then run the script.

Three hidden commands are left out on purpose: `lore merge-driver`, `lore sync hook` and `lore task hook commit-msg`. Git runs them from the hooks and the merge setting that lore installs; you never run them by hand.

<!-- commands -->
598 commands, 500 of them leaf commands (the ones you run; a command marked … only groups others).

Jump to: [actor](#lore-actor) · [agent](#lore-agent) · [api](#lore-api) · [architecturenote](#lore-architecturenote) · [audit](#lore-audit) · [batch](#lore-batch) · [behaviour](#lore-behaviour) · [cache](#lore-cache) · [check](#lore-check) · [comment](#lore-comment) · [commit](#lore-commit) · [completion](#lore-completion) · [config](#lore-config) · [contact-type](#lore-contact-type) · [context](#lore-context) · [cookbookrecipe](#lore-cookbookrecipe) · [decision](#lore-decision) · [directive](#lore-directive) · [doctor](#lore-doctor) · [explain](#lore-explain) · [external-source](#lore-external-source) · [gate](#lore-gate) · [handoff](#lore-handoff) · [hold](#lore-hold) · [hotfix](#lore-hotfix) · [identity](#lore-identity) · [inbox](#lore-inbox) · [incident](#lore-incident) · [init](#lore-init) · [land](#lore-land) · [link](#lore-link) · [lint](#lore-lint) · [live](#lore-live) · [machine](#lore-machine) · [memory](#lore-memory) · [meta](#lore-meta) · [migrate](#lore-migrate) · [mission](#lore-mission) · [move](#lore-move) · [onboard](#lore-onboard) · [org](#lore-org) · [pattern](#lore-pattern) · [pii-pattern](#lore-pii-pattern) · [pin](#lore-pin) · [plan](#lore-plan) · [playbook](#lore-playbook) · [policy](#lore-policy) · [project](#lore-project) · [prompt](#lore-prompt) · [reminder](#lore-reminder) · [render](#lore-render) · [repo](#lore-repo) · [resource](#lore-resource) · [role](#lore-role) · [row](#lore-row) · [rule](#lore-rule) · [run](#lore-run) · [scope](#lore-scope) · [search](#lore-search) · [secrets](#lore-secrets) · [session](#lore-session) · [setup](#lore-setup) · [show](#lore-show) · [skill](#lore-skill) · [snapshot](#lore-snapshot) · [spec](#lore-spec) · [sql](#lore-sql) · [store](#lore-store) · [suggestion](#lore-suggestion) · [support-bundle](#lore-support-bundle) · [sync](#lore-sync) · [tag](#lore-tag) · [task](#lore-task) · [tasklist](#lore-tasklist) · [tastepref](#lore-tastepref) · [techdoc](#lore-techdoc) · [trash](#lore-trash) · [tui](#lore-tui) · [upgrade](#lore-upgrade) · [vars](#lore-vars) · [version](#lore-version) · [whoami](#lore-whoami) · [why-context](#lore-why-context) · [workflow](#lore-workflow) · [workspace](#lore-workspace)

### lore actor

| Command | What it does | Guide |
|---|---|---|
| `lore actor` … | The org roster: register, list, show and deactivate people and agents | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |
| `lore actor contact` … | A person's contacts: emails, GitHub/GitLab logins, phones, social handles (add, edit, remove, list) | [team/profiles › Step 2 — Add contacts](../../docs/team/profiles.md#step-2--add-contacts) |
| `lore actor contact add` | Add a contact (email, GitHub login, phone, …) to a person | [team/profiles › Step 2 — Add contacts](../../docs/team/profiles.md#step-2--add-contacts) |
| `lore actor contact edit` | Edit a contact: value, label, primary, identity, note | [team/profiles › Step 2 — Add contacts](../../docs/team/profiles.md#step-2--add-contacts) |
| `lore actor contact list` | List contacts: one person's, or everyone's (the org's phone list) | [team/profiles › Step 2 — Add contacts](../../docs/team/profiles.md#step-2--add-contacts) |
| `lore actor contact remove` | Remove a contact (to the trash, restorable) | [team/profiles › Step 2 — Add contacts](../../docs/team/profiles.md#step-2--add-contacts) |
| `lore actor deactivate` | Mark someone as gone (active=false, left_at); actors are never deleted | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |
| `lore actor delete` | Refused: actors are never deleted (use `lore actor deactivate`) | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |
| `lore actor edit` | Edit a person's profile: title, timezone, location, photo, bio, birthday (team profiles) | [team/profiles › Step 1 — Fill in a profile](../../docs/team/profiles.md#step-1--fill-in-a-profile), [Step 6 — Leave and come back](../../docs/team/profiles.md#step-6--leave-and-come-back) |
| `lore actor list` | List actors | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |
| `lore actor offboard` | A person leaves: end their stint and project memberships, list their open tasks, mark their agents inactive; asks first | [team/profiles › Step 7 — Offboard someone who leaves](../../docs/team/profiles.md#step-7--offboard-someone-who-leaves), [Step 8 — Rejoin](../../docs/team/profiles.md#step-8--rejoin) |
| `lore actor period` … | A person's company stints (list, add, edit); offboard and rejoin end and start them | [team/profiles › Step 4 — Record the company period](../../docs/team/profiles.md#step-4--record-the-company-period) |
| `lore actor period add` | Record a past or current stint by hand (overlaps are refused) | [team/profiles › Step 4 — Record the company period](../../docs/team/profiles.md#step-4--record-the-company-period) |
| `lore actor period edit` | Fix a stint's dates or fields (overlap rules still apply) | [team/profiles › Step 4 — Record the company period](../../docs/team/profiles.md#step-4--record-the-company-period) |
| `lore actor period list` | List a person's company stints (with the one read from active/left_at) | [team/profiles › Step 4 — Record the company period](../../docs/team/profiles.md#step-4--record-the-company-period) |
| `lore actor refresh` | Rewrite stale copies of an actor from its source (the roster row in a team, your identity in solo) | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |
| `lore actor register` | Put yourself (or, with --kind agent, an agent you own) on the org roster | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |
| `lore actor rejoin` | A person who left comes back: a new stint on the same actor (memberships are not restored); asks first | [team/profiles › Step 7 — Offboard someone who leaves](../../docs/team/profiles.md#step-7--offboard-someone-who-leaves), [Step 8 — Rejoin](../../docs/team/profiles.md#step-8--rejoin) |
| `lore actor show` | Show actor details | [team/commands › lore actor register](../../docs/team/commands.md#lore-actor-register) |
| `lore actor status` | Set a person's status: active, on_leave (--from, --until), left or archived (TEAM_PROFILES 5.3) | [team/profiles › Step 1 — Fill in a profile](../../docs/team/profiles.md#step-1--fill-in-a-profile), [Step 6 — Leave and come back](../../docs/team/profiles.md#step-6--leave-and-come-back) |

### lore agent

| Command | What it does | Guide |
|---|---|---|
| `lore agent` … | Agent sessions (one actor per session label) | — |
| `lore agent start` | Register an agent actor for this session and print its environment; --tool: record a tool session's details (session hook) | [team/commands › lore agent start](../../docs/team/commands.md#lore-agent-start) |

### lore api

| Command | What it does | Guide |
|---|---|---|
| `lore api` … | Serve the local REST API (127.0.0.1, bearer token in ~/.lore/api-token) | [reference/rest-api › Step 1 — Start the server](../../docs/reference/rest-api.md#step-1--start-the-server), [Step 4 — Rotate the token](../../docs/reference/rest-api.md#step-4--rotate-the-token) |
| `lore api token` … | Manage the local API token | [reference/rest-api › Step 1 — Start the server](../../docs/reference/rest-api.md#step-1--start-the-server), [Step 4 — Rotate the token](../../docs/reference/rest-api.md#step-4--rotate-the-token) |
| `lore api token rotate` | Replace the API token (every client holding the old one gets 401) | [reference/rest-api › Step 1 — Start the server](../../docs/reference/rest-api.md#step-1--start-the-server), [Step 4 — Rotate the token](../../docs/reference/rest-api.md#step-4--rotate-the-token) |

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
| `lore audit log` | Show recent audit entries, newest first | [operations/audit-log › Step 2 — Catch a change to the log](../../docs/operations/audit-log.md#step-2--catch-a-change-to-the-log) |
| `lore audit verify` | Check that the audit chain is unbroken (every entry links to the one before) | [operations/audit-log › Step 2 — Catch a change to the log](../../docs/operations/audit-log.md#step-2--catch-a-change-to-the-log) |

### lore batch

| Command | What it does | Guide |
|---|---|---|
| `lore batch` … | Bulk changes: list, show and undo a batch | [sync/trash-and-purge › Step 5 — Undo a bulk delete](../../docs/sync/trash-and-purge.md#step-5--undo-a-bulk-delete) |
| `lore batch list` | List bulk actions (batches) run on this machine | [sync/trash-and-purge › Step 5 — Undo a bulk delete](../../docs/sync/trash-and-purge.md#step-5--undo-a-bulk-delete) |
| `lore batch show` | Show one batch: its kind, who ran it and the rows it wrote | [sync/trash-and-purge › Step 5 — Undo a bulk delete](../../docs/sync/trash-and-purge.md#step-5--undo-a-bulk-delete) |
| `lore batch undo` | Undo a bulk action: restore a delete batch, put the rows an edit batch changed back, or move the rows a create batch added to the trash | [sync/trash-and-purge › Step 5 — Undo a bulk delete](../../docs/sync/trash-and-purge.md#step-5--undo-a-bulk-delete) |

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
| `lore check leaks` | Check staged files and the commit message (hooks), or the tree structure (--ci), for internal or personal content | [team/commands › lore check leaks](../../docs/team/commands.md#lore-check-leaks) |
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
| `lore commit show` | Show every entity linked to a given git commit | [tasks/link-commits › Step 3 — Find the rows behind a commit](../../docs/tasks/link-commits.md#step-3--find-the-rows-behind-a-commit) |

### lore completion

| Command | What it does | Guide |
|---|---|---|
| `lore completion` | Generate the autocompletion script for the specified shell | [knowledge/first-project › Try it: check that everything is healthy](../../docs/knowledge/first-project.md#try-it-check-that-everything-is-healthy) |

### lore config

| Command | What it does | Guide |
|---|---|---|
| `lore config` … | Get/set keys of ~/.lore/config.yaml | [operations/plugins-and-db-config › Step 1 — Read and write your settings](../../docs/operations/plugins-and-db-config.md#step-1--read-and-write-your-settings) |
| `lore config check` | Validate ~/.lore/config.yaml (orgs, stores, live settings) and list every problem | [reference/config › What lore config check refuses](../../docs/reference/config.md#what-lore-config-check-refuses) |
| `lore config fmt` | Rewrite ~/.lore/config.yaml in block style when a list or mapping sits on one line (keeps comments and order) | [operations/plugins-and-db-config › Step 1 — Read and write your settings](../../docs/operations/plugins-and-db-config.md#step-1--read-and-write-your-settings) |
| `lore config get` | Get a key of ~/.lore/config.yaml (a dotted path, e.g. live.autopush) | [operations/plugins-and-db-config › Step 1 — Read and write your settings](../../docs/operations/plugins-and-db-config.md#step-1--read-and-write-your-settings) |
| `lore config list` | List every key set in ~/.lore/config.yaml | [operations/plugins-and-db-config › Step 1 — Read and write your settings](../../docs/operations/plugins-and-db-config.md#step-1--read-and-write-your-settings) |
| `lore config set` | Set a key of ~/.lore/config.yaml (keeps comments and order) | [operations/plugins-and-db-config › Step 1 — Read and write your settings](../../docs/operations/plugins-and-db-config.md#step-1--read-and-write-your-settings) |
| `lore config unset` | Remove a key of ~/.lore/config.yaml (keeps comments and order); --dry-run shows what would go | [operations/plugins-and-db-config › Step 1 — Read and write your settings](../../docs/operations/plugins-and-db-config.md#step-1--read-and-write-your-settings) |

### lore contact-type

| Command | What it does | Guide |
|---|---|---|
| `lore contact-type` … | Contact types: the built-ins plus the org's own (list, add, edit, retire) | [team/profiles › Step 3 — Extend the contact types](../../docs/team/profiles.md#step-3--extend-the-contact-types) |
| `lore contact-type add` | Add a contact type to the org's list | [team/profiles › Step 3 — Extend the contact types](../../docs/team/profiles.md#step-3--extend-the-contact-types) |
| `lore contact-type edit` | Change a contact type's label or link, or retire it | [team/profiles › Step 3 — Extend the contact types](../../docs/team/profiles.md#step-3--extend-the-contact-types) |
| `lore contact-type list` | List the contact types: built-ins and the org's own | [team/profiles › Step 3 — Extend the contact types](../../docs/team/profiles.md#step-3--extend-the-contact-types) |

### lore context

| Command | What it does | Guide |
|---|---|---|
| `lore context` … | Print the rules, hotfixes and notes for a file, task, topic or prompt (the agent's fetch command) | [operations/ci › Step 5 — Give tools the notes they cannot import](../../docs/operations/ci.md#step-5--give-tools-the-notes-they-cannot-import) |
| `lore context fetch` | Print the rules, hotfixes and notes for a file, task, topic or prompt (the agent's fetch command) | [operations/ci › Step 5 — Give tools the notes they cannot import](../../docs/operations/ci.md#step-5--give-tools-the-notes-they-cannot-import) |

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
| `lore directive` … | Install or show the lore agent-directive block in agent-loaded markdown | [knowledge/render-and-agent-files › Step 6 — Add only the directive](../../docs/knowledge/render-and-agent-files.md#step-6--add-only-the-directive) |
| `lore directive install` | Install (or refresh) the directive block at the top of one or more files | [knowledge/render-and-agent-files › Step 6 — Add only the directive](../../docs/knowledge/render-and-agent-files.md#step-6--add-only-the-directive) |
| `lore directive show` | Print the directive block (compact; --full for the long form) without writing any file | [knowledge/render-and-agent-files › Step 6 — Add only the directive](../../docs/knowledge/render-and-agent-files.md#step-6--add-only-the-directive) |

### lore doctor

| Command | What it does | Guide |
|---|---|---|
| `lore doctor` … | Run health checks; exit 0 healthy, 1 degraded, 2 broken | [operations/doctor › Check lore's health with lore doctor](../../docs/operations/doctor.md#check-lores-health-with-lore-doctor) |
| `lore doctor fix` | Run the fix of one doctor finding of this repo (a plain lore command such as lore render), after a confirmation | [operations/doctor › Check lore's health with lore doctor](../../docs/operations/doctor.md#check-lores-health-with-lore-doctor) |
| `lore doctor last` | Show each setup's last doctor results and when they were taken, without running any check | [operations/doctor › Check lore's health with lore doctor](../../docs/operations/doctor.md#check-lores-health-with-lore-doctor) |
| `lore doctor run` | Run health checks; exit 0 healthy, 1 degraded, 2 broken | [operations/doctor › Check lore's health with lore doctor](../../docs/operations/doctor.md#check-lores-health-with-lore-doctor) |

### lore explain

| Command | What it does | Guide |
|---|---|---|
| `lore explain` … | Show every copy of a row (live, this branch, the default branch), which one wins and why, and its land decision | [team/commands › lore explain](../../docs/team/commands.md#lore-explain) |
| `lore explain run` | Show every copy of a row (live, this branch, the default branch), which one wins and why, and its land decision | [team/commands › lore explain](../../docs/team/commands.md#lore-explain) |

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
| `lore identity migrate` | Move the old top-level identity: into the active setup (--keep setup\|top when the two differ) | [team/identity-and-stores › Step 2 — Set or remove your own identity: lore identity set and unset](../../docs/team/identity-and-stores.md#step-2--set-or-remove-your-own-identity-lore-identity-set-and-unset) |
| `lore identity set` | Persist an identity: the active setup's in ~/.lore/config.yaml (top-level identity: with no setup), or one org's (--org) | [team/identity-and-stores › Step 2 — Set or remove your own identity: lore identity set and unset](../../docs/team/identity-and-stores.md#step-2--set-or-remove-your-own-identity-lore-identity-set-and-unset) |
| `lore identity show` | Show the resolved actor identity + which step matched | [team/identity-and-stores › Step 2 — Set or remove your own identity: lore identity set and unset](../../docs/team/identity-and-stores.md#step-2--set-or-remove-your-own-identity-lore-identity-set-and-unset) |
| `lore identity unset` | Clear the active setup's identity and the top-level identity: in ~/.lore/config.yaml (or one org's with --org) | [team/identity-and-stores › Step 2 — Set or remove your own identity: lore identity set and unset](../../docs/team/identity-and-stores.md#step-2--set-or-remove-your-own-identity-lore-identity-set-and-unset) |

### lore inbox

| Command | What it does | Guide |
|---|---|---|
| `lore inbox` … | What needs you: assignments, handoffs, gates, expired claims, blocked tasks, findings | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |
| `lore inbox show` | What needs you: assignments, handoffs, gates, expired claims, blocked tasks, findings | [tasks/missions-and-plans › Step 7 — Leave a handoff and read your inbox](../../docs/tasks/missions-and-plans.md#step-7--leave-a-handoff-and-read-your-inbox) |

### lore incident

| Command | What it does | Guide |
|---|---|---|
| `lore incident` … | Manage incidents | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore incident add` | Add a new incident | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore incident delete` | Delete a incident (it goes to the trash; lore trash restore brings it back) | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore incident edit` | Edit incident fields or add --ref pointers (only the flags given change) | [knowledge/other-row-kinds › Step 4 — Read, change and search one kind](../../docs/knowledge/other-row-kinds.md#step-4--read-change-and-search-one-kind) |
| `lore incident list` | List incidents | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore incident search` | Full-text search across incident title + body | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |
| `lore incident show` | Show incident details | [knowledge/other-row-kinds › Step 2 — Add the other kinds](../../docs/knowledge/other-row-kinds.md#step-2--add-the-other-kinds) *(brief)* |

### lore init

| Command | What it does | Guide |
|---|---|---|
| `lore init` | Initialize a lore project in this repository (project row and repo id) | [knowledge/first-project › Step 1 — Set up lore in the repo](../../docs/knowledge/first-project.md#step-1--set-up-lore-in-the-repo) |

### lore land

| Command | What it does | Guide |
|---|---|---|
| `lore land` … | Write live rows to main now (finished rows land; open rows are copied and stay live) | [team/commands › lore land](../../docs/team/commands.md#lore-land) |
| `lore land run` | Write live rows to main now (finished rows land; open rows are copied and stay live) | [team/commands › lore land](../../docs/team/commands.md#lore-land) |

### lore link

| Command | What it does | Guide |
|---|---|---|
| `lore link` … | Link git commits to any entity (task / run / mission / decision / …) | [tasks/link-commits › Step 6 — Remove a link](../../docs/tasks/link-commits.md#step-6--remove-a-link) |
| `lore link add` | Link a git commit to an entity (--entity=tsk_… --commit=<sha>) | [tasks/link-commits › Step 6 — Remove a link](../../docs/tasks/link-commits.md#step-6--remove-a-link) |
| `lore link list` | List commit links (filter by --entity or --commit) | [tasks/link-commits › Step 6 — Remove a link](../../docs/tasks/link-commits.md#step-6--remove-a-link) |
| `lore link remove` | Remove a commit link by its ID (to the trash, restorable) | [tasks/link-commits › Step 6 — Remove a link](../../docs/tasks/link-commits.md#step-6--remove-a-link) |

### lore lint

| Command | What it does | Guide |
|---|---|---|
| `lore lint` … | Report problems in the attached stores (dangling references, shadowed variables, task cycles, long time spans, duplicate imports, unknown metadata keys, stored secrets) | [sync/conflicts-and-duplicates › Try it — check the notes after a merge with lore lint](../../docs/sync/conflicts-and-duplicates.md#try-it--check-the-notes-after-a-merge-with-lore-lint) |
| `lore lint run` | Report problems in the attached stores (dangling references, shadowed variables, task cycles, long time spans, duplicate imports, unknown metadata keys, stored secrets) | [sync/conflicts-and-duplicates › Try it — check the notes after a merge with lore lint](../../docs/sync/conflicts-and-duplicates.md#try-it--check-the-notes-after-a-merge-with-lore-lint) |

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
| `lore machine` … | List the machine profiles of the attached stores | [team/profiles › Step 10 — Label your machines](../../docs/team/profiles.md#step-10--label-your-machines) |
| `lore machine label` | Set (or clear with "") this machine's synced label and refresh its profile | [team/profiles › Step 10 — Label your machines](../../docs/team/profiles.md#step-10--label-your-machines) |
| `lore machine list` | List the machine profiles of the attached stores | [team/profiles › Step 10 — Label your machines](../../docs/team/profiles.md#step-10--label-your-machines) |
| `lore machine path` … | Where lore keeps its own files on this machine: lore home, config.yaml, this repo's .lore | [team/profiles › Step 10 — Label your machines](../../docs/team/profiles.md#step-10--label-your-machines) |
| `lore machine path list` | List the paths of lore's own files on this machine (lore home, config.yaml, this repo's .lore) | [team/profiles › Step 10 — Label your machines](../../docs/team/profiles.md#step-10--label-your-machines) |
| `lore machine reveal` | Show one of lore's files in the file manager (Finder): lore_home, config_file or repo_lore; never another path | [team/profiles › Step 10 — Label your machines](../../docs/team/profiles.md#step-10--label-your-machines) |
| `lore machine show` | Show one machine profile in full | [team/profiles › Step 10 — Label your machines](../../docs/team/profiles.md#step-10--label-your-machines) |

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
| `lore memory move` | Move a memory to another attached store (same id; the old copy is tombstoned) | [knowledge/rules-memories-decisions › Step 9 — Move a memory to another store](../../docs/knowledge/rules-memories-decisions.md#step-9--move-a-memory-to-another-store) |
| `lore memory search` | Full-text search over memories of every attached store | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory show` | Show one memory | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |
| `lore memory unarchive` | Bring an archived memory back | [knowledge/rules-memories-decisions › Step 2 — Add memories](../../docs/knowledge/rules-memories-decisions.md#step-2--add-memories) |

### lore meta

| Command | What it does | Guide |
|---|---|---|
| `lore meta` … | Metadata on any row: define keys, set and unset values, rename, move, export | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta add` | Add a metadata key to a place's registry | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta adopt` | Move another place's metadata key rows into this place (ids kept) | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta delete` | Delete a metadata key and its allowed values | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta describe` | Set a metadata key's description | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta edit` | Edit a metadata key (strict, internal, allowed values, type) | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta export` | Export metadata values as CSV or JSON lines (id,key,value) | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta forget` | Remove unused metadata keys for good (tombstones kept) | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta get` | Show a row's metadata (sensitive values masked unless --show-sensitive) | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta history` | Dated metadata value changes of one row, from git history (sensitive values masked) | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta import` | Import metadata values from CSV or JSON lines, as one batch | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta keys` | List the metadata keys every visible place defines | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta lint` | List metadata that does not fit its key, and registry problems | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta move` | Move a metadata key row outward to another place (id kept, values unchanged) | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta rebuild` | Recreate lost metadata key rows of one place from git history or data rows | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta rehome` | Re-file the metadata keys a place's rows use that it can no longer see (copy rule: reuse a visible key of that name and type, else a new key row) | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta rename` | Rename a metadata key (a label change; the id stays) | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta set` | Set metadata on a row by id | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta show` | Show a row's metadata (sensitive values masked unless --show-sensitive) | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta unset` | Remove a metadata key from a row (a parent removes its fields) | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta value` … | Allowed values of a metadata key (add, rename) | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta value add` | Add an allowed value to a metadata key | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |
| `lore meta value rename` | Rename an allowed value (a label change; the val_ id stays) | [knowledge/variables-and-metadata › Step 6 — Put metadata on a row](../../docs/knowledge/variables-and-metadata.md#step-6--put-metadata-on-a-row), [Step 9 — Rename a value or a key](../../docs/knowledge/variables-and-metadata.md#step-9--rename-a-value-or-a-key) |

### lore migrate

| Command | What it does | Guide |
|---|---|---|
| `lore migrate` … | Bring a project's data to the current format (status, run, rollback, enable, unlock) | [sync/shared-database-mode-b › Step 4 — Convert a repo that used a shared database](../../docs/sync/shared-database-mode-b.md#step-4--convert-a-repo-that-used-a-shared-database), [Step 5 — Check the migration, undo it or declare a format feature](../../docs/sync/shared-database-mode-b.md#step-5--check-the-migration-undo-it-or-declare-a-format-feature) |
| `lore migrate enable` | Declare a format feature before its first use (raises min_lore_version) | [sync/shared-database-mode-b › Step 4 — Convert a repo that used a shared database](../../docs/sync/shared-database-mode-b.md#step-4--convert-a-repo-that-used-a-shared-database), [Step 5 — Check the migration, undo it or declare a format feature](../../docs/sync/shared-database-mode-b.md#step-5--check-the-migration-undo-it-or-declare-a-format-feature) |
| `lore migrate rollback` | Undo the last migration (local undo before push, new commits after) | [sync/shared-database-mode-b › Step 4 — Convert a repo that used a shared database](../../docs/sync/shared-database-mode-b.md#step-4--convert-a-repo-that-used-a-shared-database), [Step 5 — Check the migration, undo it or declare a format feature](../../docs/sync/shared-database-mode-b.md#step-5--check-the-migration-undo-it-or-declare-a-format-feature) |
| `lore migrate run` | Rewrite v0.1.17 row files (_v: 1) as _v: 2 (the migrated nouns) | [sync/shared-database-mode-b › Step 4 — Convert a repo that used a shared database](../../docs/sync/shared-database-mode-b.md#step-4--convert-a-repo-that-used-a-shared-database), [Step 5 — Check the migration, undo it or declare a format feature](../../docs/sync/shared-database-mode-b.md#step-5--check-the-migration-undo-it-or-declare-a-format-feature) |
| `lore migrate status` | Show the row format of every place, the last migration and the lock | [sync/shared-database-mode-b › Step 4 — Convert a repo that used a shared database](../../docs/sync/shared-database-mode-b.md#step-4--convert-a-repo-that-used-a-shared-database), [Step 5 — Check the migration, undo it or declare a format feature](../../docs/sync/shared-database-mode-b.md#step-5--check-the-migration-undo-it-or-declare-a-format-feature) |
| `lore migrate unlock` | Release this machine's stale migration lock (its process is gone), or with --team a stale team lock | [sync/shared-database-mode-b › Step 4 — Convert a repo that used a shared database](../../docs/sync/shared-database-mode-b.md#step-4--convert-a-repo-that-used-a-shared-database), [Step 5 — Check the migration, undo it or declare a format feature](../../docs/sync/shared-database-mode-b.md#step-5--check-the-migration-undo-it-or-declare-a-format-feature) |

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
| `lore move` | Move a row to another store (same id; asks first) | [team/identity-and-stores › Step 6 — Move a note to where it belongs: lore move](../../docs/team/identity-and-stores.md#step-6--move-a-note-to-where-it-belongs-lore-move) |

### lore onboard

| Command | What it does | Guide |
|---|---|---|
| `lore onboard` | Alias of lore setup: open the guided setup page | [team/guided-setup › Step 2 — Open the guided setup](../../docs/team/guided-setup.md#step-2--open-the-guided-setup) |

### lore org

| Command | What it does | Guide |
|---|---|---|
| `lore org` … | Choose and inspect which org a repo belongs to | — |
| `lore org create` | Start a new org on a lore store: suggest it to teammates, add it with your identity, join its roster | [team/profiles › Step 9 — Create or rename an org](../../docs/team/profiles.md#step-9--create-or-rename-an-org) |
| `lore org import` | Adopt the org entry a team suggests in its store, with your identity, and join its roster | [team/identity-and-stores › Step 4 — Join your company's setup in one command: lore org import](../../docs/team/identity-and-stores.md#step-4--join-your-companys-setup-in-one-command-lore-org-import) |
| `lore org link` | Link this repo to an internal project in the org's store | [team/commands › lore org link](../../docs/team/commands.md#lore-org-link) |
| `lore org refresh` | Copy an org entry's stores, own and repos from the store's lore-org.yaml (the team's source) and record its version | — |
| `lore org rename` | Rename an org entry on this machine (a label change; the org's id and every row stay) | [team/profiles › Step 9 — Create or rename an org](../../docs/team/profiles.md#step-9--create-or-rename-an-org) |
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
| `lore pin` … | Pin how a must rule or critical/high hotfix appears in LORE.md | [knowledge/render-and-agent-files › Pin how a rule appears](../../docs/knowledge/render-and-agent-files.md#pin-how-a-rule-appears) |
| `lore pin set` | Pin how a must rule or critical/high hotfix appears in LORE.md: --context full or line | [knowledge/render-and-agent-files › Pin how a rule appears](../../docs/knowledge/render-and-agent-files.md#pin-how-a-rule-appears) |
| `lore pin unset` | Remove a pin; render ranks the entry again | [knowledge/render-and-agent-files › Pin how a rule appears](../../docs/knowledge/render-and-agent-files.md#pin-how-a-rule-appears) |

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
| `lore policy` … | Team rules: set, show and unset policies (lower levels may only tighten) | [operations/plugins-and-db-config › Step 2 — Set a rule for the whole team](../../docs/operations/plugins-and-db-config.md#step-2--set-a-rule-for-the-whole-team) |
| `lore policy set` | Set a policy rule at a level (may only tighten a higher level) | [operations/plugins-and-db-config › Step 2 — Set a rule for the whole team](../../docs/operations/plugins-and-db-config.md#step-2--set-a-rule-for-the-whole-team) |
| `lore policy show` | Show the effective value of every policy rule and where it comes from | [operations/plugins-and-db-config › Step 2 — Set a rule for the whole team](../../docs/operations/plugins-and-db-config.md#step-2--set-a-rule-for-the-whole-team) |
| `lore policy unset` | Remove a policy rule's row at a level (the next level up applies again) | [operations/plugins-and-db-config › Step 2 — Set a rule for the whole team](../../docs/operations/plugins-and-db-config.md#step-2--set-a-rule-for-the-whole-team) |

### lore project

| Command | What it does | Guide |
|---|---|---|
| `lore project` … | Manage projects | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project archive` | Archive a project (soft-delete via archived_at) | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project delete` | Delete a project (to the trash; archive is usually what you want) | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project list` | List projects in the current DB | [sync/multi-repo-projects › Step 1 — See the project](../../docs/sync/multi-repo-projects.md#step-1--see-the-project) |
| `lore project member` … | Who works on a project, with which roles, since when (add, edit, remove, list) | [team/profiles › Step 5 — Add people to projects, with roles](../../docs/team/profiles.md#step-5--add-people-to-projects-with-roles), [Memberships](../../docs/team/profiles.md#memberships) |
| `lore project member add` | Add a person to a project with one or more roles (re-adding starts a new stint) | [team/profiles › Step 5 — Add people to projects, with roles](../../docs/team/profiles.md#step-5--add-people-to-projects-with-roles), [Memberships](../../docs/team/profiles.md#memberships) |
| `lore project member edit` | Set the roles of a current project member | [team/profiles › Step 5 — Add people to projects, with roles](../../docs/team/profiles.md#step-5--add-people-to-projects-with-roles), [Memberships](../../docs/team/profiles.md#memberships) |
| `lore project member list` | List project members (--history adds ended stints) | [team/profiles › Step 5 — Add people to projects, with roles](../../docs/team/profiles.md#step-5--add-people-to-projects-with-roles), [Memberships](../../docs/team/profiles.md#memberships) |
| `lore project member remove` | End a project membership on a date (the row and its history are kept) | [team/profiles › Step 5 — Add people to projects, with roles](../../docs/team/profiles.md#step-5--add-people-to-projects-with-roles), [Memberships](../../docs/team/profiles.md#memberships) |
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
| `lore render` … | Render .lore/LORE.md from scoped knowledge and point your AI tools at it | [knowledge/render-and-agent-files › Step 5 — Point another tool](../../docs/knowledge/render-and-agent-files.md#step-5--point-another-tool) |
| `lore render run` | Render .lore/LORE.md from scoped knowledge and point your AI tools at it | [knowledge/render-and-agent-files › Step 5 — Point another tool](../../docs/knowledge/render-and-agent-files.md#step-5--point-another-tool) |

### lore repo

| Command | What it does | Guide |
|---|---|---|
| `lore repo` … | Manage repos within a project | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo add` | Register a repo within the current project | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo archive` | Archive a repo (soft-delete via archived_at) | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo delete` | Delete a repo (to the trash; archive is usually what you want) | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo fork` | This clone is a fork (or --split): give it its own permanent repo id | [sync/multi-repo-projects › Step 5 — The repo moved](../../docs/sync/multi-repo-projects.md#step-5--the-repo-moved-record-its-new-address), [Step 6 — A fork gets its own repo id](../../docs/sync/multi-repo-projects.md#step-6--a-fork-gets-its-own-repo-id) |
| `lore repo init` | Initialize a lore project in this repository (project row and repo id); the same as lore init | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo list` | List repos in the current project | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |
| `lore repo merge-ids` | Rewrite _o.repo of rows stamped with an alias repo id to the winning id, then clear the aliases (PROVENANCE 11.9) | [sync/multi-repo-projects › Step 5 — The repo moved](../../docs/sync/multi-repo-projects.md#step-5--the-repo-moved-record-its-new-address), [Step 6 — A fork gets its own repo id](../../docs/sync/multi-repo-projects.md#step-6--a-fork-gets-its-own-repo-id) |
| `lore repo set-url` | Confirm this repo's address: the primary (moved or renamed), a --mirror, or --remove one | [sync/multi-repo-projects › Step 5 — The repo moved](../../docs/sync/multi-repo-projects.md#step-5--the-repo-moved-record-its-new-address), [Step 6 — A fork gets its own repo id](../../docs/sync/multi-repo-projects.md#step-6--a-fork-gets-its-own-repo-id) |
| `lore repo unarchive` | Unarchive a repo (clear archived_at) | [sync/multi-repo-projects › Step 4 — Archive, unarchive and delete](../../docs/sync/multi-repo-projects.md#step-4--archive-unarchive-and-delete) |

### lore resource

| Command | What it does | Guide |
|---|---|---|
| `lore resource` … | Infrastructure facts: servers, domains, accounts and the refs between them | [operations/resources](../../docs/operations/resources.md) |
| `lore resource add` | Add a resource of a kind | [operations/resources](../../docs/operations/resources.md) |
| `lore resource adopt` | Move another place's resource kind and field rows into this place (ids kept) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource archive` | Archive a resource (refs stay; new refs are refused) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource check` | Stamp last_checked: a person or agent looked and the facts are right | [operations/resources](../../docs/operations/resources.md) |
| `lore resource clone` | Add a resource with another resource's values (--set overrides) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource delete` | Delete a resource, or every resource of a kind matching --where (to the trash) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource edit` | Edit a resource's values (stamps last_checked) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource export` | Export resources of a kind in the import format (sensitive values masked) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource field` … | Fields of a resource kind (add, edit, rename, retype, remove) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource field add` | Add a field to a resource kind (a kind_fields settings row) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource field edit` | Edit a field of a resource kind | [operations/resources](../../docs/operations/resources.md) |
| `lore resource field remove` | Remove a field from a resource kind | [operations/resources](../../docs/operations/resources.md) |
| `lore resource field rename` | Rename a field (a label change: rows key values by fld_ id) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource field retype` | Change a field's type: every value is checked first, then converted (the field id stays) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource graph` | Print the ref tree of a resource (--down: what it uses; --up: what uses it) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource history` | Field changes of one resource, newest first, with actor, date and batch (sensitive values masked) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource impact` | What breaks if a resource goes down: every resource that depends on it, with open linked tasks and incidents | [operations/resources](../../docs/operations/resources.md) |
| `lore resource import` | Import resources from a csv, json or yaml file (one batch, all or nothing) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource kind` … | Resource kinds (add, edit, rename, show) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource kind add` | Add a resource kind (a kinds settings row) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource kind edit` | Edit a resource kind | [operations/resources](../../docs/operations/resources.md) |
| `lore resource kind rename` | Rename a resource kind (a label change; ids stay) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource kind show` | Show a resource kind and its fields | [operations/resources](../../docs/operations/resources.md) |
| `lore resource kinds` | List resource kinds with their fields and row counts | [operations/resources](../../docs/operations/resources.md) |
| `lore resource lint` | Find dangling and bare-name refs, missing required fields, deprecated fields, due reminders and duplicates | [operations/resources](../../docs/operations/resources.md) |
| `lore resource list` | List resources of a kind | [operations/resources](../../docs/operations/resources.md) |
| `lore resource merge` | Fold a duplicate resource into another of the same kind: refs move, values fill, the duplicate is archived | [operations/resources](../../docs/operations/resources.md) |
| `lore resource rehome` | List the kinds and refs a place can no longer see after its chain changed | [operations/resources](../../docs/operations/resources.md) |
| `lore resource rename` | Rename a resource (ids and refs stay) | [operations/resources](../../docs/operations/resources.md) |
| `lore resource repoint` | Move every ref from one resource to another of the same kind, then archive the old one | [operations/resources](../../docs/operations/resources.md) |
| `lore resource report` | Report on the resources in view: counts per kind, stale, unchecked, dangling, quarantined, open links, secret references to rotate | [operations/resources](../../docs/operations/resources.md) |
| `lore resource show` | Show one resource | [operations/resources](../../docs/operations/resources.md) |
| `lore resource stale` | Resources whose last_checked is older than --days, or never set | [operations/resources](../../docs/operations/resources.md) |

### lore role

| Command | What it does | Guide |
|---|---|---|
| `lore role` … | Project roles: the built-ins plus the org's own (list, add, edit, retire) | [team/profiles › Roles](../../docs/team/profiles.md#roles) |
| `lore role add` | Add a project role | [team/profiles › Roles](../../docs/team/profiles.md#roles) |
| `lore role edit` | Change a role's label or description, or retire it | [team/profiles › Roles](../../docs/team/profiles.md#roles) |
| `lore role list` | List the project roles (built-ins and added ones) | [team/profiles › Roles](../../docs/team/profiles.md#roles) |

### lore row

| Command | What it does | Guide |
|---|---|---|
| `lore row` … | Any row by id: show it, or move it to another store | — |
| `lore row move` | Move any row to another store, keeping its id (the old copy is tombstoned) | [sync/stores › Step 5 — Move a note out of the repo](../../docs/sync/stores.md#step-5--move-a-note-out-of-the-repo) |
| `lore row show` | Show any row by id, with --origin for where it came from | [knowledge/tui › Show one row without the TUI](../../docs/knowledge/tui.md#show-one-row-without-the-tui) |

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
| `lore run` … | Log agent runs (start, end, cancel) | [tasks/run-log › Step 1 — Start a run](../../docs/tasks/run-log.md#step-1--start-a-run) |
| `lore run cancel` | Cancel a run (end it with outcome cancelled) | [tasks/run-log › Step 1 — Start a run](../../docs/tasks/run-log.md#step-1--start-a-run) |
| `lore run end` | Close a run with an outcome (success \| partial \| failed \| cancelled) | [tasks/run-log › Step 1 — Start a run](../../docs/tasks/run-log.md#step-1--start-a-run) |
| `lore run start` | Open a run (records what agent is attempting which task with which model) | [tasks/run-log › Step 1 — Start a run](../../docs/tasks/run-log.md#step-1--start-a-run) |

### lore scope

| Command | What it does | Guide |
|---|---|---|
| `lore scope` … | Directory scopes for rules and hotfixes (ids are kept when a folder is renamed) | [knowledge/render-and-agent-files › Rules for one folder](../../docs/knowledge/render-and-agent-files.md#rules-for-one-folder) |
| `lore scope list` | List every folder scope of this repo with its entries and file | [knowledge/render-and-agent-files › Rules for one folder](../../docs/knowledge/render-and-agent-files.md#rules-for-one-folder) |
| `lore scope move` | Relabel a folder scope after a folder rename (ids unchanged, no row rewritten) | [knowledge/render-and-agent-files › Rules for one folder](../../docs/knowledge/render-and-agent-files.md#rules-for-one-folder) |
| `lore scope show` | Show the scope chain for a path: which rules apply and which file serves each level | [knowledge/render-and-agent-files › Rules for one folder](../../docs/knowledge/render-and-agent-files.md#rules-for-one-folder) |

### lore search

| Command | What it does | Guide |
|---|---|---|
| `lore search` … | Unified full-text search across all entities (fans out per entity + RRF-merges) | [knowledge/search › Try it: whole words, prefixes and several words](../../docs/knowledge/search.md#try-it-whole-words-prefixes-and-several-words) |
| `lore search rebuild` | Rebuild the search index: every attached store's cache, from its row files | [knowledge/search › Keep the index healthy](../../docs/knowledge/search.md#keep-the-index-healthy) |
| `lore search run` | Unified full-text search across all entities (fans out per entity + RRF-merges) | [knowledge/search › Try it: whole words, prefixes and several words](../../docs/knowledge/search.md#try-it-whole-words-prefixes-and-several-words) |
| `lore search status` | Per-entity full-text index row counts and drift of the core cache | [knowledge/search › Keep the index healthy](../../docs/knowledge/search.md#keep-the-index-healthy) |

### lore secrets

| Command | What it does | Guide |
|---|---|---|
| `lore secrets` … | Secret references and who holds them (list, check, move, offboarding) | [operations/secrets-and-pii › Step 3 — Keep the secret in your password manager](../../docs/operations/secrets-and-pii.md#step-3--keep-the-secret-in-your-password-manager), [Step 6 — A renamed entry or a person who leaves](../../docs/operations/secrets-and-pii.md#step-6--a-renamed-entry-or-a-person-who-leaves) |
| `lore secrets allow` … | Record one finding as not a secret for one row, field and rule | [operations/secrets-and-pii › Step 7 — Review what was marked not a secret](../../docs/operations/secrets-and-pii.md#step-7--review-what-was-marked-not-a-secret) |
| `lore secrets allow list` | List the allow entries | [operations/secrets-and-pii › Step 7 — Review what was marked not a secret](../../docs/operations/secrets-and-pii.md#step-7--review-what-was-marked-not-a-secret) |
| `lore secrets allow rm` | Remove an allow entry (the finding is reported again) | [operations/secrets-and-pii › Step 7 — Review what was marked not a secret](../../docs/operations/secrets-and-pii.md#step-7--review-what-was-marked-not-a-secret) |
| `lore secrets check` | Verify each referenced entry by its name (the value is read and dropped); adds the store-then-rotate tasks | [operations/secrets-and-pii › Step 3 — Keep the secret in your password manager](../../docs/operations/secrets-and-pii.md#step-3--keep-the-secret-in-your-password-manager), [Step 6 — A renamed entry or a person who leaves](../../docs/operations/secrets-and-pii.md#step-6--a-renamed-entry-or-a-person-who-leaves) |
| `lore secrets link` | Fill a ${secret:unstored} placeholder with the entry it is now stored in | [operations/secrets-and-pii › Step 3 — Keep the secret in your password manager](../../docs/operations/secrets-and-pii.md#step-3--keep-the-secret-in-your-password-manager), [Step 6 — A renamed entry or a person who leaves](../../docs/operations/secrets-and-pii.md#step-6--a-renamed-entry-or-a-person-who-leaves) |
| `lore secrets list` | Every secret reference in this view: entry, holder, row and field (opens no vault) | [operations/secrets-and-pii › Step 3 — Keep the secret in your password manager](../../docs/operations/secrets-and-pii.md#step-3--keep-the-secret-in-your-password-manager), [Step 6 — A renamed entry or a person who leaves](../../docs/operations/secrets-and-pii.md#step-6--a-renamed-entry-or-a-person-who-leaves) |
| `lore secrets move` | Rewrite the holder of matching references (one batch; never copies a secret) | [operations/secrets-and-pii › Step 3 — Keep the secret in your password manager](../../docs/operations/secrets-and-pii.md#step-3--keep-the-secret-in-your-password-manager), [Step 6 — A renamed entry or a person who leaves](../../docs/operations/secrets-and-pii.md#step-6--a-renamed-entry-or-a-person-who-leaves) |
| `lore secrets relabel` | Rewrite each reference's holder label from its holder id (one batch) | [operations/secrets-and-pii › Step 3 — Keep the secret in your password manager](../../docs/operations/secrets-and-pii.md#step-3--keep-the-secret-in-your-password-manager), [Step 6 — A renamed entry or a person who leaves](../../docs/operations/secrets-and-pii.md#step-6--a-renamed-entry-or-a-person-who-leaves) |
| `lore secrets rename-entry` | Rewrite an entry name everywhere (a renamed vault entry; one batch) | [operations/secrets-and-pii › Step 3 — Keep the secret in your password manager](../../docs/operations/secrets-and-pii.md#step-3--keep-the-secret-in-your-password-manager), [Step 6 — A renamed entry or a person who leaves](../../docs/operations/secrets-and-pii.md#step-6--a-renamed-entry-or-a-person-who-leaves) |
| `lore secrets rule` … | Secret detection rules on top of the defaults (add, edit, off, on) | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore secrets rule add` | Add a lore secret rule on top of the defaults | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore secrets rule edit` | Change a secret rule (lore defaults and gitleaks ids included) | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore secrets rule off` | Switch a secret rule off at a level, with a reason | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore secrets rule on` | Switch a secret rule back on at a level | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore secrets rules` | List secret rule rows (added, changed or off) with their level | [operations/secrets-and-pii › Add your own patterns](../../docs/operations/secrets-and-pii.md#add-your-own-patterns) |
| `lore secrets show` | Show a secret reference or a row's references; --reveal prints the value (asks every time) | [operations/secrets-and-pii › Step 3 — Keep the secret in your password manager](../../docs/operations/secrets-and-pii.md#step-3--keep-the-secret-in-your-password-manager), [Step 6 — A renamed entry or a person who leaves](../../docs/operations/secrets-and-pii.md#step-6--a-renamed-entry-or-a-person-who-leaves) |

### lore session

| Command | What it does | Guide |
|---|---|---|
| `lore session` … | Inspect this machine's agent sessions (local only, never synced) | — |
| `lore session list` | List this machine's agent sessions: actor, tool, tool session, writes, new keys, flags (local only) | [tasks/run-log › Pitfalls](../../docs/tasks/run-log.md#pitfalls) |

### lore setup

| Command | What it does | Guide |
|---|---|---|
| `lore setup` … | Open the guided setup page (setups, identity, GitHub, stores, tools, folders); --schema-only: update the local database schema after an upgrade; --agent-hooks: install the tools' session and context hooks | [knowledge/first-project › Step 2 — Nothing else to set up](../../docs/knowledge/first-project.md#step-2--nothing-else-to-set-up) *(brief)* |
| `lore setup aliases` | List the ssh host aliases of github.com, the GitHub user each logs in as, and gh's logins; --keys tests every key alone | [team/guided-setup › Step 3 — Let lore find how you reach GitHub](../../docs/team/guided-setup.md#step-3--let-lore-find-how-you-reach-github) |
| `lore setup apply` | Save one setup step: its one write, once every error-level check passes | [team/setup-cli](../../docs/team/setup-cli.md) |
| `lore setup browse` | List the subfolders of a folder under your home folder (names only), for picking a setup's folders | [team/setup-cli](../../docs/team/setup-cli.md) |
| `lore setup check` | Validate one step's answers and run its checks (writes nothing but the progress) | [team/setup-cli](../../docs/team/setup-cli.md) |
| `lore setup disable` | Disable a setup: it stays in config.yaml but matches no folder, --setup or default, and doctor --all skips it | [team/setup-cli › Step 5 — Disable and enable](../../docs/team/setup-cli.md#step-5--disable-and-enable) |
| `lore setup discover` | Find the GitHub accounts, emails, ssh aliases, keys, folders and stores on this machine, for the setup page to offer | [team/guided-setup › Step 3 — Let lore find how you reach GitHub](../../docs/team/guided-setup.md#step-3--let-lore-find-how-you-reach-github) |
| `lore setup edit` | Change a setup's folders, GitHub account, key, personal store, tools or secrets backend | [team/setup-cli](../../docs/team/setup-cli.md) |
| `lore setup enable` | Enable a disabled setup again | [team/setup-cli › Step 5 — Disable and enable](../../docs/team/setup-cli.md#step-5--disable-and-enable) |
| `lore setup fix` | Run a failing check's fix command after a confirmation | [team/setup-cli](../../docs/team/setup-cli.md) |
| `lore setup folders` | Suggest folders for a setup: where lore setup was started, its git repo and parent, and the gitconfig includeIf folders | [team/setup-cli](../../docs/team/setup-cli.md) |
| `lore setup import` | Write setup entries (as setup.import_scan proposes them) to config.yaml; a taken name is skipped | [team/setup-cli › Step 8 — Import from an existing install](../../docs/team/setup-cli.md#step-8--import-from-an-existing-install) |
| `lore setup import-dismiss` | Hide Home's import banner for the values it names (remembered in ~/.lore/state) | [team/setup-cli › Step 5 — Disable and enable](../../docs/team/setup-cli.md#step-5--disable-and-enable) |
| `lore setup import-scan` | Read an existing lore install (config.yaml, old identity files, known repos, stores.yaml) and propose setups to import; writes nothing | [team/setup-cli › Step 8 — Import from an existing install](../../docs/team/setup-cli.md#step-8--import-from-an-existing-install) |
| `lore setup join` | Join a team from an invite line: parse it and open the setup page on the join step | [team/guided-setup › Join a team from an invite](../../docs/team/guided-setup.md#step-6--join-a-team-from-an-invite) |
| `lore setup leftovers` … | The identity or personal.store of ~/.lore/config.yaml no setup uses any more: clear one (lore setup list shows them) | [knowledge/first-project › Step 2 — Nothing else to set up](../../docs/knowledge/first-project.md#step-2--nothing-else-to-set-up) *(brief)* |
| `lore setup leftovers clear` | Clear the identity or personal.store of ~/.lore/config.yaml once no setup uses it (setup list shows them) | [knowledge/first-project › Step 2 — Nothing else to set up](../../docs/knowledge/first-project.md#step-2--nothing-else-to-set-up) *(brief)* |
| `lore setup list` | List the setups on this machine and the one active here | [team/setup-cli](../../docs/team/setup-cli.md) |
| `lore setup plan` | List the guided setup steps for a kind and the progress of a setup being made | [team/setup-cli](../../docs/team/setup-cli.md) |
| `lore setup remove` | Move a setup to the trash (restorable; never its stores, repos or orgs) | [team/setup-cli](../../docs/team/setup-cli.md) |
| `lore setup rename` | Rename a setup: its entry, repo pins and saved progress take the new name | [team/setup-cli › Step 5 — Disable and enable](../../docs/team/setup-cli.md#step-5--disable-and-enable) |
| `lore setup repair` | Run a setup's checks fresh and list the steps to redo; --yes runs the safe fixes | [operations/doctor-all](../../docs/operations/doctor-all.md) |
| `lore setup run` | Open the guided setup page (setups, identity, GitHub, stores, tools, folders); --schema-only: update the local database schema after an upgrade; --agent-hooks: install the tools' session and context hooks | [team/setup-cli](../../docs/team/setup-cli.md) |
| `lore setup show` | Show one setup's entry (keys and paths, never a secret) | [team/setup-cli](../../docs/team/setup-cli.md) |
| `lore setup suggest-again` | Suggest a setup again for a GitHub account Home stopped suggesting because its setup was purged (remembered in ~/.lore/state) | [knowledge/first-project › Step 2 — Nothing else to set up](../../docs/knowledge/first-project.md#step-2--nothing-else-to-set-up) *(brief)* |
| `lore setup trash` … | Setups lore setup remove moved aside: list, restore (with their repo pins), purge for good (never a repo) | [team/setup-cli › Step 5 — Disable and enable](../../docs/team/setup-cli.md#step-5--disable-and-enable) |
| `lore setup trash list` | List the setups in the trash (lore setup remove moved them there), newest first | [team/setup-cli › Step 6 — Remove, restore and purge](../../docs/team/setup-cli.md#step-6--remove-restore-and-purge) |
| `lore setup trash purge` | Delete a trashed setup for good (never a store or a repo); --dry-run lists what stays | [team/setup-cli › Step 6 — Remove, restore and purge](../../docs/team/setup-cli.md#step-6--remove-restore-and-purge) |
| `lore setup trash restore` | Bring a setup back from the trash, with its repo pins | [team/setup-cli › Step 6 — Remove, restore and purge](../../docs/team/setup-cli.md#step-6--remove-restore-and-purge) |
| `lore setup use` | Pin this repo to a setup (--here), clear the pin, or print the shell line for one shell | [team/setup-cli](../../docs/team/setup-cli.md) |
| `lore setup web` | Open the local setup page (lore setup --web) | [team/setup-cli](../../docs/team/setup-cli.md) |

### lore show

| Command | What it does | Guide |
|---|---|---|
| `lore show` | Show any row by id (--origin: where it came from) | [knowledge/tui › Show one row without the TUI](../../docs/knowledge/tui.md#show-one-row-without-the-tui) |

### lore skill

| Command | What it does | Guide |
|---|---|---|
| `lore skill` … | Meta-tools for the skill bundle | — |
| `lore skill compile` | Compress the canonical bundle into a smaller .md with a model | [knowledge/render-and-agent-files › Build a skill file](../../docs/knowledge/render-and-agent-files.md#build-a-skill-file) *(brief)* |
| `lore skill install` | Install the lore skill bundle into each coding tool's skill folder, stamped with this binary's version | [knowledge/skill-install](../../docs/knowledge/skill-install.md) |
| `lore skill status` | Show each coding tool's installed lore skill: folder, version, hand edits and the version check | [knowledge/skill-install](../../docs/knowledge/skill-install.md) |

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
| `lore spec docs` | Write cli-commands.md (commands and errors blocks) | [reference/rest-api › For maintainers: lore spec](../../docs/reference/rest-api.md#for-maintainers-lore-spec) |
| `lore spec openapi` | Write openapi.json | [reference/rest-api › For maintainers: lore spec](../../docs/reference/rest-api.md#for-maintainers-lore-spec) |

### lore sql

| Command | What it does | Guide |
|---|---|---|
| `lore sql` … | Run one read-only SQL query over every table of the repo's lens (sensitive values masked) | [knowledge/first-project › Try it: check that everything is healthy](../../docs/knowledge/first-project.md#try-it-check-that-everything-is-healthy) *(brief)* |
| `lore sql run` | Run one read-only SQL query over every table of the repo's lens (sensitive values masked) | [knowledge/first-project › Try it: check that everything is healthy](../../docs/knowledge/first-project.md#try-it-check-that-everything-is-healthy) *(brief)* |

### lore store

| Command | What it does | Guide |
|---|---|---|
| `lore store` … | Manage lore stores: lore-only repos holding internal and personal projects | — |
| `lore store add` | Attach an existing lore store to this repo (.lore/stores.yaml) | [sync/shared-database-mode-b › Step 1 — Make the store and attach it in each repo](../../docs/sync/shared-database-mode-b.md#step-1--make-the-store-and-attach-it-in-each-repo) |
| `lore store compact` | Squash a store's history older than an age into one snapshot commit (every row is kept; asks first) | [team/identity-and-stores › How writes reach the remote](../../docs/team/identity-and-stores.md#how-writes-reach-the-remote) |
| `lore store doctor` | Check every store this repo and machine use (stores.yaml, the org, the personal store) and the v0.1.17 org stores: reachable, unpushed commits and their age, refused pushes, the clone folders | [team/commands › lore store doctor](../../docs/team/commands.md#lore-store-doctor) |
| `lore store folder` … | Project folders inside a store | — |
| `lore store folder add` | Create a folder in a store (shown only in repos that attach it) | [sync/stores › Step 4 — Split a store into folders](../../docs/sync/stores.md#step-4--split-a-store-into-folders) |
| `lore store forget` | Delete this machine's clone of a store (refused while it holds unpushed work) | [team/identity-and-stores › Step 7 — Look inside a store, or drop your copy: lore store open and forget](../../docs/team/identity-and-stores.md#step-7--look-inside-a-store-or-drop-your-copy-lore-store-open-and-forget) |
| `lore store init` | Make a git repo or folder a lore store and attach it (me = your personal store) | [team/commands › lore store init](../../docs/team/commands.md#lore-store-init) |
| `lore store list` | List the stores this repo sees, in precedence order | [sync/stores › Step 1 — Make a store and look at it](../../docs/sync/stores.md#step-1--make-a-store-and-look-at-it) |
| `lore store open` | Check out a store branch for browsing (detached, under ~/.lore/worktrees) | [team/identity-and-stores › Step 7 — Look inside a store, or drop your copy: lore store open and forget](../../docs/team/identity-and-stores.md#step-7--look-inside-a-store-or-drop-your-copy-lore-store-open-and-forget) |
| `lore store remove` | Detach a store from this repo (its rows stay in the store); asks first | [sync/stores › Step 6 — Detach a store](../../docs/sync/stores.md#step-6--detach-a-store) |
| `lore store set` | Set a store's push mode and history compaction (me, an org store as its admin, or a .lore/stores.yaml store) | [team/identity-and-stores › How writes reach the remote](../../docs/team/identity-and-stores.md#how-writes-reach-the-remote) |
| `lore store set-url` | Record a store's new address (the store repo moved or was renamed); its id and rows stay | [sync/stores › Step 3 — Follow a store that moved](../../docs/sync/stores.md#step-3--follow-a-store-that-moved) |
| `lore store show` | List the configured org stores, their clones and (with --links) the link index; with a store name, its push mode and compaction | [team/commands › lore store show](../../docs/team/commands.md#lore-store-show) |
| `lore store visibility` | Set whether a store's rows may reach client repos and committed files (internal or shareable) | [sync/stores › Step 2 — Decide whether its notes may reach client repos](../../docs/sync/stores.md#step-2--decide-whether-its-notes-may-reach-client-repos) |

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
| `lore support-bundle` … | Produce a sanitized incident-report bundle (no row text) | [operations/doctor › Send a support bundle with a bug report](../../docs/operations/doctor.md#send-a-support-bundle-with-a-bug-report) |
| `lore support-bundle run` | Produce a sanitized incident-report bundle (no row text) | [operations/doctor › Send a support bundle with a bug report](../../docs/operations/doctor.md#send-a-support-bundle-with-a-bug-report) |

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
| `lore sync purge` | Durably remove rows archived before a cutoff (moved: lore trash purge --archived-before) | [sync/how-notes-travel-with-git › Try it — turn sync off for one command](../../docs/sync/how-notes-travel-with-git.md#try-it--turn-sync-off-for-one-command) |
| `lore sync resolve` | Close a conflict, keeping the current version or the saved other one | [sync/conflicts-and-duplicates › Step 3 — The file and your database both changed](../../docs/sync/conflicts-and-duplicates.md#step-3--the-file-and-your-database-both-changed) |
| `lore sync run` | Sync now: exchange every store, reconcile .lore/data, and print what changed | [sync/how-notes-travel-with-git › Try it — turn sync off for one command](../../docs/sync/how-notes-travel-with-git.md#try-it--turn-sync-off-for-one-command) |
| `lore sync status` | Show sync state: baseline, pending exports, conflicts, broken files, live rows | [sync/how-notes-travel-with-git › Step 4 — Run a pass by hand when you want to see it](../../docs/sync/how-notes-travel-with-git.md#step-4--run-a-pass-by-hand-when-you-want-to-see-it) |
| `lore sync trash` … | Moved: lore trash --local (this spelling goes away in the next release) | [sync/how-notes-travel-with-git › Try it — turn sync off for one command](../../docs/sync/how-notes-travel-with-git.md#try-it--turn-sync-off-for-one-command) |
| `lore sync trash list` | Moved: lore trash --local (this spelling goes away in the next release) | [sync/how-notes-travel-with-git › Try it — turn sync off for one command](../../docs/sync/how-notes-travel-with-git.md#try-it--turn-sync-off-for-one-command) |
| `lore sync trash restore` | Moved: lore trash restore --local <n> | [sync/how-notes-travel-with-git › Try it — turn sync off for one command](../../docs/sync/how-notes-travel-with-git.md#try-it--turn-sync-off-for-one-command) |

### lore tag

| Command | What it does | Guide |
|---|---|---|
| `lore tag` … | Manage tags + entity-tag bindings | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |
| `lore tag add` | Create a new tag in the current project | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |
| `lore tag attach` | Attach a tag to an entity | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |
| `lore tag detach` | Detach a tag from an entity | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |
| `lore tag edit` | Change a tag's name, color or topic glob set | [knowledge/comments-and-tags › Step 4 — Rename a tag or give it files](../../docs/knowledge/comments-and-tags.md#step-4--rename-a-tag-or-give-it-files) |
| `lore tag list` | List tags in the current project | [knowledge/comments-and-tags › Step 2 — Tag a row](../../docs/knowledge/comments-and-tags.md#step-2--tag-a-row) |

### lore task

| Command | What it does | Guide |
|---|---|---|
| `lore task` … | Manage tasks (discrete work items) | — |
| `lore task accept` | Accept a proposed task (commitment accepted) | [tasks/commitment-and-snooze › Step 3 — Say yes to a proposal](../../docs/tasks/commitment-and-snooze.md#step-3--say-yes-to-a-proposal) |
| `lore task add` | Add a new task | [tasks/tasks › Step 1 — Add tasks](../../docs/tasks/tasks.md#step-1--add-tasks) |
| `lore task board` | Show tasks as a board grouped by status, assignee, priority, mission, repo or parent (read-only) | [tasks/task-tools › Step 1 — See the tasks as a board](../../docs/tasks/task-tools.md#step-1--see-the-tasks-as-a-board) |
| `lore task cancel` | Cancel a task | [tasks/tasks › Step 8 — Cancel or delete a task](../../docs/tasks/tasks.md#step-8--cancel-or-delete-a-task) |
| `lore task checklist` … | Checklist items inside a task (add, tick, untick, promote to a sub-task) | [tasks/task-tools › Step 2 — Keep a checklist inside a task](../../docs/tasks/task-tools.md#step-2--keep-a-checklist-inside-a-task) |
| `lore task checklist add` | Add checklist items to a task; if you would ever claim it, make it a sub-task | [tasks/task-tools › Step 2 — Keep a checklist inside a task](../../docs/tasks/task-tools.md#step-2--keep-a-checklist-inside-a-task) |
| `lore task checklist edit` | Change the text of a checklist item | [tasks/task-tools › Step 2 — Keep a checklist inside a task](../../docs/tasks/task-tools.md#step-2--keep-a-checklist-inside-a-task) |
| `lore task checklist import` | Turn the - [ ] / - [x] lines of a task's body into checklist items | [tasks/task-tools › Step 2 — Keep a checklist inside a task](../../docs/tasks/task-tools.md#step-2--keep-a-checklist-inside-a-task) |
| `lore task checklist list` | List a task's checklist (positions and chk_ ids) | [tasks/task-tools › Step 2 — Keep a checklist inside a task](../../docs/tasks/task-tools.md#step-2--keep-a-checklist-inside-a-task) |
| `lore task checklist promote` | Turn a checklist item into a sub-task (same text, item removed, one batch) | [tasks/task-tools › Step 2 — Keep a checklist inside a task](../../docs/tasks/task-tools.md#step-2--keep-a-checklist-inside-a-task) |
| `lore task checklist rm` | Remove a checklist item (kept as a tombstone so copies merge) | [tasks/task-tools › Step 2 — Keep a checklist inside a task](../../docs/tasks/task-tools.md#step-2--keep-a-checklist-inside-a-task) |
| `lore task checklist tick` | Tick a checklist item (by position or chk_ id); the task status stays | [tasks/task-tools › Step 2 — Keep a checklist inside a task](../../docs/tasks/task-tools.md#step-2--keep-a-checklist-inside-a-task) |
| `lore task checklist untick` | Untick a checklist item | [tasks/task-tools › Step 2 — Keep a checklist inside a task](../../docs/tasks/task-tools.md#step-2--keep-a-checklist-inside-a-task) |
| `lore task claim` | Claim a task (or coordinate a mission) on the signals ref, with a lease | [team/commands › lore task claim](../../docs/team/commands.md#lore-task-claim) |
| `lore task decline` | Decline a proposed task (cancelled, with a close reason) | [tasks/commitment-and-snooze › Step 3 — Say yes to a proposal](../../docs/tasks/commitment-and-snooze.md#step-3--say-yes-to-a-proposal) |
| `lore task deferred` | Show deferred tasks (snoozed; deferred_until in the future) | [tasks/commitment-and-snooze › Step 4 — Snooze a task until a date](../../docs/tasks/commitment-and-snooze.md#step-4--snooze-a-task-until-a-date) |
| `lore task delete` | Delete a task and its sub-tasks (it goes to the trash; lore trash restore brings it back) | [tasks/tasks › Step 8 — Cancel or delete a task](../../docs/tasks/tasks.md#step-8--cancel-or-delete-a-task) |
| `lore task done` | Mark a task done (with the code that finished it) | [tasks/tasks › Step 6 — Finish a task](../../docs/tasks/tasks.md#step-6--finish-a-task) |
| `lore task edit` | Edit task fields (only flags you pass are applied) | [tasks/tasks › Step 5 — Change a task](../../docs/tasks/tasks.md#step-5--change-a-task) |
| `lore task export` | Export tasks as CSV, JSON (the import shape), Markdown or a GitHub issue list | [tasks/tasks › Step 12 — Move tasks in and out with files](../../docs/tasks/tasks.md#step-12--move-tasks-in-and-out-with-files) |
| `lore task heartbeat` | Renew your claim's lease (alive, not progress) | [team/commands › lore task heartbeat](../../docs/team/commands.md#lore-task-heartbeat) |
| `lore task history` | Show a task's activity history (field changes, claims; --git for older versions) | [tasks/task-tools › Step 7 — Read a task's history](../../docs/tasks/task-tools.md#step-7--read-a-tasks-history) |
| `lore task import` | Import tasks from a CSV or JSON file (one batch) | [tasks/tasks › Step 12 — Move tasks in and out with files](../../docs/tasks/tasks.md#step-12--move-tasks-in-and-out-with-files) |
| `lore task list` | List tasks | [tasks/tasks › Step 2 — See your list](../../docs/tasks/tasks.md#step-2--see-your-list) |
| `lore task merged` | Record that a done task's work reached the default branch | [tasks/link-commits › Step 5 — Mark a task merged by hand](../../docs/tasks/link-commits.md#step-5--mark-a-task-merged-by-hand) |
| `lore task next` | Claim the next ready task (resume-first, leaves only, dependencies, gates, urgency) | [team/commands › lore task next](../../docs/team/commands.md#lore-task-next) |
| `lore task note` | Record a progress note on a task (pushes your work branch) | [team/commands › lore task note](../../docs/team/commands.md#lore-task-note) |
| `lore task reassign` | Assign a task to someone else, dropping any claim (human override); --from reassigns every open task of a person as one batch | [team/agents › Step 3 — Give the task to someone else: lore task reassign](../../docs/team/agents.md#step-3--give-the-task-to-someone-else-lore-task-reassign) |
| `lore task recur` … | A repeating task's schedule (show, edit, skip, stop) | [tasks/task-tools › Step 9 — Make a task repeat](../../docs/tasks/task-tools.md#step-9--make-a-task-repeat) |
| `lore task recur edit` | Change the recurrence rule (this and later instances) | [tasks/task-tools › Step 9 — Make a task repeat](../../docs/tasks/task-tools.md#step-9--make-a-task-repeat) |
| `lore task recur show` | Show a recurring task's rule, series and next dates | [tasks/task-tools › Step 9 — Make a task repeat](../../docs/tasks/task-tools.md#step-9--make-a-task-repeat) |
| `lore task recur skip` | Close this instance as skipped and create the next one | [tasks/task-tools › Step 9 — Make a task repeat](../../docs/tasks/task-tools.md#step-9--make-a-task-repeat) |
| `lore task recur stop` | Stop the series: no further instances (this one stays) | [tasks/task-tools › Step 9 — Make a task repeat](../../docs/tasks/task-tools.md#step-9--make-a-task-repeat) |
| `lore task relate` | Relate a task to another (duplicates, relates-to, follows-up, blocked-by, blocks) | [tasks/task-tools › Step 5 — Relate tasks to each other](../../docs/tasks/task-tools.md#step-5--relate-tasks-to-each-other) |
| `lore task release` | Give back a claim you hold | [team/agents › Step 1 — Give back a claim you hold: lore task release](../../docs/team/agents.md#step-1--give-back-a-claim-you-hold-lore-task-release) |
| `lore task revoke` | Drop the current claim on a task (human override; the holder is fenced out) | [team/agents › Step 2 — Take a claim away: lore task revoke](../../docs/team/agents.md#step-2--take-a-claim-away-lore-task-revoke) |
| `lore task search` | Full-text search across task title + body (BM25 ranked) | [tasks/tasks › Step 7 — Search your tasks](../../docs/tasks/tasks.md#step-7--search-your-tasks) |
| `lore task show` | Show task details | [team/commands › lore task show](../../docs/team/commands.md#lore-task-show) |
| `lore task someday` | Show someday/maybe tasks (parking lot, no commitment) | [tasks/commitment-and-snooze › Step 2 — Look at proposals and ideas](../../docs/tasks/commitment-and-snooze.md#step-2--look-at-proposals-and-ideas) |
| `lore task split` | Split a task into sub-tasks (one batch) | [tasks/task-tools › Step 3 — Split a task into sub-tasks](../../docs/tasks/task-tools.md#step-3--split-a-task-into-sub-tasks) |
| `lore task stale` | List open tasks with no change for N days; --close cancels them (close_reason stale) | [tasks/task-tools › Step 10 — Sweep out stale tasks](../../docs/tasks/task-tools.md#step-10--sweep-out-stale-tasks) |
| `lore task start` | Mark task as in_progress | [tasks/tasks › Step 3 — Start a task](../../docs/tasks/tasks.md#step-3--start-a-task) |
| `lore task template` … | Task templates (add, edit, rename, show, delete) | [tasks/task-tools › Step 8 — Save work you repeat as a template](../../docs/tasks/task-tools.md#step-8--save-work-you-repeat-as-a-template) |
| `lore task template add` | Add a task template from a YAML file or an existing task tree | [tasks/task-tools › Step 8 — Save work you repeat as a template](../../docs/tasks/task-tools.md#step-8--save-work-you-repeat-as-a-template) |
| `lore task template delete` | Delete a task template (trash, like any row) | [tasks/task-tools › Step 8 — Save work you repeat as a template](../../docs/tasks/task-tools.md#step-8--save-work-you-repeat-as-a-template) |
| `lore task template edit` | Replace a task template's description, args and tree from a YAML file | [tasks/task-tools › Step 8 — Save work you repeat as a template](../../docs/tasks/task-tools.md#step-8--save-work-you-repeat-as-a-template) |
| `lore task template list` | List task templates (nearest store first) | [tasks/task-tools › Step 8 — Save work you repeat as a template](../../docs/tasks/task-tools.md#step-8--save-work-you-repeat-as-a-template) |
| `lore task template rename` | Rename a task template (the id stays) | [tasks/task-tools › Step 8 — Save work you repeat as a template](../../docs/tasks/task-tools.md#step-8--save-work-you-repeat-as-a-template) |
| `lore task template show` | Show a task template in its YAML form | [tasks/task-tools › Step 8 — Save work you repeat as a template](../../docs/tasks/task-tools.md#step-8--save-work-you-repeat-as-a-template) |
| `lore task time` … | Time spent on a task (show, log, edit, stop) | [tasks/run-log › Step 5 — Log time by hand](../../docs/tasks/run-log.md#step-5--log-time-by-hand), [Step 6 — Fix or remove an entry](../../docs/tasks/run-log.md#step-6--fix-or-remove-an-entry), [Step 7 — Stop the clock](../../docs/tasks/run-log.md#step-7--stop-the-clock-without-changing-the-status) |
| `lore task time edit` | Change a time entry (auto entries keep their original times) | [tasks/run-log › Step 5 — Log time by hand](../../docs/tasks/run-log.md#step-5--log-time-by-hand), [Step 6 — Fix or remove an entry](../../docs/tasks/run-log.md#step-6--fix-or-remove-an-entry), [Step 7 — Stop the clock](../../docs/tasks/run-log.md#step-7--stop-the-clock-without-changing-the-status) |
| `lore task time log` | Log time on a task by hand | [tasks/run-log › Step 5 — Log time by hand](../../docs/tasks/run-log.md#step-5--log-time-by-hand), [Step 6 — Fix or remove an entry](../../docs/tasks/run-log.md#step-6--fix-or-remove-an-entry), [Step 7 — Stop the clock](../../docs/tasks/run-log.md#step-7--stop-the-clock-without-changing-the-status) |
| `lore task time restore` | Undo the removal of a time entry | [tasks/run-log › Step 5 — Log time by hand](../../docs/tasks/run-log.md#step-5--log-time-by-hand), [Step 6 — Fix or remove an entry](../../docs/tasks/run-log.md#step-6--fix-or-remove-an-entry), [Step 7 — Stop the clock](../../docs/tasks/run-log.md#step-7--stop-the-clock-without-changing-the-status) |
| `lore task time rm` | Remove a time entry (kept as a tombstone; restore undoes it) | [tasks/run-log › Step 5 — Log time by hand](../../docs/tasks/run-log.md#step-5--log-time-by-hand), [Step 6 — Fix or remove an entry](../../docs/tasks/run-log.md#step-6--fix-or-remove-an-entry), [Step 7 — Stop the clock](../../docs/tasks/run-log.md#step-7--stop-the-clock-without-changing-the-status) |
| `lore task time show` | Show a task's tracked time (effort, elapsed, manual, per worker) | [tasks/run-log › Step 5 — Log time by hand](../../docs/tasks/run-log.md#step-5--log-time-by-hand), [Step 6 — Fix or remove an entry](../../docs/tasks/run-log.md#step-6--fix-or-remove-an-entry), [Step 7 — Stop the clock](../../docs/tasks/run-log.md#step-7--stop-the-clock-without-changing-the-status) |
| `lore task time stop` | Close your own open status span; the status stays | [tasks/run-log › Step 5 — Log time by hand](../../docs/tasks/run-log.md#step-5--log-time-by-hand), [Step 6 — Fix or remove an entry](../../docs/tasks/run-log.md#step-6--fix-or-remove-an-entry), [Step 7 — Stop the clock](../../docs/tasks/run-log.md#step-7--stop-the-clock-without-changing-the-status) |
| `lore task tree` … | Check a task's sub-task tree | — |
| `lore task tree check` | Check the sub-task tree: parent cycles, orphans, open children of closed parents, depth, shared ranks (--fix repairs cycles) | [tasks/task-tools › Step 4 — Check the sub-task tree](../../docs/tasks/task-tools.md#step-4--check-the-sub-task-tree) |
| `lore task triage` | Show proposed tasks (suggested, not yet committed) | [tasks/commitment-and-snooze › Step 2 — Look at proposals and ideas](../../docs/tasks/commitment-and-snooze.md#step-2--look-at-proposals-and-ideas) |
| `lore task unrelate` | Remove a relation added with task relate | [tasks/task-tools › Step 5 — Relate tasks to each other](../../docs/tasks/task-tools.md#step-5--relate-tasks-to-each-other) |
| `lore task unwatch` | Stop watching a task | [tasks/task-tools › Step 6 — Watch a task](../../docs/tasks/task-tools.md#step-6--watch-a-task) |
| `lore task view` … | Saved task list views (add, edit, show, delete) | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task view add` | Save a task list filter as a named view | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task view delete` | Delete a saved task view | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task view edit` | Change a saved task view (only the flags you pass) | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task view list` | List saved task views | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task view show` | Show a saved task view | [tasks/tasks › Step 11 — Save a filter under a name](../../docs/tasks/tasks.md#step-11--save-a-filter-under-a-name) |
| `lore task watch` | Watch a task (changes reach your inbox) | [tasks/task-tools › Step 6 — Watch a task](../../docs/tasks/task-tools.md#step-6--watch-a-task) |

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
| `lore trash` … | List the rows in the trash | [sync/trash-and-purge › Step 4 — Delete a note with lore and bring it back](../../docs/sync/trash-and-purge.md#step-4--delete-a-note-with-lore-and-bring-it-back), [Step 5 — Undo a bulk delete](../../docs/sync/trash-and-purge.md#step-5--undo-a-bulk-delete) |
| `lore trash list` | List the rows in the trash | [sync/trash-and-purge › Step 4 — Delete a note with lore and bring it back](../../docs/sync/trash-and-purge.md#step-4--delete-a-note-with-lore-and-bring-it-back), [Step 5 — Undo a bulk delete](../../docs/sync/trash-and-purge.md#step-5--undo-a-bulk-delete) |
| `lore trash purge` | Remove rows from the trash for good, with their comments and links (a tombstone stays forever); needs --confirm | [sync/trash-and-purge › Step 4 — Delete a note with lore and bring it back](../../docs/sync/trash-and-purge.md#step-4--delete-a-note-with-lore-and-bring-it-back), [Step 5 — Undo a bulk delete](../../docs/sync/trash-and-purge.md#step-5--undo-a-bulk-delete) |
| `lore trash restore` | Bring a row (with its dependents) or a whole batch back from the trash, same ids | [sync/trash-and-purge › Step 4 — Delete a note with lore and bring it back](../../docs/sync/trash-and-purge.md#step-4--delete-a-note-with-lore-and-bring-it-back), [Step 5 — Undo a bulk delete](../../docs/sync/trash-and-purge.md#step-5--undo-a-bulk-delete) |
| `lore trash show` | Show one row in the trash, body included (sensitive values masked) | [sync/trash-and-purge › Step 4 — Delete a note with lore and bring it back](../../docs/sync/trash-and-purge.md#step-4--delete-a-note-with-lore-and-bring-it-back), [Step 5 — Undo a bulk delete](../../docs/sync/trash-and-purge.md#step-5--undo-a-bulk-delete) |
| `lore trash stats` | Count the trash per type, store and deleter, rows past retention, and tombstones | [sync/trash-and-purge › Step 4 — Delete a note with lore and bring it back](../../docs/sync/trash-and-purge.md#step-4--delete-a-note-with-lore-and-bring-it-back), [Step 5 — Undo a bulk delete](../../docs/sync/trash-and-purge.md#step-5--undo-a-bulk-delete) |

### lore tui

| Command | What it does | Guide |
|---|---|---|
| `lore tui` | Browse every table of the attached stores in the terminal (read-only) | [knowledge/tui › Step 1 — Open it](../../docs/knowledge/tui.md#step-1--open-it) |

### lore upgrade

| Command | What it does | Guide |
|---|---|---|
| `lore upgrade` … | Install the newest lore release (or this project's pinned version, or exactly --version) | [operations/upgrade](../../docs/operations/upgrade.md) |
| `lore upgrade run` | Install the newest lore release (or this project's pinned version, or exactly --version) | [operations/upgrade](../../docs/operations/upgrade.md) |

### lore vars

| Command | What it does | Guide |
|---|---|---|
| `lore vars` … | Project variables, used as ${var:<name>} in any text field | [knowledge/variables-and-metadata › Step 1 — Set a variable](../../docs/knowledge/variables-and-metadata.md#step-1--set-a-variable), [Step 4 — Rename a variable](../../docs/knowledge/variables-and-metadata.md#step-4--rename-a-variable) |
| `lore vars list` | List project variables (nearest definition per name) | [knowledge/variables-and-metadata › Step 1 — Set a variable](../../docs/knowledge/variables-and-metadata.md#step-1--set-a-variable), [Step 4 — Rename a variable](../../docs/knowledge/variables-and-metadata.md#step-4--rename-a-variable) |
| `lore vars rename` | Rename a variable (the old name stays an alias) | [knowledge/variables-and-metadata › Step 1 — Set a variable](../../docs/knowledge/variables-and-metadata.md#step-1--set-a-variable), [Step 4 — Rename a variable](../../docs/knowledge/variables-and-metadata.md#step-4--rename-a-variable) |
| `lore vars rm` | Remove a variable (to the trash, restorable) | [knowledge/variables-and-metadata › Step 1 — Set a variable](../../docs/knowledge/variables-and-metadata.md#step-1--set-a-variable), [Step 4 — Rename a variable](../../docs/knowledge/variables-and-metadata.md#step-4--rename-a-variable) |
| `lore vars set` | Set a variable (adds it, or changes its value in that place) | [knowledge/variables-and-metadata › Step 1 — Set a variable](../../docs/knowledge/variables-and-metadata.md#step-1--set-a-variable), [Step 4 — Rename a variable](../../docs/knowledge/variables-and-metadata.md#step-4--rename-a-variable) |
| `lore vars show` | Show one variable | [knowledge/variables-and-metadata › Step 1 — Set a variable](../../docs/knowledge/variables-and-metadata.md#step-1--set-a-variable), [Step 4 — Rename a variable](../../docs/knowledge/variables-and-metadata.md#step-4--rename-a-variable) |
| `lore vars where` | Every place that defines a variable | [knowledge/variables-and-metadata › Step 1 — Set a variable](../../docs/knowledge/variables-and-metadata.md#step-1--set-a-variable), [Step 4 — Rename a variable](../../docs/knowledge/variables-and-metadata.md#step-4--rename-a-variable) |

### lore version

| Command | What it does | Guide |
|---|---|---|
| `lore version` | Print version information | [knowledge/first-project › Before you start](../../docs/knowledge/first-project.md#before-you-start) *(brief)* |

### lore whoami

| Command | What it does | Guide |
|---|---|---|
| `lore whoami` … | Show the resolved org, what decided it, your identity and what loads here | [team/commands › lore whoami](../../docs/team/commands.md#lore-whoami) |
| `lore whoami show` | Show the resolved org, what decided it, your identity and what loads here | [team/commands › lore whoami](../../docs/team/commands.md#lore-whoami) |

### lore why-context

| Command | What it does | Guide |
|---|---|---|
| `lore why-context` … | Show the last rendered context (what the coding tool saw) | [knowledge/render-and-agent-files › Try it: what did the tool read?](../../docs/knowledge/render-and-agent-files.md#try-it-what-did-the-tool-read) |
| `lore why-context show` | Show the last rendered context (what the agent saw); --cut: what the render moved out of the core of LORE.md, and why | [knowledge/render-and-agent-files › Try it: what did the tool read?](../../docs/knowledge/render-and-agent-files.md#try-it-what-did-the-tool-read) |

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
