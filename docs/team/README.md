# Lore for teams

**In short:** lore keeps your project's notes and to-do list as small files in git, so everyone on the team sees the same ones. These pages show how that works when several people, several repos, a client or a few agents (programs that write code) share one project. They also show how to keep the agency's private notes away from the client.

Lore keeps two things as small files in git: a project's shared memory (rules like "Run the tests before you push", decisions like "We use Stripe for payments", notes) and its shared work (tasks like "Send the invoice reminder before Friday's demo", and missions, which are groups of tasks). These pages explain how to set lore up when more than one person, repo, company or agent uses it.

You do not need to read the design spec to use any of this. Every command example on these pages was run for real (see [How these examples were made](#how-these-examples-were-made)).

## Who should read what

| You are… | Start with | Then |
|---|---|---|
| a developer who wants tasks to reach teammates at once | [Getting started](getting-started.md) | [Small team in one repo](use-cases/small-team.md) |
| a team lead setting up an agency or company | [Overview](overview.md) | [Concepts](concepts.md), [Agency with a client repo](use-cases/agency-client-repo.md), [Contractor limited to one client](use-cases/contractor.md) |
| someone working for two companies on one laptop | [One person in two companies](use-cases/two-companies.md) | [Personal notes](use-cases/personal-notes.md) |
| running agents on a shared plan | [Agents](agents.md) | [Agents on one mission](use-cases/agents-mission.md) |
| curious how it works inside | [How it works](how-it-works.md) | [Concepts](concepts.md) |
| setting your identity, joining an org, tidying stores | [Identity and stores](identity-and-stores.md) | [Getting started](getting-started.md#7-a-first-config-with-an-org) |
| stuck on an error | [Troubleshooting](troubleshooting.md) | [Commands reference](commands.md) |

## Reading order

1. [Overview](overview.md): what problems this solves, the whole picture in one diagram, and a five-minute mental model.
2. [Concepts](concepts.md): rows, live rows, signals, landing, stores, links, orgs and identity, explained one at a time.
3. [Getting started](getting-started.md): install, first config, first live task, first landing.
4. One or two [use cases](#use-cases) close to your situation.
5. [How it works](how-it-works.md) when you want to know what lore does in git for each command.
6. [Commands reference](commands.md), [Agents](agents.md), [Identity and stores](identity-and-stores.md) and [Troubleshooting](troubleshooting.md) as you need them.

## Use cases

Each page follows the same order: the situation, the config, the commands with their real output, a small diagram of what happened, and the pitfalls.

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
| [Agents on one mission](use-cases/agents-mission.md) | plan, claims, a crash and a reclaim, a human gate, done and merged |
| [Sharing an internal note with the client](use-cases/sharing-a-note.md) | the one deliberate way internal text reaches a client |
| [Cleaning up after a leak](use-cases/leak-cleanup.md) | an internal row reached the client's repo; remove it everywhere |

## A first taste

Two developers, one repo, two branches. Bob (an agency developer) gives Alice (another agency developer) a task. Alice sees it on her next command, without any merge (from [Small team in one repo](use-cases/small-team.md)):

<!-- example: small-team#assign-across-branches only=1,3 -->
```console
# bob, in billing
$ git pull -q && git switch -q -c feat/payments && lore task add 'check the checkout page on a phone' --assign @alice
✓ tsk_162d…9666 — check the checkout page on a phone [medium]
lore live: (live, pushed)
lore live: 1 row(s) left live (main holds them)

# alice, in billing
$ lore sync && lore task list --mine
· already in sync
○ tsk_162d…9666 check the checkout page on a phone [medium]
```
<!-- /example -->

## Pitfalls

- **Hidden is not private.** Anything in the client's repo, on any branch or hidden list, the client can download. A hidden list is a place on GitHub that is not a branch (git calls it a ref). Private notes belong in the agency's store: a separate private repo that only the agency can open, like a locked filing cabinet next to the client's repo.
- **Lore fetches once per command and reuses that fetch for 60 seconds.** A teammate's change from a few seconds ago may not show yet. `lore sync` always fetches.
- **Your identity key is your email.** Keep it the same on every machine, or you become two people.
- **A gate needs a person.** A gate is a step that waits for a person to say yes. Any person on the roster (the list of people and agents in an org) may approve it; an agent never can.

## How these examples were made

`tests/team/docs/run-examples.sh` builds lore from this repo and runs every example on these pages in a throwaway world: a fake GitHub made of plain git repos on disk, one home folder per person, nothing on the network. It writes what each command printed to `tests/team/docs/transcripts/`. It fails when a command that should work fails, or when a command shown as an error succeeds. `tests/team/docs/paste.py` then copies that output into the pages between `<!-- example: … -->` markers, so the pages always show what the current code prints.

The same kind of story also runs against two real repos on a real git host, by hand, before a release: see [Real-repo trial](real-repo-trial.md).

Two things are shortened for reading: row ids (`tsk_01a19d82ec2f7ba5968210b0efbee6fe` is shown as `tsk_01a1…e6fe`) and the few lines every fresh clone prints once while it builds its local cache. Paths look like `~/work/website`: each person in the examples has their own home folder, and `~` stands for it.

All names are role words, not real companies: **the agency** (org `agency`, private store `github.com/agency/notes`), **the client** (`github.com/client/website`), **client 2** (three repos, `github.com/client2/app`, `client2/api` and `client2/infra`, that make one product) and **client 3**. The people are Alice and Bob (agency developers) and Carol (the client's developer); the agents are `agent-backend`, `agent-frontend` and friends.
