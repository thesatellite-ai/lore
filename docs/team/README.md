# Lore for teams

Lore keeps a project's shared memory (rules, decisions, notes) and its shared work (tasks, missions) as small files in git. These pages explain how lore works when more than one person, more than one repo, more than one company or a crew of AI agents use it together, and how to set each case up.

You do not need to read the design spec to use any of this. Every command example on these pages was run for real (see [How these examples were made](#how-these-examples-were-made)).

## Who should read what

| You are… | Start with | Then |
|---|---|---|
| a developer who wants tasks to reach teammates at once | [Getting started](getting-started.md) | [Small team in one repo](use-cases/small-team.md) |
| a team lead setting up an agency or company | [Overview](overview.md) | [Concepts](concepts.md), [Agency with a client repo](use-cases/agency-client-repo.md), [Contractor limited to one client](use-cases/contractor.md) |
| someone working for two companies on one laptop | [One person in two companies](use-cases/two-companies.md) | [Personal notes](use-cases/personal-notes.md) |
| running AI agents on a shared plan | [Agents](agents.md) | [AI agents on one mission](use-cases/agents-mission.md) |
| curious how it works inside | [How it works](how-it-works.md) | [Concepts](concepts.md) |
| stuck on an error | [Troubleshooting](troubleshooting.md) | [Commands reference](commands.md) |

## Reading order

1. [Overview](overview.md): what problems this solves, the whole picture in one diagram, and a five-minute mental model.
2. [Concepts](concepts.md): rows, live rows, signals, landing, stores, links, orgs and identity, explained one at a time.
3. [Getting started](getting-started.md): install, first config, first live task, first landing.
4. One or two [use cases](#use-cases) close to your situation.
5. [How it works](how-it-works.md) when you want to know what lore does in git for each command.
6. [Commands reference](commands.md), [Agents](agents.md) and [Troubleshooting](troubleshooting.md) as you need them.

## Use cases

Each page follows the same order: the situation, the config, the commands with their real output, a small diagram of what happened, and the gotchas.

| Page | Situation |
|---|---|
| [Solo developer](use-cases/solo.md) | one person, one repo; lore behaves as it always did |
| [Small team in one repo](use-cases/small-team.md) | tasks reach teammates on any branch, at once |
| [Agency with a client repo](use-cases/agency-client-repo.md) | internal notes and tasks the client never sees |
| [Several repos as one product](use-cases/group-project.md) | one mission across backend, frontend and proxy repos |
| [Same store, separate staging](use-cases/staging.md) | a test lore that never mixes with the real one |
| [Contractor limited to one client](use-cases/contractor.md) | one store per client, and access through GitHub permissions |
| [One person in two companies](use-cases/two-companies.md) | two identities, two rosters, never mixed |
| [Personal notes](use-cases/personal-notes.md) | notes only you see, on one machine or synced |
| [AI agents on one mission](use-cases/agents-mission.md) | plan, claims, a crash and a reclaim, a human gate, done and merged |
| [Sharing an internal note with the client](use-cases/sharing-a-note.md) | the one deliberate way internal text reaches a client |
| [Cleaning up after a leak](use-cases/leak-cleanup.md) | an internal row reached the client's repo; remove it everywhere |

## How these examples were made

`tests/team/docs/run-examples.sh` builds lore from this repo and runs every example on these pages in a throwaway world: a fake GitHub made of bare git repos, one home folder per person, nothing on the network. It writes what each command printed to `tests/team/docs/transcripts/`, and fails when a command that should work fails, or a command shown as an error succeeds. `tests/team/docs/paste.py` then copies that output into the pages between `<!-- example: … -->` markers, so the pages always show what the current code prints.

Two things are shortened for reading: row ids (`tsk_01a19d82ec2f7ba5968210b0efbee6fe` is shown as `tsk_01a1…e6fe`) and the few lines every fresh clone prints once while it builds its local cache. Paths look like `/home/alice/work/acme-app` because each person in the examples has their own home folder.

All names are made up: the agency Northwind (store `northwind/lore`), its clients Acme, Globex and Initech, and the people alice, dev1, dev2 and dev3.
