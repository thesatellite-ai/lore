# Which command for which job

**In short:** lore often has two or three ways to keep the same kind of thing. This page picks one for each everyday job: which kind of note to write, which kind of to-do to use, how a note moves between branches and people, how git merges notes, and whether to use the command line, the local HTTP API or the terminal browser. When in doubt: a rule for "always/never", a memory for anything you learned, a task for anything you will do.

## Capturing knowledge: which kind of note

| You want to keep | Use | Why this kind | Pinned into the agent file? |
|---|---|---|---|
| an "always" or "never" every session must follow | `lore rule add` (`--severity must`, the default) | rules are the hard lines; `should` and `may` rules are found by search only | yes, `must` rules |
| a fact you learned ("The client replies faster on WhatsApp") | `lore memory add` | the catch-all; `lore memory invalidate` marks it no longer true | no, found by `lore search` |
| a choice and its reason ("We use Stripe for payments") | `lore decision add` | keeps the alternatives and the reasoning; a newer decision supersedes it | no |
| a loud warning about something broken right now | `lore hotfix add` (`--severity high`, the default) | shown until you archive it | yes, `critical` and `high` |
| steps you repeat ("How to release") | `lore playbook add` | a recipe others can follow; `lore search --tables playbooks` finds it | no |
| text you paste into a tool again and again | `lore prompt add` | kept word for word | no |
| what broke and what you did | `lore incident add` | `--ref` points it at the rows it touched | no |
| one value many notes mention (a host name) | `lore vars set` and `${var:<name>}` | change it in one place | no |
| extra fields on any row (`client=acme`) | `lore meta set` | filter, export and check them | no |
| servers, domains and services with typed fields | `lore resource add --kind <kind> --name <name>` | values are checked, refs between rows are followed | no |

The other kinds (pattern, snapshot, behaviour, architecture note, cookbook recipe, suggestion, taste preference, tech doc, workflow, workspace) work the same way; pick the one whose name says what the note is for, so `lore search --tables <kind>` finds it.

- **If you are not sure,** write a memory. Turning it into a rule later is `lore rule add` plus `lore memory delete`, and nothing is lost if you never do.
- **If a note must reach every coding session,** it has to be a `must` rule or a `critical`/`high` hotfix; everything else waits until the agent searches for it (see [Which file your coding tool reads](render-targets.md)).
- **If a note holds a password or a token,** lore refuses it; store a reference to your vault instead ([Secrets and personal data](../operations/secrets-and-pii.md)).

## Planning work: task, mission, plan or reminder

| You have | Use | What it gives you |
|---|---|---|
| one thing to do | `lore task add` | status, priority, `--due`, `--defer-until` (snooze), `--assign`; live by default, so teammates see it at once |
| a thing too big for one task | `lore task add --parent <id>` (sub-tasks) or a checklist inside the task | a tree you can check with the task tools |
| work you do the same way each time | a task template (`--template`) | the same tasks and fields each time |
| work that comes back on a schedule | `lore task add --recur 'every month on 1'` | the next one is made when you finish this one |
| a date that only needs to tell you ("Renew the domain on 1 June") | `lore reminder add --due` (`--recurrence 1y` to repeat) | an overdue mark in `lore reminder list`, no status to manage |
| a bigger goal made of tasks | `lore mission add`, tasks with `--mission <id>` | `lore mission status`: ready, waiting, gated, claimed, blocked, done |
| a written note on how you will get there | `lore plan add` | prose; tasks can point at it with `--plan` |
| a list of tasks with their order, in a file | `lore mission apply --file <file>` | one mission and its tasks from a YAML plan file; a re-run adds nothing |
| an idea, not yet agreed | `lore task add --commitment proposed` (or `someday`) | `lore task triage` lists it until someone says yes |

- **If only a date matters,** use a reminder; if someone has to do something, use a task.
- **If more than one person or agent works on the same goal,** use a mission, so `lore task next --mission <id>` hands out its tasks in order (see [When to use agents, claims and gates](agents-claims-gates.md)).

## Moving notes between branches and people

| You want | Use |
|---|---|
| a note reviewed and merged with the change it belongs to | write it on your branch (`--no-live`, the default for every kind but tasks, missions, handoffs and comments) |
| everyone to see a note now, whatever branch they are on | `--live` (see [Keep a note with the code, or share it at once](with-code-or-live.md)) |
| to read the notes on a teammate's branch without switching | `lore sync peek --ref <branch>` |
| one note from your branch on `main` before the pull request merges | `lore sync promote --ids <id> --to main` |
| a finished live note copied onto `main` now | `lore land` |
| a note out of the repo into a store, keeping its id | `lore row move --id <id> --to <store>` |

## Merging notes: hooks and merge driver, or the CI merge

| Where the merge happens | What does it | Set up by |
|---|---|---|
| on your machine (`git pull`, `git merge`, `git rebase`) | the merge driver: it merges two edits of the same note field by field; hooks check note files before a commit and read new notes in after a pull | `lore init` and your first lore command in a clone, by themselves |
| on GitHub (the merge button) | `lore sync ci-merge`, run by the workflow `lore sync install-action` adds: it merges `main` into the pull request with lore and pushes the result | `lore sync install-action`, run by hand by default; `--auto` runs it on every pull request |

