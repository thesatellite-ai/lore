# Agents on one mission

**In short:** one person and four coding agents (programs that write code) work on one feature across three repos. Each agent takes a task and says "I'm on it", so no two agents do the same work. When one crashes, another picks its work up. A person approves the risky last step.

## The situation

Alice (an agency developer) owns the client 2 feature "Invite landing page" across three repos (see [Several repos as one product](group-project.md)). She runs a coordinator agent (it plans and hands out work) and three worker agents. They run in tmux (terminal windows that keep running) on the `devbox` server: `agent-backend` in `api`, `agent-frontend` in `app`, `agent-infra` in `infra`. Four things must hold. No two agents take the same task. A crashed agent's work is picked up, not lost. The deploy waits for Alice to say go. And anyone can see where the mission stands.

Everything below lives in the agency's store (`client2/`), the separate private repo only the agency can open. The plan rows sit on the store's live ref, a hidden list every teammate sees at once. The claims sit on its signals ref, a hidden list of short-lived facts. A claim is an agent putting its name on a task, like writing your name on a whiteboard job. The client 2 repos gain nothing but code.

This page shows one real run. [Agents](../agents.md) explains the loop an agent should follow and what to do on each error.

## The config

The `agency` org entry from [Agency with a client repo](agency-client-repo.md#the-config). The three repos are linked to `client2/`. Alice is registered with `lore actor register`. Agents run on Alice's machine under her HOME, each in its own clone, each as its own actor (its own entry on the roster, the list of people and agents).

Each claim has a lease: it runs out after a set time unless the agent keeps saying it is alive, like a parking ticket. In this run the clock was moved forward on purpose (lore's test build reads a fixed time from `LORE_NOW`). So the lease expiry below takes one step instead of twenty minutes. Nothing else differs from a normal run.

## 1. The plan

The coordinator writes the plan once. A plan file names the mission, its approvers and its tasks. Each task has a repo, dependencies, a time box, a role and a gate. A gate is a step that waits for a person to say yes. Any person on the roster may approve a gate, never an agent. `approvers:` narrows that to the people listed:

<!-- example: agents#plan only=1 -->
```console
# alice, in api
$ cat invite.yaml
mission: {key: invite, title: "Invite landing page", to: agency/client2}
approvers: ["@alice"]
tasks:
  - {key: backend,  title: "send the invite email from the API", repo: api,   timebox: 60m}
  - {key: frontend, title: "build the invite page",       repo: app,   after: [backend], timebox: 60m}
  - {key: proxy,    title: "allow /invite on the proxy", repo: infra}
  - {key: deploy,   title: "deploy to staging",          role: coordinator, after: [frontend, proxy], gate: required}
```
<!-- /example -->

`lore agent start` registers an agent actor for this session (one actor per label, owned by a person). It prints the environment the session must export. `lore mission apply` creates the mission and every task in one write. Applying it again finds them by their keys and adds nothing. The coordinator then holds a claim on the mission itself. So a second coordinator would be told the mission is taken:

<!-- example: agents#plan only=2,3,4,5 -->
```console
# alice, in api
$ lore agent start --label agent-coord --owner @alice
✓ act_7a67…9504 agent agent-coord (owner @alice) registered in agency org/; LORE_ACTOR=agent:agent-coord LORE_SESSION=s-10e90368
export LORE_ACTOR=agent:agent-coord LORE_SESSION=s-10e90368

# agent-coord, in api
$ lore mission apply invite.yaml
✓ msn_2ad3…f891 Invite landing page (created) → [agency/client2]
  tsk_162d…9666 send the invite email from the API  timebox 1h
  tsk_54fe…28f2 build the invite page         timebox 1h  after backend
  tsk_05e9…4603 allow /invite on the proxy
  tsk_c0f3…b5b8 deploy to staging             after frontend,proxy  gate required
$ lore mission apply invite.yaml
✓ msn_2ad3…f891 Invite landing page (found) → [agency/client2]
  tsk_162d…9666 send the invite email from the API  timebox 1h
  tsk_54fe…28f2 build the invite page         timebox 1h  after backend
  tsk_05e9…4603 allow /invite on the proxy
  tsk_c0f3…b5b8 deploy to staging             after frontend,proxy  gate required
$ lore task claim msn_2ad3…f891
✓ claimed msn_2ad3…f891 (lease 20m, token ct_a5f6f…) on refs/lore/signals/main
```
<!-- /example -->

## 2. Workers start the same way

Each worker registers, reads its inbox and asks for the next ready task of its repo. `agent-frontend` gets nothing yet, with the reason and exit status 10 (`E_NO_WORK`). Its task waits for the backend task to be merged, because the two tasks are in different repos. A plain `after:` waits for **merged** across repos and only for **done** within one repo (see the pitfall "Done is not merged"):

<!-- example: agents#workers-start -->
```console
# alice, in agents/api
$ lore agent start --label agent-backend --owner @alice
✓ act_76bd…3f10 agent agent-backend (owner @alice) registered in agency org/; LORE_ACTOR=agent:agent-backend LORE_SESSION=s-d5ea7c9e
export LORE_ACTOR=agent:agent-backend LORE_SESSION=s-d5ea7c9e

# agent-backend, in agents/api
$ lore inbox
(inbox empty)
$ lore task next --mission msn_2ad3…f891 --repo api
✓ claimed tsk_162d…9666 send the invite email from the API (lease 20m, token ct_cb04f…) on refs/lore/signals/main
  read its notes: lore task show tsk_162d…9666

# alice, in agents/app
$ lore agent start --label agent-frontend --owner @alice
✓ act_d431…313a agent agent-frontend (owner @alice) registered in agency org/; LORE_ACTOR=agent:agent-frontend LORE_SESSION=s-004ed4ce
export LORE_ACTOR=agent:agent-frontend LORE_SESSION=s-004ed4ce

# agent-frontend, in agents/app
$ lore task next --mission msn_2ad3…f891 --repo app
ERROR: [E_NO_WORK] nothing ready:
  tsk_54fe…28f2 build the invite page: waits for tsk_162d…9666 to be merged (claimed by agent:agent-backend)
(exit 10)

# alice, in agents/infra
$ lore agent start --label agent-infra --owner @alice
✓ act_b4f4…a5b0 agent agent-infra (owner @alice) registered in agency org/; LORE_ACTOR=agent:agent-infra LORE_SESSION=s-ba4ad92d
export LORE_ACTOR=agent:agent-infra LORE_SESSION=s-ba4ad92d

# agent-infra, in agents/infra
$ lore task next --mission msn_2ad3…f891 --repo infra --owns nginx/sites/invite.conf
✓ claimed tsk_05e9…4603 allow /invite on the proxy (lease 20m, token ct_87f6f…) on refs/lore/signals/main
  read its notes: lore task show tsk_05e9…4603
```
<!-- /example -->

## 3. A finding every agent sees

`agent-infra` learns something the others need. A finding is a memory pinned to the mission. It shows up in every inbox on that agent's next fetch. Lore reuses a fetch for 60 seconds (`live.fetch_ttl`), so a finding can take up to a minute to reach another agent's inbox; `lore sync` fetches at once (`lore inbox` has no refresh flag of its own):

<!-- example: agents#finding -->
```console
# agent-infra, in agents/infra
$ lore finding add --mission msn_2ad3…f891 'invite links must use the public website address, not the server name'
✓ mem_28f8…03df finding on msn_2ad3…f891 (live)

# agent-backend, in agents/api
$ lore inbox
(inbox empty)

# agent-infra, in agents/infra
$ lore task done tsk_05e9…4603
✓ tsk_05e9…4603 done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
```
<!-- /example -->

## 4. Done, then merged

`agent-backend` leaves a note for whoever comes next and commits on its own branch. It finishes with the commit and a lesson. `agent-frontend` still waits: its task is in another repo, so it waits for merged, and done is not merged. Alice merges the branch. The next lore command in that repo finds the commit on `main` (by ancestry: the commit is now part of `main`'s history). Then `agent-frontend` gets its task:

<!-- example: agents#done-then-merged -->
```console
# agent-backend, in agents/api
$ lore task note tsk_162d…9666 'the API sends the invite email and returns a link; the email text is in invite.html'
✓ note cmt_4c59…6c38 on tsk_162d…9666
$ git switch -q -c agent-backend/invite && mkdir -p api && echo 'package api' > api/invite.go && git add api && git commit -qm 'send the invite email' && git push -q -u origin agent-backend/invite
$ lore task done tsk_162d…9666 --commit $(git rev-parse HEAD) --lesson 'invite links stop working after 48 hours; the README says so'
✓ tsk_162d…9666 done (commit 601d285); waiting for merge
  lesson → mem_3b62…e913

# agent-frontend, in agents/app
$ lore task next --mission msn_2ad3…f891 --repo app
ERROR: [E_NO_WORK] nothing ready:
  tsk_54fe…28f2 build the invite page: waits for tsk_162d…9666 to be merged (done, 601d285 not on the default branch yet)
(exit 10)

# alice, in api
$ git pull -q && git merge -q --no-ff --no-edit origin/agent-backend/invite && git push -q origin main
$ lore task list
lore: tsk_162d…9666 merged 10:23 (found by: ancestry, 86dbae4) but it could not be recorded: write tsk_162d…9666: signals: push: gitremote: invalid request: destination "" is not a full ref; the next lore command retries
○ tsk_54fe…28f2 build the invite page [medium]  [agency/client2]
○ tsk_c0f3…b5b8 deploy to staging [medium]  [agency/client2]

# agent-frontend, in agents/app
$ lore task next --mission msn_2ad3…f891 --repo app
✓ claimed tsk_54fe…28f2 build the invite page (lease 20m, token ct_dfebf…) on refs/lore/signals/main
  read its notes: lore task show tsk_54fe…28f2
```
<!-- /example -->

## 5. An agent crashes; its work is picked up

`agent-frontend` commits part of the work and records a note, which also pushes its work branch. Then its session dies. Twenty-five minutes later (the lease is 20 minutes, plus 2 minutes of grace), the coordinator's inbox shows the expired claim and where the work is:

<!-- example: agents#crash-and-reclaim only=1,2,3,4 -->
```console
# agent-frontend, in agents/app
$ git switch -q -c agent-frontend/invite-route && mkdir -p src/routes && echo 'export {}' > src/routes/invite.tsx && git add src && git commit -qm 'invite page'
$ lore task note tsk_54fe…28f2 'route + loader done, form next'
✓ note cmt_f147…5139 on tsk_54fe…28f2
  work branch agent-frontend/invite-route pushed

# agent-coord, in api
$ lore inbox
msn_2ad3…f891  EXPIRED  agent-frontend tsk_54fe…28f2 claimed by agent-frontend, lease expired 8 min ago; work branch pushed (agent-frontend/invite-route)
$ lore mission status msn_2ad3…f891
msn_2ad3…f891 Invite landing page   4 tasks · 2 done · 0 claimed · 0 ready · 1 waiting · 0 gated · 1 expired · 0 blocked
  done      tsk_05e9…4603  allow /invite on the proxy   agent-infra  not merged yet 4m
  done      tsk_162d…9666  send the invite email from the API agent-backend merged 10:23 12m (timebox 1h)
  expired   tsk_54fe…28f2  build the invite page        agent-frontend work branch agent-frontend/invite-route 30m (timebox 1h)
  waiting   tsk_c0f3…b5b8  deploy to staging                         waits for tsk_54fe…28f2 to be merged (in_progress)
```
<!-- /example -->

A replacement agent, `agent-frontend-2`, takes the task over (attempt 2), fetches the pushed work branch and continues. The crashed `agent-frontend` may wake up and try to finish. Then it is fenced out with `E_LOST_CLAIM` (exit 12). Fencing means lore refuses a write from an agent whose claim already ran out:

<!-- example: agents#crash-and-reclaim only=5,6,7,8,9 -->
```console
# alice, in agents/fe2/app
$ lore agent start --label agent-frontend-2 --owner @alice
✓ act_2537…6bbb agent agent-frontend-2 (owner @alice) registered in agency org/; LORE_ACTOR=agent:agent-frontend-2 LORE_SESSION=s-04c3d965
export LORE_ACTOR=agent:agent-frontend-2 LORE_SESSION=s-04c3d965

# agent-frontend-2, in agents/fe2/app
$ lore task next --mission msn_2ad3…f891 --repo app
✓ claimed tsk_54fe…28f2 build the invite page (lease 20m, token ct_d57ca…) on refs/lore/signals/main; reclaim, attempt 2; previous holder agent-frontend timed out
  work branch agent-frontend/invite-route
  read its notes: lore task show tsk_54fe…28f2
$ git fetch -q origin agent-frontend/invite-route && git log --oneline -1 FETCH_HEAD
9b40ce7 invite page

# agent-frontend, in agents/app
$ lore task done tsk_54fe…28f2
ERROR: [E_LOST_CLAIM] tsk_54fe…28f2 is held by agent:agent-frontend-2 since 10:57AM (your claim is not on the signals ref); your token ct_dfebf… expired 10:45AM
  hint: stop work on this task; `lore task next` finds other work
(exit 12)

# agent-frontend-2, in agents/fe2/app
$ git switch -q -c agent-frontend-2/invite-route FETCH_HEAD && echo form >> src/routes/invite.tsx && git commit -qam 'invite form' && git push -q -u origin agent-frontend-2/invite-route && lore task done tsk_54fe…28f2 --commit $(git rev-parse HEAD)
✓ tsk_54fe…28f2 done (commit 05513aa); waiting for merge
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
```
<!-- /example -->

## 6. Merged by hand, then the gate

The deploy task waits for every task it depends on to be merged. `agent-infra` finished without naming a commit, so lore cannot see its merge ("merge unknown"). Alice marks it by hand:

<!-- example: agents#merged-by-hand -->
```console
# agent-coord, in api
$ lore task next --mission msn_2ad3…f891 --role coordinator
ERROR: [E_NO_WORK] nothing ready:
  tsk_c0f3…b5b8 deploy to staging: waits for tsk_05e9…4603 to be merged (done, merge unknown)
(exit 10)

# alice, in infra
$ lore task merged tsk_05e9…4603
✓ tsk_05e9…4603 merged (marked by hand, 11:04)
```
<!-- /example -->

Now the deploy task is ready except for its gate. The coordinator stops there. An agent session cannot approve, even when it runs on Alice's machine. Approval comes from a person's own identity, never from `LORE_ACTOR`. Alice approves from her own shell, and the coordinator claims the task:

<!-- example: agents#gate -->
```console
# agent-coord, in api
$ lore task next --mission msn_2ad3…f891 --role coordinator
ERROR: [E_NO_WORK] nothing ready:
  tsk_c0f3…b5b8 deploy to staging: gate needs approval (lore gate approve tsk_c0f3…b5b8)
(exit 10)
$ lore gate approve tsk_c0f3…b5b8
ERROR: [E_INVALID_INPUT] approvals come from your identity file, never from LORE_ACTOR (LORE_ACTOR is set here)
  hint: an agent session cannot approve; run `lore gate approve` in your own shell
(exit 20)

# alice, in api
$ lore gate approve tsk_c0f3…b5b8
✓ tsk_c0f3…b5b8 approved by Alice Doe (org entry (config.yaml)), hash cf563f34…

# agent-coord, in api
$ lore task next --mission msn_2ad3…f891 --role coordinator
✓ claimed tsk_c0f3…b5b8 deploy to staging (lease 20m, token ct_3de87…) on refs/lore/signals/main
  read its notes: lore task show tsk_c0f3…b5b8
```
<!-- /example -->

## 7. The whole mission at a glance

From any of the client 2 repos:

<!-- example: agents#mission-status -->
```console
# alice, in infra
$ lore mission status msn_2ad3…f891
msn_2ad3…f891 Invite landing page   4 tasks · 3 done · 1 claimed · 0 ready · 0 waiting · 0 gated · 0 expired · 0 blocked
  claimed   tsk_c0f3…b5b8  deploy to staging            agent-coord  working (until 11:29) 1m
  done      tsk_05e9…4603  allow /invite on the proxy   agent-infra  merged 11:04 4m
  done      tsk_54fe…28f2  build the invite page        agent-frontend-2 merged 11:02 24m (timebox 1h; agent-frontend 20m + agent-frontend-2 4m)
  done      tsk_162d…9666  send the invite email from the API agent-backend merged 10:23 12m (timebox 1h)
$ lore task show tsk_c0f3…b5b8 --from agency/client2
tsk_c0f3…b5b8
  title:    deploy to staging
  status:   in_progress
  priority: medium
  commit:   accepted
  mission:  msn_2ad3…f891
  role:     coordinator
  after:    tsk_54fe…28f2 (until merged), tsk_05e9…4603 (until merged)
  gate:     approved
  attempts: 1
  source:   agency/client2
  claim:    active, held by agent-coord (session s-10e90368, devbox)
  token:    ct_3de87…  epoch 1
  expires:  2026-11-02T11:29:20Z  renewed_seq 0
$ lore task show tsk_54fe…28f2
tsk_54fe…28f2
  title:    build the invite page
  status:   done
  priority: medium
  commit:   accepted
  mission:  msn_2ad3…f891
  done by:  agent-frontend-2
  closed:   completed
  time:     3m (agent 3m, person 0m)
  repo:     app
  after:    tsk_162d…9666 (until merged)
  timebox:  1h
  attempts: 2
  branch:   agent-frontend/invite-route
  commit:   df3a55c39644
  merged:   2026-11-02T11:02:20Z
  source:   agency/client2
  notes:
    2026-11-02 10:28  route + loader done, form next  [agency/client2]
  claim:    none on refs/lore/signals/main
```
<!-- /example -->

## What happened

```text
 agency/notes (store)                                     who wrote it
   refs/lore/live/main    client2/missions/…, tasks/…     agent-coord (mission apply), workers (notes, done)
                          client2/memories/… (finding, lessons)
   refs/heads/main        client2/.lore/data/…            landings: every finished task, with done_by,
                                                          done_commit, merged_at, claim log
   refs/lore/signals/main client2/claims/<task>.json      one commit, replaced on every claim and heartbeat

 github.com/client2/app
   agent-frontend/invite-route   pushed by `lore task note`, fetched by agent-frontend-2
   main                          the merged code
```

## Pitfalls

- **Keep the lease alive.** A long test run must not let the lease expire. Call `lore task heartbeat` (no id: every claim of this session, at most once per `lease.renew_every`) from your agent tool's hook, or run the daemon. `lore task heartbeat --daemon <id>` renews at once and then every `lease.renew_every`, and checks every 5 seconds that the process that started it is still running. When that process is gone, it stops (it also stops when the claim ends). So start it from a process that lives as long as the work: the agent's own long-running shell or wrapper script, or the same shell command as the long step (`lore task heartbeat --daemon <id> & go test ./...; kill $!`). A detached launch from a short-lived script or tool call (`setsid … &`, then the script exits) stops within seconds, together with that script. It needs the agent's `LORE_ACTOR` and `LORE_SESSION` in its environment. A heartbeat says "alive", not "progress". Record progress with `lore task note`.
- **Push work before you might die.** `lore task note` pushes the claim's work branch, so a replacement can fetch it. Work never pushed is lost with the session, and `lore mission status` says so.
- **Done is not merged.** A plain `after:` (or `--after <id>`) waits for **merged** when the task it waits for is in another repo, and only for **done** when both are in the same repo. To make a same-repo dependency wait for the merge, add or edit the task with `--after <id>:merged` (`:done` forces the other way): `lore task add 'build the invite page' --after tsk_162d…9666:merged`. A plan file's `after:` takes the same suffixes on a key: `after: ["backend:merged"]`. Pass `--commit <sha>` or `--pr <n>` to `lore task done` (a holder finishing on its work branch records the branch HEAD, unless that commit is already on `main`). Otherwise lore cannot see the merge (`merge unknown`); use `lore task merged <id>` to mark it by hand. With `merge_check.adapter: github`, lore asks `gh` about pull requests.
- **Claims fail closed.** Offline, a claim, heartbeat, release, done on a claimed task or an approval fails with `E_OFFLINE` (exit 15). Nothing is queued to happen later.
- **Speed and scale.** A claim is a git round trip (a second or two). Others see changes on their next fetch. Ten to twenty agents per store branch are fine.
- **Task ids from the store** are found from any linked repo by every task command (`task show`, `task next`, `task done`, `mission status`); `--from agency/client2` picks the source when an id exists in two places.
