# Overview

**In short:** lore keeps your project's notes and to-dos as small files in git. For a team it adds three things. Your teammates see a new task at once, whatever branch they are on. Private notes can live in a separate repo the client cannot open. And several coding agents can share one plan without doing the same task twice.

Lore stores what your project knows and what it has to do as rows. A row is one note: a rule ("Run the tests before you push"), a decision ("We use Stripe for payments"), a memory ("The client replies faster on WhatsApp than email"), a task ("Send the invoice reminder before Friday's demo") or a mission (a bigger goal made of tasks). Each row is one small JSON file under `.lore/data/` in your repo. So git already shares it: you commit it with your code, it travels on your branch, and it reaches `main` when the branch merges.

That works well for one person. For a team it leaves gaps. Lore for teams fills them.

## The problems it solves

| Problem | Without lore for teams | With lore for teams |
|---|---|---|
| **Branches.** Bob (an agency developer) adds a task for Alice (another agency developer) while both are on feature branches. | Alice sees it only after Bob's branch merges into `main`. | Tasks are **live** (a live list every teammate sees at once, whatever branch they are on): Alice sees it on her next lore command. |
| **Private notes.** You keep notes about the client that the client must never read, but the client can read the repo. | The client can read anything in the repo, hidden or not. | Internal notes live in a **store**: a separate private repo only the agency can open, like a locked filing cabinet next to the client's repo. |
| **Many repos.** Client 2's product is spread over three repos: app, API and infra. | Each repo has its own lore. Nothing ties them together. | The repos **link** to one internal project. A mission and its tasks span all three. |
| **Many companies.** You work for two companies on one laptop. | One identity everywhere, notes mixed together. | Each **org** (one company's setup) has its own identity, store and roster (the list of people and agents in it). A repo loads exactly one org. |
| **Agents.** Several coding agents work on one plan. | Two agents take the same task. A crashed agent's task is stuck. Nobody sees the whole plan. | An agent **claims** a task, like writing its name on a whiteboard job. The claim has a **lease**: it runs out after a set time unless the agent keeps saying it is alive, like a parking ticket. An expired lease frees the task. `lore mission status` shows everything. |

## The whole picture

```text
 github.com/client/website  (the client's repo: the client can read ALL of this)
 ┌──────────────────────────────────────────────────────────────────────┐
 │ branches  main, feat/login, feat/checkout                            │
 │           .lore/data/ on each branch  ── rows that travel with code  │
 │           .lore/data/ on main         ── the durable home of a row   │
 │ refs/lore/live     ── live rows, shared at once, whatever branch     │
 │ refs/lore/signals  ── claims (client repos use the store's instead)  │
 └──────────────────────────────────────────────────────────────────────┘
          ▲ link (stored only in the store, never in the client repo)
          │
 github.com/agency/notes  (the agency's store: the client cannot read it)
 ┌──────────────────────────────────────────────────────────────────────┐
 │ lore-store.yaml        marker: "this repo is a lore store"           │
 │ org/.lore/data/        company rules, the roster of people and agents│
 │ client/.lore/data/     internal notes and tasks about the client     │
 │ client2/.lore/data/    one project shared by three client 2 repos    │
 │ refs/lore/live/main    live rows of every folder                     │
 │ refs/lore/signals/main claims, leases, heartbeats of agents          │
 └──────────────────────────────────────────────────────────────────────┘

 your machine
 ┌──────────────────────────────────────────────────────────────────────┐
 │ ~/.lore/config.yaml    your orgs, your identity in each, your stores │
 │ ~/.lore/personal/      your personal notes: everywhere/, agency/     │
 │ ~/.lore/stores/        one git clone per store (<owner>/<repo>)      │
 │ ~/.lore/render/…md     internal context for agents, OUTSIDE the repo │
 └──────────────────────────────────────────────────────────────────────┘
```

Who sees what:

| Place | Who can read it |
|---|---|
| a client repo's branches, `.lore/data`, `refs/lore/live`, `refs/lore/signals` | everyone who can clone the client repo, the client included |
| the store (`agency/notes`) | only people GitHub lets read that repo: the agency |
| `~/.lore/personal`, or your own private personal-store repo | only you |
| `~/.lore/config.yaml`, `~/.lore/render`, `~/.lore/cache` | only your machine |

## The five-minute mental model

1. **A row is a file.** Every task, rule or note is one JSON file. The copy in `main`'s `.lore/data` is its lasting home. On other branches, rows behave like code.
2. **Live is a whiteboard in the hallway.** Some rows are written to a hidden list on GitHub that is not a branch (git calls it a *ref*: `refs/lore/live`). Tasks, missions, handoffs and comments go there by default, and any row you add with `--live`. Every clone fetches that list, so everyone sees these rows at once, from any branch. The live copy wins over any branch copy.
3. **Landing files the whiteboard into the archive.** When a live row is finished, lore writes it to `main`'s `.lore/data` by itself. This is called landing. Finished means: a task is done or cancelled, a decision is superseded or rejected; rules, memories and notes count as finished on write. Once `main` holds the same content, lore wipes the row off live. If `main` is protected, the row rides along in your next push as a small extra commit.
4. **Signals are sticky notes by the door.** "`agent-backend` is working on this task until 14:20" changes every few minutes and matters only now. So it lives on another hidden list, `refs/lore/signals`. That list always holds exactly one commit and never builds history.
5. **Privacy is decided by which repo a row is in.** A hidden ref is hidden, not private: anyone who can clone a repo can download every ref in it. So internal notes never go into the client's repo at all. They go into a **store**, a separate repo only your company can read.
6. **One org per repo.** Your `~/.lore/config.yaml` lists your orgs (the companies you work for). In each repo lore picks exactly one, by a link in a store or by the repo's URL. It loads that org's identity, store and roster, plus your personal notes for that org. `lore whoami` tells you which one and why.
7. **Agents claim, renew and finish.** An agent takes a task with `lore task next`. It keeps its lease alive with heartbeats (small "I am still here" messages). It finishes with `lore task done`. If it crashes, the lease runs out and another agent takes the task over. The crashed one can no longer write to it (lore calls this fencing).

## What stays the same

If you work alone in one repo, nothing changes unless you want it to. No config is needed, and every command works as before. One thing is new: tasks are live by default, so with a remote they are pushed to `refs/lore/live` at once. See [Solo developer](use-cases/solo.md) for how to keep the old behaviour.

## Next

- [Concepts](concepts.md) explains each idea above with a diagram.
- [Getting started](getting-started.md) walks through a first setup with real output.

## Pitfalls

- **Hidden is not private.** Anyone who can clone a repo can download everything in it, on any branch or hidden list. Private notes must live in a repo the other side cannot open.
- **Live is temporary.** Live rows are copied to `main` when they are finished, or after 7 days (`live.max_age`) if they are still open. A ZIP download, a fork or a shallow clone carries only what is on `main`.
- **Your identity key is your email.** Keep it the same on every machine.

## Related

- [Concepts](concepts.md): each idea explained on its own.
- [Getting started](getting-started.md): your first live task.
- [Use cases](README.md#use-cases): pick the one closest to your team.
- [Guided setup](guided-setup.md): setting up a machine for one or more teams.
