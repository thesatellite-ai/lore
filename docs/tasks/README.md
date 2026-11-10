# Tasks, missions and plans

**In short:** lore keeps your to-do list inside the repo, next to the code. A task is one thing to do, a mission is a bigger goal made of tasks, and a plan is how you will get there. These pages show how one person uses them; the team pages add other people and agents.

## Pages

| Page | What you learn |
|---|---|
| [Tasks](tasks.md) | add, list, start, finish, change, search, cancel and delete tasks; make one task wait for another; tasklists and saved views |
| [Commitment and snooze](commitment-and-snooze.md) | keep ideas (`someday`) and suggestions (`proposed`) out of your list, and hide a task until a date |
| [Missions and plans](missions-and-plans.md) | group tasks under a goal, see the whole goal at a glance, write plans, turn a plan file into tasks, leave a handoff and read your inbox |
| [Reminders](reminders.md) | dates you must not forget, once or on repeat |
| [Link commits](link-commits.md) | record which commit did a task, find the task behind a commit, and see when the change reached `main` |
| [Run log](run-log.md) | one timed try at a piece of work, the time it adds to its task, and time logged, fixed or stopped by hand |
| [Task tools](task-tools.md) | a board view, checklists inside a task, splitting a task, the sub-task tree check, relations, watching, history, templates, repeating tasks and the stale-task sweep |

## Where to start

New to lore? Set up a repo with [Your first project](../knowledge/first-project.md), then read [Tasks](tasks.md). Once the list grows, [Task tools](task-tools.md) helps keep it in shape, and [Commitment and snooze](commitment-and-snooze.md) keeps ideas and suggestions out of the way. Every example on these pages is a real run of lore, made by `tests/docs/run-examples.sh` (sections `tasks`, `task-tools`, `missions` and `runs`).

## Which page for which job

- **"What do I do today?"** `lore task list`, or `lore task board` for columns: [Tasks](tasks.md), [Task tools › Step 1](task-tools.md#step-1--see-the-tasks-as-a-board).
- **"This task is too big."** Split it into sub-tasks, or keep small steps as a checklist: [Task tools › Step 2](task-tools.md#step-2--keep-a-checklist-inside-a-task) and [Step 3](task-tools.md#step-3--split-a-task-into-sub-tasks).
- **"We do this every release."** Save it as a template: [Task tools › Step 8](task-tools.md#step-8--save-work-you-repeat-as-a-template).
- **"This comes back every week."** A repeating task: [Task tools › Step 9](task-tools.md#step-9--make-a-task-repeat). For a date that only needs to tell you, use a [reminder](reminders.md).
- **"An agent suggested this."** Accept or decline the proposal: [Commitment and snooze › Step 3](commitment-and-snooze.md#step-3--say-yes-to-a-proposal).
- **"How long did this take?"** Runs and time by hand: [Run log](run-log.md).
- **"Which task was this commit for?"** [Link commits](link-commits.md).
- **"Bring my backlog in from another tool."** [Tasks › Step 12](tasks.md#step-12--move-tasks-in-and-out-with-files).

## Working with other people and agents

Some task features only make sense with more than one person: assigning a task to Bob (an agency developer, `@bob`), an agent claiming a task so nobody else takes it, `lore task next` (lore picks the next free task for you), heartbeats (an agent saying "I am still working"), and gates that wait for a person's approval. They are on the team pages:

- [Agents](../team/agents.md): the loop an agent follows (claim, heartbeat, note, done).
- [Team commands › Tasks and missions (team flags)](../team/commands.md#tasks-and-missions-team-flags) and [Agent coordination](../team/commands.md#agent-coordination).
- [Use case › Agents on a mission](../team/use-cases/agents-mission.md) and [Use case › Small team](../team/use-cases/small-team.md).

## Related

- [Your first project](../knowledge/first-project.md): set up lore in a repo.
- [Rules, memories, decisions](../knowledge/rules-memories-decisions.md): keep what a task taught you; `task done --lesson` writes a memory.
- [How notes travel with git](../sync/how-notes-travel-with-git.md): how tasks reach the other clones and `main`.
- [Trash and purge](../sync/trash-and-purge.md): bring back a deleted task, or undo a bulk change.
- [Reference › Errors](../reference/errors.md#tasks): the task error codes.
- [Team commands](../team/commands.md): assignees, claims and the other team flags.
