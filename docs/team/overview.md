# Overview

Lore stores a project's knowledge and work as rows: a rule, a decision, a memory, a task, a mission. Each row is one small JSON file under `.lore/data/` in your repo, so git already shares it: you commit it with your code, it travels on your branch, and it reaches `main` when the branch merges.

That works well for one person. For a team it leaves gaps, and lore for teams fills them.

## The problems it solves

| Problem | Without lore for teams | With lore for teams |
|---|---|---|
| **Branches.** dev2 adds a task for dev1 while both are on feature branches. | dev1 sees it only after dev2's branch merges into `main`. | Tasks are **live**: dev1 sees it on the next lore command, whatever branch either person is on. |
| **Private notes.** You keep notes about a client that the client must never read, but the client can read the repo. | Anything in the repo is readable by the client, hidden or not. | Internal notes live in a **store**, a separate repo the client cannot read, linked to the client repo. |
| **Many repos.** One product is spread over a backend, a frontend and a proxy repo. | Each repo has its own lore; nothing ties them together. | The repos **link** to one internal project; a mission and its tasks span all three. |
| **Many companies.** You work for two companies on one laptop. | One identity everywhere, notes mixed together. | Each **org** has its own identity, store and roster; a repo loads exactly one org. |
| **Agents.** Several AI agents work one plan. | Two agents take the same task; a crashed agent's task is stuck; nobody sees the whole plan. | Agents **claim** tasks with a lease; an expired lease frees the task; `lore mission status` shows everything. |

## The whole picture

```text
 github.com/acme/app  (the client's repo: Acme can read ALL of this)
 ┌──────────────────────────────────────────────────────────────────────┐
 │ branches  main, feat/login, feat/checkout                            │
 │           .lore/data/ on each branch  ── rows that travel with code  │
 │           .lore/data/ on main         ── the durable home of a row   │
 │ refs/lore/live     ── live rows, shared at once, whatever branch     │
 │ refs/lore/signals  ── claims (client repos use the store's instead)  │
 └──────────────────────────────────────────────────────────────────────┘
          ▲ link (stored only in the store, never in the client repo)
          │
 github.com/northwind/lore  (Northwind's store: Acme cannot read it)
 ┌──────────────────────────────────────────────────────────────────────┐
 │ lore-store.yaml        marker: "this repo is a lore store"           │
 │ org/.lore/data/        company rules, the roster of people and agents│
 │ acme/.lore/data/       internal notes and tasks about Acme           │
 │ globex/.lore/data/     one project shared by three Globex repos      │
 │ refs/lore/live/main    live rows of every folder                     │
 │ refs/lore/signals/main claims, leases, heartbeats of agents          │
 └──────────────────────────────────────────────────────────────────────┘

 your machine
 ┌──────────────────────────────────────────────────────────────────────┐
 │ ~/.lore/config.yaml    your orgs, your identity in each, your stores │
 │ ~/.lore/personal/      your personal notes: everywhere/, northwind/  │
 │ ~/.lore/clones/        one bare clone per store                      │
 │ ~/.lore/render/…md     internal context for agents, OUTSIDE the repo │
 └──────────────────────────────────────────────────────────────────────┘
```

Who sees what:

| Place | Who can read it |
|---|---|
| a client repo's branches, `.lore/data`, `refs/lore/live`, `refs/lore/signals` | everyone who can clone the client repo, the client included |
| the store (`northwind/lore`) | only people GitHub lets read that repo: Northwind |
| `~/.lore/personal`, or your own private personal-store repo | only you |
| `~/.lore/config.yaml`, `~/.lore/render`, `~/.lore/cache` | only your machine |

## The five-minute mental model

1. **A row is a file.** Every task, rule or note is one JSON file. The copy in `main`'s `.lore/data` is its durable home. On other branches rows behave like code.
2. **Live is a whiteboard in the hallway.** Some rows (tasks, missions, handoffs, comments by default; anything with `--live`) are written to `refs/lore/live`, a hidden git ref that every clone fetches. Everyone sees them at once, from any branch, and the live copy wins over any branch copy.
3. **Landing files the whiteboard into the archive.** When a live row is finished (a task is done, a rule is written), lore writes it to `main`'s `.lore/data` by itself and wipes it off live once `main` holds the same content. If `main` is protected, the row rides along in your next push as a small extra commit.
4. **Signals are sticky notes by the door.** "Agent backend-1 is working on this task until 14:20" changes every few minutes and matters only now, so it lives on `refs/lore/signals`, which always holds exactly one commit and never builds history.
5. **Privacy is decided by which repo a row is in.** A hidden ref is hidden, not private: anyone who can clone a repo can fetch every ref in it. So internal notes never go into the client's repo at all; they go into a **store**, a separate repo only your company can read.
6. **One org per repo.** Your `~/.lore/config.yaml` lists your orgs (companies). In each repo lore picks exactly one, by a link in a store or by the repo's URL, and loads that org's identity, store and roster, plus your personal notes for that org. `lore whoami` tells you which one and why.
7. **Agents claim, renew and finish.** An agent takes a task with `lore task next`, keeps its lease alive with heartbeats, and finishes with `lore task done`. If it crashes, the lease runs out and another agent takes the task over; the crashed one can no longer write to it.

## What stays the same

Nothing changes for a single person in a single repo unless they want it to: no config is needed, and every command works as before. Tasks are live by default, which with a remote means they are pushed to `refs/lore/live` at once; see [Solo developer](use-cases/solo.md) for how to keep the old behaviour.

## Next

- [Concepts](concepts.md) explains each idea above with a diagram.
- [Getting started](getting-started.md) walks through a first setup with real output.
