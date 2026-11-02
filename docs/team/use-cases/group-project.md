# Several repos as one product

## The situation

Globex's product is three repos: `globex/globex-api` (backend), `globex/globex-web` (frontend) and `globex/globex-proxy`. Northwind plans one feature, "Invite landing page", that needs work in all three. Each repo keeps its own lore, but the plan, its tasks and what the team learns along the way should be one thing that every repo shows.

## The config

The same `northwind` org entry as in [Agency with a client repo](agency-client-repo.md#the-config); `repos:` already matches `github.com/globex/*`. The shared work lives in one internal project, `globex/`, in Northwind's store. A project that several repos link to is called a **group**.

## The commands

Link each repo to the same folder (once per repo, by anyone at Northwind):

<!-- example: group#link-three-repos only=1,2,3 -->
```console
# alice, in globex-api
$ lore org link northwind --project globex --yes
This marks github.com/globex/globex-api as a CLIENT repo of northwind: internal notes default to github.com/northwind/lore globex/,
and the leak guards turn on for everyone in northwind.
  link: project prj_01a1…59ff at github.com/globex/globex-api → github.com/northwind/lore@main:globex/ (default=false)
✓ github.com/globex/globex-api (prj_01a1…59ff, github.com/globex/globex-api) linked to github.com/northwind/lore globex/ (created; pushed)
  nothing was written to this repo

# alice, in globex-web
$ lore org link northwind --project globex --yes
This marks github.com/globex/globex-web as a CLIENT repo of northwind: internal notes default to github.com/northwind/lore globex/,
and the leak guards turn on for everyone in northwind.
  link: project prj_01a1…bee0 at github.com/globex/globex-web → github.com/northwind/lore@main:globex/ (default=false)
✓ github.com/globex/globex-web (prj_01a1…bee0, github.com/globex/globex-web) linked to github.com/northwind/lore globex/ (pushed)
  nothing was written to this repo

# alice, in globex-proxy
$ lore org link northwind --project globex --yes
This marks github.com/globex/globex-proxy as a CLIENT repo of northwind: internal notes default to github.com/northwind/lore globex/,
and the leak guards turn on for everyone in northwind.
  link: project prj_01a1…ca8b at github.com/globex/globex-proxy → github.com/northwind/lore@main:globex/ (default=false)
✓ github.com/globex/globex-proxy (prj_01a1…ca8b, github.com/globex/globex-proxy) linked to github.com/northwind/lore globex/ (pushed)
  nothing was written to this repo
```
<!-- /example -->

The first link created `globex/` in the store; the others joined it. Now a mission and its tasks, each saying which repo it is for, with a dependency between them. `--key` makes each command safe to run twice:

<!-- example: group#mission-across-repos only=1,2,3,4 -->
```console
# alice, in globex-api
$ lore mission add 'Invite landing page' --to northwind/globex --key invite
✓ msn_01a1…db8b msn_01a1…db8b — Invite landing page → [northwind/globex]
lore live: (live, pushed)
$ lore task add 'invite mutation + email' --to northwind/globex --mission msn_01a1…db8b --repo globex-api --key invite/backend
✓ tsk_01a1…8bbe tsk_01a1…8bbe — invite mutation + email [medium] → [northwind/globex]
lore live: (live, pushed)
$ lore task add 'invite landing route' --to northwind/globex --mission msn_01a1…db8b --repo globex-web --after tsk_01a1…8bbe --key invite/frontend
✓ tsk_01a1…04c8 tsk_01a1…04c8 — invite landing route [medium] → [northwind/globex]
lore live: (live, pushed)
$ lore task add 'invite mutation + email' --to northwind/globex --mission msn_01a1…db8b --repo globex-api --key invite/backend
✓ tsk_01a1…8bbe tsk_01a1…8bbe — invite mutation + email (already added with --key invite/backend)
```
<!-- /example -->

Any of the three repos shows the group's tasks, and `lore mission status` shows the whole plan, including why a task waits:

<!-- example: group#mission-across-repos only=5,7 -->
```console
# alice, in globex-web
$ lore task list
(no tasks)
tsk_01a1…8bbe  invite mutation + email  todo  [northwind/globex]
tsk_01a1…04c8  invite landing route  todo  [northwind/globex]

# alice, in globex-proxy
$ lore mission status msn_01a1…db8b
msn_01a1…db8b Invite landing page   2 tasks · 0 done · 0 claimed · 1 ready · 1 waiting · 0 gated · 0 expired · 0 blocked
  waiting   tsk_01a1…04c8  invite landing route                      waits for tsk_01a1…8bbe to be merged (todo)
  ready     tsk_01a1…8bbe  invite mutation + email                   
```
<!-- /example -->

The landing route waits for the backend task to be **merged**, not just done, because it is for another repo: the frontend should start against backend code that is really on `main`. See [Done, then merged](../how-it-works.md#done-then-merged).

## What happened

```text
 github.com/globex/globex-api   ─┐
 github.com/globex/globex-web   ─┼─ link ──▶ northwind/lore  globex/.lore/data/_meta.json  links: [api, web, proxy]
 github.com/globex/globex-proxy ─┘                           refs/lore/live/main  globex/missions/…, globex/tasks/…
                                                             (land on the store's main when finished)
 the Globex repos themselves gain nothing but the code
```

## Gotchas

- **`--repo` takes the repo's lore name** (the name given to `lore init --name`, usually the repo name). A task with no repo is offered only to callers that pass no `--repo`, or a matching `--role`.
- **Every repo lists every group task.** `lore task list` in `globex-web` shows the backend task too; use the mission status, or `lore task next --repo globex-web`, to see only what is ready for one repo.
- **Same key, same row.** Running the `task add … --key invite/backend` command again prints "already added with --key invite/backend" and changes nothing. A whole plan can be applied from a file with `lore plan apply`; see [AI agents on one mission](agents-mission.md).
- Nothing about the plan is visible to Globex: it is all in Northwind's store.
