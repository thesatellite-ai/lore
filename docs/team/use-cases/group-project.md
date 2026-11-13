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
$ lore org link --org agency --project client2 --yes
lore org link: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/api/.git/config; hook blocks in ~/work/api/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
lore: pushed store github.com/agency/notes: commit 601d285 to branch main
This marks github.com/client2/api as a CLIENT repo of agency: internal notes default to github.com/agency/notes client2/,
and the leak guards turn on for everyone in agency.
  link: project prj_7a67…9504 at github.com/client2/api → github.com/agency/notes@main:client2/ (default=false)
✓ github.com/client2/api (prj_7a67…9504) linked to github.com/agency/notes client2/ (created; pushed)
  nothing was written to this repo

# alice, in app
$ lore org link --org agency --project client2 --yes
lore org link: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/app/.git/config; hook blocks in ~/work/app/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
lore: pushed store github.com/agency/notes: commit 86dbae4 to branch main
This marks github.com/client2/app as a CLIENT repo of agency: internal notes default to github.com/agency/notes client2/,
and the leak guards turn on for everyone in agency.
  link: project prj_2ad3…f891 at github.com/client2/app → github.com/agency/notes@main:client2/ (default=false)
✓ github.com/client2/app (prj_2ad3…f891) linked to github.com/agency/notes client2/ (pushed)
  nothing was written to this repo

# alice, in infra
$ lore org link --org agency --project client2 --yes
lore org link: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/infra/.git/config; hook blocks in ~/work/infra/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
lore: pushed store github.com/agency/notes: commit 9b40ce7 to branch main
This marks github.com/client2/infra as a CLIENT repo of agency: internal notes default to github.com/agency/notes client2/,
and the leak guards turn on for everyone in agency.
  link: project prj_162d…9666 at github.com/client2/infra → github.com/agency/notes@main:client2/ (default=false)
✓ github.com/client2/infra (prj_162d…9666) linked to github.com/agency/notes client2/ (pushed)
  nothing was written to this repo
```
<!-- /example -->

The first link created `client2/` in the store. The others joined it. Now add a mission (a bigger goal made of tasks) and its tasks. Each task says which repo it is for, and one task waits for the other. `--key` makes each command safe to run twice:

<!-- example: group#mission-across-repos only=1,2,3,4 -->
```console
# alice, in api
$ lore mission add --title 'Invite landing page' --to agency/client2 --key invite
lore: pushed store agency: commit cf563f3 to branch main
lore: pushed store agency: commit df3a55c to branch main
lore: wrote 2 row file(s) in ~/.lore/personal/agency (store me)
lore: wrote 3 row file(s) in ~/.lore/stores/agency/notes/client2 (store agency)
✓ msn_c0f3…b5b8 — Invite landing page → [agency/client2]
$ lore task add --title 'send the invite email from the API' --to agency/client2 --mission msn_c0f3…b5b8 --repo api --key invite/backend
lore: pushed store agency: commit 6d00426 to branch main
lore: pushed store agency: commit 34ee210 to branch main
lore: wrote ~/.lore/stores/agency/notes/client2/repos/rep_76bd…3f10.json (store agency)
lore: wrote ~/.lore/stores/agency/notes/client2/task_lists/tlt_d431…313a.json (store agency)
lore: wrote ~/.lore/stores/agency/notes/client2/tasks/tsk_b4f4…a5b0.json (store agency)
✓ tsk_b4f4…a5b0 — send the invite email from the API [medium] → [agency/client2]
  created tasklist tlt_d431…313a "default" for tasks added without --tasklist
$ lore task add --title 'build the invite page' --to agency/client2 --mission msn_c0f3…b5b8 --repo app --after tsk_b4f4…a5b0 --key invite/frontend
lore: pushed store agency: commit 7898daa to branch main
lore: pushed store agency: commit ccfb0bb to branch main
lore: wrote ~/.lore/stores/agency/notes/client2/repos/rep_28f8…03df.json (store agency)
lore: wrote ~/.lore/stores/agency/notes/client2/tasks/tsk_4c59…6c38.json (store agency)
✓ tsk_4c59…6c38 — build the invite page [medium] → [agency/client2]
  on tasklist tlt_d431…313a "default" (the default; --tasklist picks another)
$ lore task add --title 'send the invite email from the API' --to agency/client2 --mission msn_c0f3…b5b8 --repo api --key invite/backend
✓ tsk_b4f4…a5b0 — send the invite email from the API (already added with --key invite/backend)
```
<!-- /example -->

Any of the three repos shows the group's tasks. `lore mission status` shows the whole plan, including why a task waits:

<!-- example: group#mission-across-repos only=5,7 -->
```console
# alice, in app
$ lore task list
○ tsk_b4f4…a5b0 send the invite email from the API [medium]  [agency/client2]
○ tsk_4c59…6c38 build the invite page [medium]  [agency/client2]

# alice, in infra
$ lore mission status --id msn_c0f3…b5b8
msn_c0f3…b5b8 Invite landing page   2 tasks · 0 done · 0 claimed · 1 ready · 1 waiting · 0 gated · 0 expired · 0 blocked
  ready     tsk_b4f4…a5b0  send the invite email from the API              
  waiting   tsk_4c59…6c38  build the invite page                     waits for tsk_b4f4…a5b0 to be merged (todo)
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
