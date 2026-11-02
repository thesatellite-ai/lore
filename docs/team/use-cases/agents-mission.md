# AI agents on one mission

## The situation

Alice owns the Globex feature "Invite landing page" across three repos (see [Several repos as one product](group-project.md)). She runs a coordinator agent and three workers in tmux on the `devbox` server: `backend-1` in `globex-api`, `frontend-1` in `globex-web`, `proxy-1` in `globex-proxy`. They must not take the same task twice, a crashed agent's work must be picked up rather than lost, the deploy must wait for Alice to say go, and anyone must be able to see where the mission stands.

Everything below lives in Northwind's store (`globex/`): the plan rows on the store's live ref, the claims on its signals ref. The Globex repos gain nothing but code.

This page shows one real run. [Agents](../agents.md) explains the loop an agent should follow and what to do on each error.

## The config

The `northwind` org entry from [Agency with a client repo](agency-client-repo.md#the-config); the three repos linked to `globex/`; Alice registered with `lore actor register`. Agents run on Alice's machine under her HOME, each in its own clone, each as its own actor.

In this run the clock was moved forward on purpose (lore's test build reads a fixed time from `LORE_NOW`), so the lease expiry below takes one step instead of twenty minutes. Nothing else differs from a normal run.

## 1. The plan

The coordinator writes the plan once. A plan file names the mission, its approvers and its tasks with repo, dependencies, time box, role and gate:

<!-- example: agents#plan only=1 -->
```console
# alice, in globex-api
$ cat invite.yaml
mission: {key: invite, title: "Invite landing page", to: northwind/globex}
approvers: ["@alice"]
tasks:
  - {key: backend,  title: "invite mutation + email",    repo: globex-api,   timebox: 60m}
  - {key: frontend, title: "invite landing route",       repo: globex-web,   after: [backend], timebox: 60m}
  - {key: proxy,    title: "allow /invite on the proxy", repo: globex-proxy}
  - {key: deploy,   title: "deploy to staging",          role: coordinator, after: [frontend, proxy], gate: required}
```
<!-- /example -->

`lore agent start` registers an agent actor for this session (one actor per label, owned by a person) and prints the environment the session must export. `lore plan apply` creates the mission and every task in one write; applying it again finds them by their keys and adds nothing. The coordinator then holds a claim on the mission itself, so a second coordinator would be told the mission is taken:

<!-- example: agents#plan only=2,3,4,5 -->
```console
# alice, in globex-api
$ lore agent start --label coord --owner @alice
✓ act_7a67…9504 agent coord (owner @alice) registered in northwind org/; LORE_ACTOR=agent:coord LORE_SESSION=s-10e90368
export LORE_ACTOR=agent:coord LORE_SESSION=s-10e90368
lore: [northwind/org] 1 file(s) pushed

# coord, in globex-api
$ lore plan apply invite.yaml
✓ msn_2ad3…f891 Invite landing page (created) → [northwind/globex]
  tsk_162d…9666 invite mutation + email       timebox 1h
  tsk_54fe…28f2 invite landing route          timebox 1h  after backend
  tsk_05e9…4603 allow /invite on the proxy  
  tsk_c0f3…b5b8 deploy to staging             after frontend,proxy  gate required
$ lore plan apply invite.yaml
✓ msn_2ad3…f891 Invite landing page (found) → [northwind/globex]
  tsk_162d…9666 invite mutation + email       timebox 1h
  tsk_54fe…28f2 invite landing route          timebox 1h  after backend
  tsk_05e9…4603 allow /invite on the proxy  
  tsk_c0f3…b5b8 deploy to staging             after frontend,proxy  gate required
$ lore task claim msn_2ad3…f891
✓ claimed msn_2ad3…f891 (lease 20m, token ct_a5f6f…) on refs/lore/signals/main
```
<!-- /example -->

## 2. Workers start the same way

Each worker registers, reads its inbox and asks for the next ready task of its repo. `frontend-1` gets nothing yet, with the reason and exit status 10 (`E_NO_WORK`), because its task waits for the backend task to be merged:

<!-- example: agents#workers-start -->
```console
# alice, in agents/globex-api
$ lore agent start --label backend-1 --owner @alice
✓ act_76bd…3f10 agent backend-1 (owner @alice) registered in northwind org/; LORE_ACTOR=agent:backend-1 LORE_SESSION=s-d5ea7c9e
export LORE_ACTOR=agent:backend-1 LORE_SESSION=s-d5ea7c9e
lore: [northwind/org] 1 file(s) pushed

# backend-1, in agents/globex-api
$ lore inbox
(inbox empty)
$ lore task next --mission msn_2ad3…f891 --repo globex-api
✓ claimed tsk_162d…9666 invite mutation + email (lease 20m, token ct_cb04f…) on refs/lore/signals/main
  read its notes: lore task show tsk_162d…9666

# alice, in agents/globex-web
$ lore agent start --label frontend-1 --owner @alice
✓ act_d431…313a agent frontend-1 (owner @alice) registered in northwind org/; LORE_ACTOR=agent:frontend-1 LORE_SESSION=s-004ed4ce
export LORE_ACTOR=agent:frontend-1 LORE_SESSION=s-004ed4ce
lore: [northwind/org] 1 file(s) pushed

# frontend-1, in agents/globex-web
$ lore task next --mission msn_2ad3…f891 --repo globex-web
ERROR: [E_NO_WORK] nothing ready:
  tsk_54fe…28f2 invite landing route: waits for tsk_162d…9666 to be merged (claimed by agent:backend-1)
(exit 10)

# alice, in agents/globex-proxy
$ lore agent start --label proxy-1 --owner @alice
✓ act_b4f4…a5b0 agent proxy-1 (owner @alice) registered in northwind org/; LORE_ACTOR=agent:proxy-1 LORE_SESSION=s-ba4ad92d
export LORE_ACTOR=agent:proxy-1 LORE_SESSION=s-ba4ad92d
lore: [northwind/org] 1 file(s) pushed

# proxy-1, in agents/globex-proxy
$ lore task next --mission msn_2ad3…f891 --repo globex-proxy --owns nginx/sites/invite.conf
✓ claimed tsk_05e9…4603 allow /invite on the proxy (lease 20m, token ct_87f6f…) on refs/lore/signals/main
  read its notes: lore task show tsk_05e9…4603
```
<!-- /example -->

## 3. A finding every agent sees

`proxy-1` learns something the others need. A finding is a memory pinned to the mission; it shows up in every inbox:

<!-- example: agents#finding -->
```console
# proxy-1, in agents/globex-proxy
$ lore finding add --mission msn_2ad3…f891 'the proxy strips X-Forwarded-Host; links must use the public base URL'
✓ mem_28f8…03df finding on msn_2ad3…f891 (live)
lore live: (live, pushed)
lore live: landed on main (601d285); leaves live once main is fetched

# backend-1, in agents/globex-api
$ lore inbox
msn_2ad3…f891  finding  proxy-1      the proxy strips X-Forwarded-Host; links must use the public base URL

# proxy-1, in agents/globex-proxy
$ lore task done tsk_05e9…4603
✓ tsk_05e9…4603 done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: landed on main (86dbae4); leaves live once main is fetched
lore live: 7 row(s) left live (main holds them)
```
<!-- /example -->

## 4. Done, then merged

`backend-1` leaves a note for whoever comes next, commits on its own branch, and finishes with the commit and a lesson. `frontend-1` still waits: done is not merged. When Alice merges the branch, the next lore command in that repo finds the commit on `main` (by ancestry), and `frontend-1` gets its task:

<!-- example: agents#done-then-merged -->
```console
# backend-1, in agents/globex-api
$ lore task note tsk_162d…9666 'mutation inviteUser(email) returns a token; email template invite.html'
✓ note cmt_4c59…6c38 on tsk_162d…9666
lore live: landed on main (9b40ce7); leaves live once main is fetched
lore live: 1 row(s) left live (main holds them)
$ git switch -q -c agent/backend-1/invite && mkdir -p api && echo 'package api' > api/invite.go && git add api && git commit -qm 'invite mutation' && git push -q -u origin agent/backend-1/invite
$ lore task done tsk_162d…9666 --commit $(git rev-parse HEAD) --lesson 'invite tokens expire in 48h, documented in the README'
✓ tsk_162d…9666 done (commit 05513aa); waiting for merge
  lesson → mem_3b62…e913
lore live: landed on main (dfd8dc8); leaves live once main is fetched
lore live: 1 row(s) left live (main holds them)
lore: [northwind/globex] 2 file(s) pushed

# frontend-1, in agents/globex-web
$ lore task next --mission msn_2ad3…f891 --repo globex-web
ERROR: [E_NO_WORK] nothing ready:
  tsk_54fe…28f2 invite landing route: waits for tsk_162d…9666 to be merged (done, 05513aa not on the default branch yet)
(exit 10)

# alice, in globex-api
$ git pull -q && git merge -q --no-ff --no-edit origin/agent/backend-1/invite && git push -q origin main
$ lore task list
✓ tsk_162d…9666 merged 10:23 (found by: ancestry, cf563f3)
lore live: landed on main (df3a55c); leaves live once main is fetched
tsk_162d…9666  invite mutation + email  done  [northwind/globex]
tsk_54fe…28f2  invite landing route  todo  [northwind/globex]
tsk_05e9…4603  allow /invite on the proxy  done  [northwind/globex]
tsk_c0f3…b5b8  deploy to staging  todo  [northwind/globex]

# frontend-1, in agents/globex-web
$ lore task next --mission msn_2ad3…f891 --repo globex-web
✓ claimed tsk_54fe…28f2 invite landing route (lease 20m, token ct_dfebf…) on refs/lore/signals/main
  read its notes: lore task show tsk_54fe…28f2
lore live: 1 row(s) left live (main holds them)
```
<!-- /example -->

## 5. An agent crashes; its work is picked up

`frontend-1` commits part of the work and records a note, which also pushes its work branch. Then its session dies. Twenty-five minutes later (the lease is 20 minutes, plus 2 minutes of grace) the coordinator's inbox shows the expired claim and where the work is:

<!-- example: agents#crash-and-reclaim only=1,2,3,4 -->
```console
# frontend-1, in agents/globex-web
$ git switch -q -c agent/frontend-1/invite-route && mkdir -p src/routes && echo 'export {}' > src/routes/invite.tsx && git add src && git commit -qm 'route + loader'
$ lore task note tsk_54fe…28f2 'route + loader done, form next'
✓ note cmt_f147…5139 on tsk_54fe…28f2
  work branch agent/frontend-1/invite-route pushed
lore live: landed on main (6d00426); leaves live once main is fetched

# coord, in globex-api
$ lore inbox
msn_2ad3…f891  EXPIRED  frontend-1   tsk_54fe…28f2 claimed by frontend-1, lease expired 8 min ago; work branch pushed (agent/frontend-1/invite-route)
msn_2ad3…f891  finding  proxy-1      the proxy strips X-Forwarded-Host; links must use the public base URL
$ lore mission status msn_2ad3…f891
msn_2ad3…f891 Invite landing page   4 tasks · 2 done · 0 claimed · 0 ready · 1 waiting · 0 gated · 1 expired · 0 blocked
  done      tsk_05e9…4603  allow /invite on the proxy   proxy-1      not merged yet 4m
  done      tsk_162d…9666  invite mutation + email      backend-1    merged 10:23 12m (timebox 1h)
  expired   tsk_54fe…28f2  invite landing route         frontend-1   work branch agent/frontend-1/invite-route 30m (timebox 1h)
  waiting   tsk_c0f3…b5b8  deploy to staging                         waits for tsk_54fe…28f2 to be merged (in_progress)
```
<!-- /example -->

A replacement agent, `frontend-2`, takes the task over (attempt 2), fetches the pushed work branch and continues. If the crashed `frontend-1` wakes up and tries to finish, it is fenced out with `E_LOST_CLAIM` (exit 12):

<!-- example: agents#crash-and-reclaim only=5,6,7,8,9 -->
```console
# alice, in agents/fe2/globex-web
$ lore agent start --label frontend-2 --owner @alice
✓ act_2537…6bbb agent frontend-2 (owner @alice) registered in northwind org/; LORE_ACTOR=agent:frontend-2 LORE_SESSION=s-04c3d965
export LORE_ACTOR=agent:frontend-2 LORE_SESSION=s-04c3d965
lore: [northwind/org] 1 file(s) pushed

# frontend-2, in agents/fe2/globex-web
$ lore task next --mission msn_2ad3…f891 --repo globex-web
✓ claimed tsk_54fe…28f2 invite landing route (lease 20m, token ct_d57ca…) on refs/lore/signals/main; reclaim, attempt 2; previous holder frontend-1 timed out
  read its notes: lore task show tsk_54fe…28f2
lore live: 1 row(s) left live (main holds them)
$ git fetch -q origin agent/frontend-1/invite-route && git log --oneline -1 FETCH_HEAD
34ee210 route + loader

# frontend-1, in agents/globex-web
$ lore task done tsk_54fe…28f2
ERROR: [E_LOST_CLAIM] tsk_54fe…28f2 is held by agent:frontend-2 since 10:57AM (your claim is not on the signals ref); your token ct_dfebf… expired 10:45AM
  hint: stop work on this task; `lore task next` finds other work
(exit 12)

# frontend-2, in agents/fe2/globex-web
$ git switch -q -c agent/frontend-2/invite-route FETCH_HEAD && echo form >> src/routes/invite.tsx && git commit -qam 'invite form' && git push -q -u origin agent/frontend-2/invite-route && lore task done tsk_54fe…28f2 --commit $(git rev-parse HEAD)
✓ tsk_54fe…28f2 done (commit 7898daa); waiting for merge
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: landed on main (ccfb0bb); leaves live once main is fetched
```
<!-- /example -->

## 6. Merged by hand, then the gate

The deploy task waits for every dependency to be merged. `proxy-1` finished without naming a commit, so lore cannot see its merge ("merge unknown"); Alice marks it by hand:

<!-- example: agents#merged-by-hand -->
```console
# coord, in globex-api
$ lore task next --mission msn_2ad3…f891 --role coordinator
ERROR: [E_NO_WORK] nothing ready:
  tsk_c0f3…b5b8 deploy to staging: waits for tsk_05e9…4603 to be merged (done, merge unknown)
(exit 10)

# alice, in globex-proxy
$ lore task merged tsk_05e9…4603
✓ tsk_05e9…4603 merged (marked by hand, 11:04)
lore live: landed on main (51886a5); leaves live once main is fetched
lore live: 1 row(s) left live (main holds them)
```
<!-- /example -->

Now the deploy task is ready except for its gate. The coordinator stops there. An agent session cannot approve, even when it runs on Alice's machine: approval comes from a person's own identity, never from `LORE_ACTOR`. Alice approves from her own shell, and the coordinator claims the task:

<!-- example: agents#gate -->
```console
# coord, in globex-api
$ lore task next --mission msn_2ad3…f891 --role coordinator
ERROR: [E_NO_WORK] nothing ready:
  tsk_c0f3…b5b8 deploy to staging: gate needs approval (lore gate approve tsk_c0f3…b5b8)
(exit 10)
$ lore gate approve tsk_c0f3…b5b8
ERROR: [E_INVALID_INPUT] approvals come from your identity file, never from LORE_ACTOR (LORE_ACTOR is set here)
  hint: an agent session cannot approve; run `lore gate approve` in your own shell
(exit 1)

# alice, in globex-api
$ lore gate approve tsk_c0f3…b5b8
✓ tsk_c0f3…b5b8 approved by Alice Doe (org entry (config.yaml)), hash 22efc3b3…
lore live: 1 row(s) left live (main holds them)

# coord, in globex-api
$ lore task next --mission msn_2ad3…f891 --role coordinator
✓ claimed tsk_c0f3…b5b8 deploy to staging (lease 20m, token ct_3de87…) on refs/lore/signals/main
  read its notes: lore task show tsk_c0f3…b5b8
```
<!-- /example -->

## 7. The whole mission at a glance

From any of the Globex repos:

<!-- example: agents#mission-status -->
```console
# alice, in globex-proxy
$ lore mission status msn_2ad3…f891
msn_2ad3…f891 Invite landing page   4 tasks · 3 done · 1 claimed · 0 ready · 0 waiting · 0 gated · 0 expired · 0 blocked
  claimed   tsk_c0f3…b5b8  deploy to staging            coord        working (until 11:29) 1m
  done      tsk_05e9…4603  allow /invite on the proxy   proxy-1      merged 11:04 4m
  done      tsk_54fe…28f2  invite landing route         frontend-2   merged 11:02 24m (timebox 1h; frontend-1 20m + frontend-2 4m)
  done      tsk_162d…9666  invite mutation + email      backend-1    merged 10:23 12m (timebox 1h)
$ lore task show tsk_c0f3…b5b8 --from northwind/globex
tsk_c0f3…b5b8 tsk_c0f3…b5b8
  title:    deploy to staging
  status:   in_progress
  priority: medium
  commit:   accepted
  mission:  msn_2ad3…f891
  role:     coordinator
  after:    tsk_54fe…28f2 (until merged), tsk_05e9…4603 (until merged)
  gate:     approved
  attempts: 1
  source:   northwind/globex
  claim:    active, held by coord (session s-10e90368, devbox)
  token:    ct_3de87…  epoch 1
  expires:  2026-11-02T11:29:20Z  renewed_seq 0
$ lore task show tsk_54fe…28f2
tsk_54fe…28f2 tsk_54fe…28f2
  title:    invite landing route
  status:   done
  priority: medium
  commit:   accepted
  mission:  msn_2ad3…f891
  repo:     globex-web
  after:    tsk_162d…9666 (until merged)
  timebox:  1h
  attempts: 2
  branch:   agent/frontend-1/invite-route
  done by:  frontend-2
  commit:   8c373dc3944d
  merged:   2026-11-02T11:02:20Z
  source:   northwind/globex
  notes:
    2026-11-02 10:28  route + loader done, form next  (frontend-1)
  claim:    none on refs/lore/signals/main
```
<!-- /example -->

## What happened

```text
 northwind/lore (store)                                   who wrote it
   refs/lore/live/main   globex/missions/…, tasks/…      coord (plan apply), workers (notes, done)
                         globex/memories/… (finding, lessons)
   refs/heads/main       globex/.lore/data/…              landings: every finished task, with done_by,
                                                          done_commit, merged_at, claim log
   refs/lore/signals/main globex/claims/<task>.json       one commit, replaced on every claim and heartbeat

 github.com/globex/globex-web
   agent/frontend-1/invite-route   pushed by `lore task note`, fetched by frontend-2
   main                            the merged code
```

## Gotchas

- **Keep the lease alive.** A long test run must not let the lease expire: run `lore task heartbeat --daemon <id>` in the background (or call `lore task heartbeat` from your agent tool's hook). A heartbeat says "alive", not "progress"; record progress with `lore task note`.
- **Push work before you might die.** `lore task note` pushes the claim's work branch; a replacement can fetch it. Work never pushed is lost with the session, and `lore mission status` says so.
- **Done is not merged.** A task in another repo waits for **merged**. Pass `--commit <sha>` or `--pr <n>` to `lore task done`, or lore cannot see the merge (`merge unknown`, `lore task merged <id>` to mark it by hand). With `merge_check.adapter: github`, lore asks `gh` about pull requests.
- **Claims fail closed.** Offline, a claim, heartbeat, release, done on a claimed task or an approval fails with `E_OFFLINE` (exit 15); nothing is queued to happen later.
- **Speed and scale.** A claim is a git round trip (a second or two); others see changes on their next fetch. Ten to twenty agents per store branch are fine.
- **Task ids from the store** are found from any linked repo by every task command (`task show`, `task next`, `task done`, `mission status`); `--from northwind/globex` picks the source when an id exists in two places.
