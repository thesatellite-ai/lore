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
| [Run log](run-log.md) | a step-by-step diary of one try at a piece of work, and how to replay it |

## Where to start

New to lore? Set up a repo with [Your first project](../knowledge/first-project.md), then read [Tasks](tasks.md). Every example on these pages is a real run of lore, made by `tests/docs/run-examples.sh` (sections `tasks`, `missions` and `runs`).

## Working with other people and agents

Some task features only make sense with more than one person: assigning a task to Bob (an agency developer, `@bob`), an agent claiming a task so nobody else takes it, `lore task next` (lore picks the next free task for you), heartbeats (an agent saying "I am still working"), and gates that wait for a person's approval. They are on the team pages:

- [Agents](../team/agents.md): the loop an agent follows (claim, heartbeat, note, done).
- [Team commands › Tasks and missions (team flags)](../team/commands.md#tasks-and-missions-team-flags) and [Agent coordination](../team/commands.md#agent-coordination).
- [Use case › Agents on a mission](../team/use-cases/agents-mission.md) and [Use case › Small team](../team/use-cases/small-team.md).
