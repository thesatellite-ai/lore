# lore documentation

**In short:** lore keeps what your project knows (rules, memories, decisions, tasks) as small files in your git repo, and hands the important ones to your coding tool every time it starts. You can use it alone in one repo in five minutes. The same tool grows to a team, private notes a client never sees, and several agents sharing one plan. Follow the learning path below, or jump to your goal.

![How lore is built: callers at the top, then surface, wire, ops, engine, store and adapters, and the .lore/data row files in git at the bottom; a command goes down and is committed and pushed, and teammates' rows come back up as the context file coding agents read](assets/architecture.svg)

The picture, from top to bottom: you or an agent run a lore command; lore decides who sees the note (`--to`) and when (`--live`); it saves it in git, either in the `.lore/data` folder or on a hidden list on GitHub that is not a branch (git calls it a ref), and keeps a fast local database in step. Going back up, lore fetches your teammates' notes and merges their edits one field at a time, so two people can change different parts of the same note. It keeps private notes out of the client's files and writes the context file your agents read. The helpers on the side keep it safe: git hooks (small scripts git runs before a commit or push), the merge driver (a small helper git calls to combine two edits of the same note), the CI job (a check that runs on every push) and the health check (`lore doctor`). Read [How it works](team/how-it-works.md) for the full story.

## Learning path

Each step builds on the one before, from your first rule in one repo to a team with a client, private stores, several agents and CI. Every example is a script that runs the real lore binary in a throwaway folder: a `docs` example link opens the script and "output" opens what it printed; a team example link opens the printed output of a section in `tests/team/docs/run-examples.sh`.

| Step | What you learn | Guide | Example |
|---|---|---|---|
| 1 | Set up lore in one repo, write a first rule, and give it to your coding tool | [Your first project](knowledge/first-project.md) | [`start`](../tests/docs/sections/start.sh) ([output](../tests/docs/transcripts/start.txt)) |
| 2 | The kinds of note you use most (rules, memories, decisions), the other kinds, comments and tags, and values you change in one place | [Rules, memories and decisions](knowledge/rules-memories-decisions.md), [Other kinds of notes](knowledge/other-row-kinds.md), [Variables and metadata](knowledge/variables-and-metadata.md) | [`knowledge`](../tests/docs/sections/knowledge.sh) ([output](../tests/docs/transcripts/knowledge.txt)), [`meta`](../tests/docs/sections/meta.sh) ([output](../tests/docs/transcripts/meta.txt)) |
| 3 | Find any note again, and choose what your tool reads when it starts | [Search](knowledge/search.md), [Render and agent files](knowledge/render-and-agent-files.md) | [`search`](../tests/docs/sections/search.sh) ([output](../tests/docs/transcripts/search.txt)), [`render`](../tests/docs/sections/render.sh) ([output](../tests/docs/transcripts/render.txt)) |
| 4 | Keep your to-do list next to the code: tasks, snooze, reminders, commit links, then boards, checklists, templates and repeating tasks | [Tasks](tasks/tasks.md), [Task tools](tasks/task-tools.md) | [`tasks`](../tests/docs/sections/tasks.sh) ([output](../tests/docs/transcripts/tasks.txt)), [`task-tools`](../tests/docs/sections/task-tools.sh) ([output](../tests/docs/transcripts/task-tools.txt)) |
| 5 | Bigger goals: missions, plans, plan files, handoffs and the run log | [Missions and plans](tasks/missions-and-plans.md), [Run log](tasks/run-log.md) | [`missions`](../tests/docs/sections/missions.sh) ([output](../tests/docs/transcripts/missions.txt)), [`runs`](../tests/docs/sections/runs.sh) ([output](../tests/docs/transcripts/runs.txt)) |
| 6 | How notes travel with your commits, branches and pull requests; conflicts, duplicates and the trash | [How notes travel with git](sync/how-notes-travel-with-git.md), [Conflicts and duplicates](sync/conflicts-and-duplicates.md), [Trash, restore and purge](sync/trash-and-purge.md) | [`sync`](../tests/docs/sections/sync.sh) ([output](../tests/docs/transcripts/sync.txt)) |
| 7 | Several repos as one project, and stores of your own: notes kept outside the repo | [Projects and repos](sync/multi-repo-projects.md), [Your stores](sync/stores.md) | [`projects`](../tests/docs/sections/projects.sh) ([output](../tests/docs/transcripts/projects.txt)), [`stores`](../tests/docs/sections/stores.sh) ([output](../tests/docs/transcripts/stores.txt)) |
| 8 | Keep lore healthy and safe: the doctor, a damaged cache, the audit log, secrets, upgrades | [Doctor](operations/doctor.md), [Secrets and personal data](operations/secrets-and-pii.md), [Upgrade lore](operations/upgrade.md) | [`ops`](../tests/docs/sections/ops.sh) ([output](../tests/docs/transcripts/ops.txt)), [`secrets`](../tests/docs/sections/secrets.sh) ([output](../tests/docs/transcripts/secrets.txt)) |
| 9 | Set lore up on a machine for one or more companies, from the command line or the guided page | [Setups from the command line](team/setup-cli.md), [Guided setup](team/guided-setup.md) | [`setup-cli`](../tests/team/docs/transcripts/setup-cli.txt) |
| 10 | A small team: tasks that reach a teammate at once, invites, and people's profiles | [Getting started with teams](team/getting-started.md), [Team invites](team/invites.md), [Team profiles](team/profiles.md) | [`getting-started`](../tests/team/docs/transcripts/getting-started.txt), [`small-team`](../tests/team/docs/transcripts/small-team.txt), [`invites`](../tests/team/docs/transcripts/invites.txt), [`profiles`](../tests/team/docs/transcripts/profiles.txt) |
| 11 | An agency and a client: an org, a private store, notes the client never sees, contractors and two companies on one laptop | [Agency with a client repo](team/use-cases/agency-client-repo.md), [How to lay out your stores](choosing/store-layout.md), [Contractor](team/use-cases/contractor.md), [Two companies](team/use-cases/two-companies.md) | [`agency`](../tests/team/docs/transcripts/agency.txt), [`contractor`](../tests/team/docs/transcripts/contractor.txt), [`two-orgs`](../tests/team/docs/transcripts/two-orgs.txt) |
| 12 | Agents on one mission: claims, leases, a crash and a reclaim, a gate a person approves | [Agents](team/agents.md), [Agents on one mission](team/use-cases/agents-mission.md) | [`agents`](../tests/team/docs/transcripts/agents.txt) |
| 13 | The full production setup: every setup checked, CI in read-only mode, the GitHub workflow, leak guards, and what to do after a leak | [Checking every setup](operations/doctor-all.md), [Use lore in CI](operations/ci.md), [The GitHub workflow](sync/github-workflow.md), [Cleaning up after a leak](team/use-cases/leak-cleanup.md) | [`ops`](../tests/docs/sections/ops.sh) ([output](../tests/docs/transcripts/ops.txt)), [`leak-cleanup`](../tests/team/docs/transcripts/leak-cleanup.txt) |

## Start from your goal

| I want to… | Read | Example |
|---|---|---|
| try lore in five minutes | [Your first project](knowledge/first-project.md) | [`start`](../tests/docs/sections/start.sh) ([output](../tests/docs/transcripts/start.txt)) |
| choose how to set lore up (where notes live, live or with the code, stores, orgs, agent files, agents) | [Decision guides](choosing/README.md) | — |
| find the settings that fit my kind of team | [Which options for which kind of team](choosing/by-team-shape.md) | — |
| pick one command when lore has several ways to do a job | [Which command for which job](choosing/workflow-methods.md) | — |
| set lore up on a new machine, or for a second company | [Setups from the command line](team/setup-cli.md), [Guided setup](team/guided-setup.md) | [`setup-cli`](../tests/team/docs/transcripts/setup-cli.txt) |
| invite a teammate | [Team invites](team/invites.md) | [`invites`](../tests/team/docs/transcripts/invites.txt) |
| make and list the team store's client folders from a parent folder that is not a repo | [Client folders from the parent folder](team/client-folders.md) | [`client-folders`](../tests/team/docs/transcripts/client-folders.txt) |
| record who is on the team, their contacts and roles | [Team profiles](team/profiles.md) | [`profiles`](../tests/team/docs/transcripts/profiles.txt) |
| bring in the notes I already have in markdown | [Bring in notes you already have](knowledge/import-existing-notes.md) | [`knowledge`](../tests/docs/sections/knowledge.sh) ([output](../tests/docs/transcripts/knowledge.txt)) |
| move an existing repo onto lore with a coding tool's help | [Migration prompt](MIGRATE_TO_LORE.md) | — |
| make my coding tool read my rules | [Render and agent files](knowledge/render-and-agent-files.md), [Which file your coding tool reads](choosing/render-targets.md) | [`render`](../tests/docs/sections/render.sh) ([output](../tests/docs/transcripts/render.txt)) |
| keep a value (a host, a port) in one place, or add my own fields to notes | [Variables and metadata](knowledge/variables-and-metadata.md) | [`meta`](../tests/docs/sections/meta.sh) ([output](../tests/docs/transcripts/meta.txt)) |
| keep a to-do list in the repo | [Tasks](tasks/tasks.md), [Task tools](tasks/task-tools.md) | [`tasks`](../tests/docs/sections/tasks.sh) ([output](../tests/docs/transcripts/tasks.txt)), [`task-tools`](../tests/docs/sections/task-tools.sh) ([output](../tests/docs/transcripts/task-tools.txt)) |
| give a teammate a task they see at once | [Small team in one repo](team/use-cases/small-team.md), [Keep a note with the code, or share it at once](choosing/with-code-or-live.md) | [`small-team`](../tests/team/docs/transcripts/small-team.txt) |
| keep private notes about a client out of the client's repo | [Agency with a client repo](team/use-cases/agency-client-repo.md), [Your stores](sync/stores.md) | [`agency`](../tests/team/docs/transcripts/agency.txt), [`stores`](../tests/docs/sections/stores.sh) ([output](../tests/docs/transcripts/stores.txt)) |
| share one private note with the client on purpose | [Sharing an internal note with the client](team/use-cases/sharing-a-note.md) | [`agency`](../tests/team/docs/transcripts/agency.txt) |
| keep notes only I can see | [Personal notes](team/use-cases/personal-notes.md) | [`personal`](../tests/team/docs/transcripts/personal.txt) |
| work across several repos of one product | [Where your notes live](choosing/database-mode.md), [Several repos as one product](team/use-cases/group-project.md) | [`group`](../tests/team/docs/transcripts/group.txt), [`projects`](../tests/docs/sections/projects.sh) ([output](../tests/docs/transcripts/projects.txt)) |
| let a contractor see one client only | [Contractor limited to one client](team/use-cases/contractor.md) | [`contractor`](../tests/team/docs/transcripts/contractor.txt) |
| run several agents on one plan | [When to use agents, claims and gates](choosing/agents-claims-gates.md), [Agents on one mission](team/use-cases/agents-mission.md) | [`agents`](../tests/team/docs/transcripts/agents.txt) |
| record our servers and domains, and what breaks if one goes down | [Resources](operations/resources.md) | [`resources`](../tests/docs/sections/resources.sh) ([output](../tests/docs/transcripts/resources.txt)) |
| keep a password out of my notes but still point at it | [Secrets and personal data](operations/secrets-and-pii.md) | [`secrets`](../tests/docs/sections/secrets.sh) ([output](../tests/docs/transcripts/secrets.txt)) |
| fix a merge conflict in a note | [Conflicts and duplicates](sync/conflicts-and-duplicates.md) | [`sync`](../tests/docs/sections/sync.sh) ([output](../tests/docs/transcripts/sync.txt)) |
| get a note back that I deleted | [Trash, restore from git and purge](sync/trash-and-purge.md) | [`sync`](../tests/docs/sections/sync.sh) ([output](../tests/docs/transcripts/sync.txt)) |
| recover from a damaged cache | [A damaged cache](operations/backup-restore-repair.md) | [`ops`](../tests/docs/sections/ops.sh) ([output](../tests/docs/transcripts/ops.txt)) |
| upgrade lore, or pin one version for the team | [Upgrade lore](operations/upgrade.md) | [`ops`](../tests/docs/sections/ops.sh) ([output](../tests/docs/transcripts/ops.txt)) |
| run lore in CI | [Use lore in CI](operations/ci.md) | [`ops`](../tests/docs/sections/ops.sh) ([output](../tests/docs/transcripts/ops.txt)) |
| call lore from another program over HTTP | [Local REST API](reference/rest-api.md) | [`api`](../tests/docs/sections/api.sh) ([output](../tests/docs/transcripts/api.txt)) |
| find out what an error means | [Error codes and exit statuses](reference/errors.md), [Troubleshooting](team/troubleshooting.md) | [`reference`](../tests/docs/sections/reference.sh) ([output](../tests/docs/transcripts/reference.txt)), [`errors`](../tests/team/docs/transcripts/errors.txt) |
| look up one command, flag or setting | [Reference](reference/README.md), [Feature index](reference/features.md) | — |

## Every page

### Knowledge: write it down, find it, hand it to your tool

- [Knowledge guides](knowledge/README.md): the overview of this section.
- [Your first project](knowledge/first-project.md): `lore init`, a first rule, `lore render`; also setup, doctor, tables and shell completion.
- [Rules, memories and decisions](knowledge/rules-memories-decisions.md): add, list, change, replace, retire and delete the three kinds you use most, and why a password is refused.
- [Other kinds of notes](knowledge/other-row-kinds.md): hotfixes, patterns, playbooks, prompts, snapshots and ten more.
- [Comments and tags](knowledge/comments-and-tags.md): reply to a note, and label notes so they can be found together.
- [Bring in notes you already have](knowledge/import-existing-notes.md): turn an old notes file into rules, decisions and memories, and outside sources.
- [Variables and metadata](knowledge/variables-and-metadata.md): `lore vars` and `lore meta`, one named value used in many notes, and extra fields on any row.
- [Search](knowledge/search.md): one search over every kind of note, and keeping the index healthy.
- [Render and agent files](knowledge/render-and-agent-files.md): `lore render`, other agent files, the directive block, what the tool read, and skill files.
- [Keep the lore skill current](knowledge/skill-install.md): `lore skill install` and `lore skill status`, the `lore_version` line.
- [Browse your notes in the terminal](knowledge/tui.md): `lore tui`.

### Tasks, missions and plans

- [Tasks, missions and plans](tasks/README.md): the overview of this section.
- [Tasks](tasks/tasks.md): add, list, start, finish, search and group tasks; one task waiting for another; saved filters.
- [Task tools](tasks/task-tools.md): board, checklists, split, relations, watch, history, templates, repeating tasks, stale sweep.
- [Commitment and snooze](tasks/commitment-and-snooze.md): ideas, suggestions and tasks for later, kept out of your normal list.
- [Reminders](tasks/reminders.md): notes with a date, including ones that repeat.
- [Link commits to tasks](tasks/link-commits.md): which commit finished which task, and when it reached `main`.
- [Missions and plans](tasks/missions-and-plans.md): bigger goals, written plans, plan files and handoffs.
- [Run log](tasks/run-log.md): one timed try at a piece of work.

### Notes that travel with git

- [Notes that travel with git](sync/README.md): the overview of this section.
- [How notes travel with git](sync/how-notes-travel-with-git.md): `.lore/data`, commits, clones, and turning sync off for one command.
- [Branches: peek and promote](sync/branches-peek-promote.md): read another branch's notes, and copy one to `main` now.
- [Conflicts and duplicates](sync/conflicts-and-duplicates.md): field-by-field merges, real conflicts, the same fact written twice, and two separate setups in one repo.
- [Trash, restore from git and purge](sync/trash-and-purge.md): get a lost note back, write `.lore/data` again, delete old notes for good.
- [Hooks and the merge driver](sync/hooks-and-merge-driver.md): the git set-up lore adds and why.
- [The GitHub workflow](sync/github-workflow.md): let GitHub merge note files with lore.
- [Projects and repos](sync/multi-repo-projects.md): projects, repos inside a project, and renamed repos.
- [One store for several repos](sync/shared-database-mode-b.md): one folder store attached in each repo; converting an old shared-database (Mode B) repo with `lore migrate run`.
- [Your stores: list, move, retire](sync/stores.md): `lore store init`, list, visibility, a moved store, folders, moving a note into a store, and detaching one.

### Keep lore healthy and safe

- [Keep lore healthy and safe](operations/README.md): the overview of this section.
- [Doctor](operations/doctor.md): the health check, its exit codes, and a support bundle for bug reports.
- [Checking every setup](operations/doctor-all.md): `lore doctor --all`, every setup check with its fix, and `lore setup repair`.
- [The audit log](operations/audit-log.md): who changed what, and a check that nobody changed it behind lore's back.
- [A damaged cache](operations/backup-restore-repair.md): rebuild the local cache and the search index from git.
- [Secrets and personal data](operations/secrets-and-pii.md): why passwords are refused, your own patterns, and the safety switches.
- [Use lore in CI](operations/ci.md): read-only mode, quiet hints, and failing a job on a bad health check.
- [Your settings and team rules](operations/plugins-and-db-config.md): `lore config` and `lore policy`.
- [Resources: servers, domains and the services on them](operations/resources.md): `lore resource`, typed rows for your infrastructure, what depends on what, import and export, vault references instead of secrets.
- [Upgrade lore](operations/upgrade.md): `lore upgrade`, version pins for the team and CI, and the daily new-release line.

### Lore for teams

- [Lore for teams](team/README.md): who should read what, and the reading order.
- [Overview](team/overview.md): what lore adds for a team.
- [Concepts](team/concepts.md): rows, live rows, signals, landing, stores, orgs and identity, one idea at a time.
- [Getting started](team/getting-started.md): a first live task, a first landing, a first org.
- [How it works](team/how-it-works.md): what lore does in git behind each command.
- [Your identity, joining an org, and store housekeeping](team/identity-and-stores.md): your email as your identity, `lore org import`, moving a note, tidying caches.
- [Guided setup](team/guided-setup.md): `lore setup` / `lore onboard`, the local setup page (accounts found on this machine, fields with suggestions, import from an existing lore install, the current-folder line, Repair from Home), several setups on one machine chosen by folder, and `lore setup join`.
- [Setups from the command line](team/setup-cli.md): every `lore setup` subcommand with real output: list, show, use, edit, disable/enable, remove/trash, plan/check/apply, import-scan/import, join.
- [Team invites](team/invites.md): `lore team invite`, list, resend and revoke; single use, 14 days, `--grant`.
- [Client folders from the parent folder](team/client-folders.md): `lore store folder add|list` from a folder that is not a repo, and which store commands need one.
- [Team profiles](team/profiles.md): people, contacts, contact types, company periods, project members and roles, offboarding, orgs and machine labels.
- [Setup and invite errors](team/setup-errors.md): every `E_SETUP_*`, `E_INVITE_*` and fix error with its cause and fix.
- [Agents](team/agents.md): the loop an agent follows, and how a person takes work back.
- [Commands reference](team/commands.md): every team command, setting and error.
- [Troubleshooting](team/troubleshooting.md): each error message, its cause and its fix.
- [Real-repo trial](team/real-repo-trial.md): a maintainers' check that plays an agency and its client on two real repos and confirms nothing internal reached the client.
- Use cases: [Solo developer](team/use-cases/solo.md), [Small team in one repo](team/use-cases/small-team.md), [Agency with a client repo](team/use-cases/agency-client-repo.md), [Several repos as one product](team/use-cases/group-project.md), [Same store, separate staging](team/use-cases/staging.md), [Contractor limited to one client](team/use-cases/contractor.md), [One person in two companies](team/use-cases/two-companies.md), [Personal notes](team/use-cases/personal-notes.md), [Agents on one mission](team/use-cases/agents-mission.md), [Sharing an internal note with the client](team/use-cases/sharing-a-note.md), [Cleaning up after a leak](team/use-cases/leak-cleanup.md).

### Decision guides

- [Decision guides](choosing/README.md): the overview, and a good default for most people.
- [Where your notes live](choosing/database-mode.md): in each repo (Mode A), a folder store on your machine, or a store.
- [Keep a note with the code, or share it at once](choosing/with-code-or-live.md): `--no-live` or `--live`.
- [How to lay out your stores](choosing/store-layout.md): one store, one per client, or a mix.
- [Which org and identity setup](choosing/org-and-identity.md): no config, one org, joining, two companies.
- [Which file your coding tool reads](choosing/render-targets.md): `CLAUDE.md`, `AGENTS.md`, private context outside the repo.
- [When to use agents, claims and gates](choosing/agents-claims-gates.md): from one agent alone to many agents on one plan.
- [Which options for which kind of team](choosing/by-team-shape.md): find the row that looks like your team and take its settings.
- [Which command for which job](choosing/workflow-methods.md): one pick for each everyday job when lore has two or three ways to do it.
- [Which storage backend and cache](choosing/backend.md): where the note files and their caches land, and why a cloud-sync folder is refused.

### Reference

- [Reference](reference/README.md): every command, flag, config key, environment variable and error code, in tables.
- [Feature index](reference/features.md): every feature with its code, guide and runnable example.
- [Command index](reference/commands.md): every command with its one-line help.
- [Flags](reference/flags.md): flags shared by many commands.
- [Config files](reference/config.md): `~/.lore/config.yaml` and the other settings files, every key.
- [Environment variables](reference/environment.md): settings for one shell or one command.
- [Error codes and exit statuses](reference/errors.md): every code, its hint and its exit status.
- [Local REST API](reference/rest-api.md): `lore api`, its token, routes and errors; `lore spec` for maintainers.
- [Generated command and error reference](reference/cli-commands.md): every command and error code from the operation registry, written by `lore spec docs`.

### For contributors and agent tools

- [Development](DEVELOPMENT.md): build, test and release lore.
- [Migration prompt](MIGRATE_TO_LORE.md): a prompt that moves an existing repo onto lore.
- [The lore skill](../skills/README.md): the instructions a coding tool loads to use lore, with its [command list](../skills/COMMANDS.md), [error list](../skills/ERRORS.md) and [walkthroughs](../skills/examples/01-bootstrap.md).
- [Docs examples](../tests/docs/README.md): how the examples on these pages are run and pasted.
- Design specs: [LORE_TEAM_SPEC.md](../LORE_TEAM_SPEC.md) (teams, stores, live rows, agents) and [LORE_SYNC_SPEC.md](../LORE_SYNC_SPEC.md) (notes in git).