- **If everyone merges on their own machine,** you need nothing more: the merge driver is already in place.
- **If people merge with the GitHub button,** install the action, and keep it manual until you know you want it to spend GitHub Actions minutes on every pull request.
- **If a merge left a conflict lore could not settle,** `lore sync conflicts` lists it and `lore sync resolve --id <id>` settles it.

## Talking to lore: command line, local API or terminal browser

| Interface | Use it when | Writes? |
|---|---|---|
| the `lore` command line | you, a script or a coding agent with a shell | yes |
| `lore context fetch` (`--for-file`, `--for-task`, `--topic`) | an agent needs the notes for one file or task, not the whole set | no |
| `lore api` (local REST) | a program can speak HTTP but cannot start a command: an editor plugin, a local dashboard | yes, with the same checks and errors as the command; it listens on `127.0.0.1` only and needs the token in `~/.lore/api-token` |
| `lore tui` | you want to browse every table in the terminal | no, read-only |
| `lore sql run` | a question no list command answers | no, one `SELECT` on the cache |

## Pitfalls

- **A `should` rule is not pinned.** If an agent keeps ignoring a rule, check its severity before you rewrite it.
- **A reminder has no assignee or status;** a task does. Do not use reminders as a to-do list for a team.
- **`lore sync promote` never pushes.** It makes a commit on your local `main`; push it, or open a pull request when `main` is protected.
- **`lore api` is only for this machine.** Never put it behind a proxy or share its token; anyone with the token writes as you.
- **`lore tui` changes nothing.** Use the `add` and `edit` commands to write.

## Guides and examples for each choice

| Choice | Guide | Example |
|---|---|---|
| Rules, memories, decisions | [Rules, memories and decisions](../knowledge/rules-memories-decisions.md) | [`knowledge`](../../tests/docs/sections/knowledge.sh) ([output](../../tests/docs/transcripts/knowledge.txt)) |
| Hotfixes and the other kinds | [Other kinds of notes](../knowledge/other-row-kinds.md) | [`knowledge`](../../tests/docs/sections/knowledge.sh) ([output](../../tests/docs/transcripts/knowledge.txt)) |
| Variables and metadata | [Variables and metadata](../knowledge/variables-and-metadata.md) | [`meta`](../../tests/docs/sections/meta.sh) ([output](../../tests/docs/transcripts/meta.txt)) |
| Resources | [Resources](../operations/resources.md) | [`resources`](../../tests/docs/sections/resources.sh) ([output](../../tests/docs/transcripts/resources.txt)) |
| Tasks | [Tasks](../tasks/tasks.md), [Commitment and snooze](../tasks/commitment-and-snooze.md) | [`tasks`](../../tests/docs/sections/tasks.sh) ([output](../../tests/docs/transcripts/tasks.txt)) |
| Sub-tasks, checklists, templates, repeating tasks | [Task tools](../tasks/task-tools.md) | [`task-tools`](../../tests/docs/sections/task-tools.sh) ([output](../../tests/docs/transcripts/task-tools.txt)) |
| Reminders | [Reminders](../tasks/reminders.md) | [`tasks`](../../tests/docs/sections/tasks.sh) ([output](../../tests/docs/transcripts/tasks.txt)) |
| Missions, plans, plan files | [Missions and plans](../tasks/missions-and-plans.md) | [`missions`](../../tests/docs/sections/missions.sh) ([output](../../tests/docs/transcripts/missions.txt)) |
| Peek and promote | [Branches: peek and promote](../sync/branches-peek-promote.md) | [`sync`](../../tests/docs/sections/sync.sh) ([output](../../tests/docs/transcripts/sync.txt)) |
| Moving a note to a store | [Your stores](../sync/stores.md) | [`stores`](../../tests/docs/sections/stores.sh) ([output](../../tests/docs/transcripts/stores.txt)) |
| Hooks and merge driver | [Hooks and the merge driver](../sync/hooks-and-merge-driver.md), [Conflicts and duplicates](../sync/conflicts-and-duplicates.md) | [`sync`](../../tests/docs/sections/sync.sh) ([output](../../tests/docs/transcripts/sync.txt)) |
| CI merge on GitHub | [GitHub workflow](../sync/github-workflow.md) | [`sync`](../../tests/docs/sections/sync.sh) ([output](../../tests/docs/transcripts/sync.txt)) |
| Local REST API | [Local REST API](../reference/rest-api.md) | [`api`](../../tests/docs/sections/api.sh) ([output](../../tests/docs/transcripts/api.txt)) |
| Terminal browser | [The terminal browser](../knowledge/tui.md) | none: it needs a terminal |
| Search and `lore sql` | [Search](../knowledge/search.md), [Your first project](../knowledge/first-project.md) | [`search`](../../tests/docs/sections/search.sh) ([output](../../tests/docs/transcripts/search.txt)), [`start`](../../tests/docs/sections/start.sh) ([output](../../tests/docs/transcripts/start.txt)) |

## Related

- [Which options for which kind of team](by-team-shape.md)
- [Feature index](../reference/features.md): every feature with its guide and example.
- [All decision guides](README.md)
