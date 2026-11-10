# Several repos as one product

**In short:** client 2's product is three repos, but the plan for one feature should be one list, not three. The agency keeps that plan in one internal project that all three repos link to. So every repo shows the same mission and tasks.

## The situation

Client 2's product is three repos: `client2/api` (backend), `client2/app` (frontend) and `client2/infra`. The agency plans one feature, "Invite landing page", that needs work in all three. Each repo keeps its own lore. But the plan, its tasks and what the team learns along the way should be one thing that every repo shows.

## The config

The same `agency` org entry as in [Agency with a client repo](agency-client-repo.md#the-config). Its `repos:` already matches `github.com/client2/*`. The shared work lives in one internal project, `client2/`, in the agency's store (a separate private repo only the agency can open). A project that several repos link to is called a **group**.

## The commands

Link each repo to the same folder. Anyone at the agency can do it, once per repo. Here Alice (an agency developer) does it:

<!-- example: group#link-three-repos only=1,2,3 -->
```console
# alice, in api
$ lore org link agency --project client2 --yes
This marks github.com/client2/api as a CLIENT repo of agency: internal notes default to github.com/agency/notes client2/,
and the leak guards turn on for everyone in agency.
  link: project prj_7a67…9504 at github.com/client2/api → github.com/agency/notes@main:client2/ (default=false)
✓ github.com/client2/api (prj_7a67…9504, github.com/client2/api) linked to github.com/agency/notes client2/ (created; pushed)
  nothing was written to this repo

# alice, in app
$ lore org link agency --project client2 --yes
This marks github.com/client2/app as a CLIENT repo of agency: internal notes default to github.com/agency/notes client2/,
and the leak guards turn on for everyone in agency.
  link: project prj_2ad3…f891 at github.com/client2/app → github.com/agency/notes@main:client2/ (default=false)
✓ github.com/client2/app (prj_2ad3…f891, github.com/client2/app) linked to github.com/agency/notes client2/ (pushed)
  nothing was written to this repo

# alice, in infra
$ lore org link agency --project client2 --yes
This marks github.com/client2/infra as a CLIENT repo of agency: internal notes default to github.com/agency/notes client2/,
and the leak guards turn on for everyone in agency.
  link: project prj_162d…9666 at github.com/client2/infra → github.com/agency/notes@main:client2/ (default=false)
✓ github.com/client2/infra (prj_162d…9666, github.com/client2/infra) linked to github.com/agency/notes client2/ (pushed)
  nothing was written to this repo
```
<!-- /example -->

The first link created `client2/` in the store. The others joined it. Now add a mission (a bigger goal made of tasks) and its tasks. Each task says which repo it is for, and one task waits for the other. `--key` makes each command safe to run twice:

<!-- example: group#mission-across-repos only=1,2,3,4 -->
```console
# alice, in api
$ lore mission add 'Invite landing page' --to agency/client2 --key invite
✓ msn_05e9…4603 — Invite landing page → [agency/client2]
$ lore task add 'send the invite email from the API' --to agency/client2 --mission msn_05e9…4603 --repo api --key invite/backend
✓ tsk_c0f3…b5b8 — send the invite email from the API [medium] → [agency/client2]
$ lore task add 'build the invite page' --to agency/client2 --mission msn_05e9…4603 --repo app --after tsk_c0f3…b5b8 --key invite/frontend
✓ tsk_76bd…3f10 — build the invite page [medium] → [agency/client2]
$ lore task add 'send the invite email from the API' --to agency/client2 --mission msn_05e9…4603 --repo api --key invite/backend
✓ tsk_c0f3…b5b8 — send the invite email from the API (already added with --key invite/backend)
```
<!-- /example -->

Any of the three repos shows the group's tasks. `lore mission status` shows the whole plan, including why a task waits:

<!-- example: group#mission-across-repos only=5,7 -->
```console
# alice, in app
$ lore task list
○ tsk_c0f3…b5b8 send the invite email from the API [medium]  [agency/client2]
○ tsk_76bd…3f10 build the invite page [medium]  [agency/client2]

# alice, in infra
$ lore mission status msn_05e9…4603
msn_05e9…4603 Invite landing page   2 tasks · 0 done · 0 claimed · 1 ready · 1 waiting · 0 gated · 0 expired · 0 blocked
  ready     tsk_c0f3…b5b8  send the invite email from the API              
  waiting   tsk_76bd…3f10  build the invite page                     waits for tsk_c0f3…b5b8 to be merged (todo)
```
<!-- /example -->

The frontend task ("build the invite page") waits for the backend task to be **merged**, not just done. That is because it is for another repo: the frontend should start against backend code that is really on `main`. See [Done, then merged](../how-it-works.md#done-then-merged).

## What happened

```text
 github.com/client2/api   ─┐
 github.com/client2/app   ─┼─ link ──▶ agency/notes  client2/.lore/data/_meta.json  links: [api, app, infra]
 github.com/client2/infra ─┘                         refs/lore/live/main  client2/missions/…, client2/tasks/…
                                                                          (land on the store's main when finished)
 the client 2 repos themselves gain nothing but the code
```

## Pitfalls

- **`--repo` takes the repo's lore name**: the name given to `lore init --name`, usually the repo name. A task with no repo is offered only to callers that pass no `--repo`, or a matching `--role`.
- **Every repo lists every group task.** `lore task list` in `app` shows the backend task too. To see only what is ready for one repo, use the mission status, or `lore task next --repo app`.
- **Same key, same row.** Running the `task add … --key invite/backend` command again prints "already added with --key invite/backend" and changes nothing. You can apply a whole plan from a file with `lore mission apply`. See [Agents on one mission](agents-mission.md).
- Client 2 can see nothing about the plan. It is all in the agency's store.

## Related

- [Agents on one mission](agents-mission.md): the same kind of mission run by agents.
- [Agency with a client repo](agency-client-repo.md).
- [Concepts](../concepts.md): projects, links and stores.
