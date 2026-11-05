# When to use agents, claims and gates

**In short:** an agent is a program that writes code for you in its own terminal. One agent working alone needs nothing more than the normal task commands. When several agents (or agents and people) share one plan, each one takes a task with a claim, which says "I'm on it" so nobody does the same work twice. A gate stops the agents before a risky step until a person says yes.

## The tools in one table

| Tool | What it is, in everyday words | Use it when | Commands |
|---|---|---|---|
| **Plain tasks** | a to-do list | one person or one agent does the work | `lore task add`, `lore task start`, `lore task done` |
| **Agent identity** | a name tag for one agent session, owned by a person | an agent writes notes or takes tasks in a team | `lore agent start --label agent-backend --owner @alice` |
| **Claim** | your name on the whiteboard next to a task | two or more workers could pick the same task | `lore task next --mission <id>`, `lore task claim <id>` |
| **Lease and heartbeat** | the claim runs out after 20 minutes unless the agent says "still here" | always, with claims; it is how a crashed agent's task comes free | `lore task heartbeat --daemon <id>` |
| **Dependencies** | "do this one after that one" | one task needs another's code first, maybe in another repo | `lore task add --after <id>` (`<id>:merged` waits for the merge) |
| **Gate** | a "stop, ask a person" sign on a task | a step is risky: a payment change, a change to the live database, a release | `lore gate request <id>`, then a person runs `lore gate approve <id>` |
| **Resource hold** | a key to a room only one person may use at a time | only one worker may run something heavy at once, like the full test suite | `lore hold claim full-suite --ttl 30m`, `lore hold release full-suite` |
| **Handoff and memory** | a note left for the next worker; a fact every agent should know | an agent stops mid-way, or learns something the others need | `lore handoff add --for @agent-frontend --task <id>`, `lore memory add "<fact>"` |

## If this, then that

- **If one agent works alone in your repo,** use plain tasks. No claims, no agent identity: the agent writes as you.
- **If two or more agents share one mission** (one shared goal with its tasks), start each one with `lore agent start`, and let each take work with `lore task next --mission <id>`. It resumes the task the agent already holds, respects dependencies and gates, and handles two agents reaching for the same task at once.
- **If a person tells an agent which task to do,** use `lore task claim <id>` instead of `next`.
- **If a step must never happen without a person,** put a gate on it with `lore gate request <id>`. Any person on the roster (the list of people and agents in the org) may approve; a mission's `approvers:` list can narrow that. An agent never can.
- **If an agent crashed or went quiet,** wait for its lease to run out (20 minutes by default, `lease.ttl`), and another agent takes the task with `lore task next`. To take it back at once, a person runs `lore task revoke <id>` or `lore task reassign <id>`.
- **If the work spans several repos,** link them to one project in the store (see [How to lay out your stores](store-layout.md)) and give each task a repo; agents pass `--repo <name>` to `lore task next`.
- **If two agents must not run the same heavy job at once,** hold a resource around it.
- **If an agent works in a client's repo,** claims and work notes stay in the agency's store, and the client's copy of the task carries only what the client should see.

## Pitfalls

- **Claims need the remote.** Claiming, renewing, releasing, finishing a claimed task, approving a gate and holding a resource succeed only when the git server confirmed them. Offline they fail with `E_OFFLINE`; they are never queued.
- **The 60-second cache.** Lore fetches once per command and reuses that fetch for 60 seconds (`lore sync` always fetches). A teammate's claim from a few seconds ago may not show yet; the claim itself always checks the server.
- **`lore task next` uses exit statuses.** `0` means you hold a task, `10` means nothing is ready (the message says why each task waits), `11` means another agent got every task you tried first. On `10`, stop or wait; do not loop.
- **One label per session.** Two sessions started with the same `--label` are one actor (one name on the roster), and their claims collide.
- **One claim per agent by default** (`agents.max_claims_per_agent: 1`), and after 3 agents have tried a task (`agents.max_attempts`) it is marked `blocked` and needs a person.
- **A released task stays `in_progress`.** `lore task release` drops only the claim; cancel the task or move it back if nobody should work on it.
- **Teams stay internal.** In a linked client repo, assigning a whole team is refused; assign a person.
- **Resources are not available in a linked client repo yet** (`E_NOT_IMPLEMENTED`); run shared-resource scripts from the agency's own repos.

## Guides and examples for each tool

| Tool | Guide | Example |
|---|---|---|
| Plain tasks | [Tasks](../tasks/tasks.md) | [`tasks`](../../tests/docs/sections/tasks.sh) ([output](../../tests/docs/transcripts/tasks.txt)) |
| Agents, claims, leases, gates | [Agents](../team/agents.md), [Agents on one mission](../team/use-cases/agents-mission.md) | [`agents`](../../tests/team/docs/transcripts/agents.txt) |
| What a claim writes in git | [How it works: signals](../team/how-it-works.md#signals-claim-renew-expire-reclaim) | [`internals`](../../tests/team/docs/transcripts/internals.txt) |
| Release, revoke, reassign, resources, teams | [Agents: taking work back](../team/agents.md#taking-work-back-from-an-agent-people-only) | [`team-ops`](../../tests/team/docs/transcripts/team-ops.txt) |
| Missions, plans, handoffs | [Missions and plans](../tasks/missions-and-plans.md) | [`missions`](../../tests/docs/sections/missions.sh) ([output](../../tests/docs/transcripts/missions.txt)) |
| How long one agent's run took | [Run log](../tasks/run-log.md) | [`runs`](../../tests/docs/sections/runs.sh) ([output](../../tests/docs/transcripts/runs.txt)) |
| Offline claim and other errors | [Troubleshooting](../team/troubleshooting.md) | [`errors`](../../tests/team/docs/transcripts/errors.txt) |

## Related

- [Which org and identity setup](org-and-identity.md): agents are actors on the org's roster.
- [Keep a note with the code, or share it at once](with-code-or-live.md)
- [All decision guides](README.md)
