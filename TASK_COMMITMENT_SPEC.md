---
title: Task commitment + deferral
status: accepted
---

# Task commitment & deferral — spec

<DocStatus state="review" owner="amank" updated="2026-05-17"></DocStatus>

Give every task two new orthogonal axes besides lifecycle `status`: **commitment** (is this real / agreed?) and **deferral** (real, but not now). The driver: AI agents create *all* tasks and set *all* state — there is no human triage step — so speculative tasks must be distinguishable from agreed ones without a human in the loop, and the failure mode must be loud, never silent.

Tasks for this work live in the sibling [`TASKS.md`](./TASKS.md) (per repo rule: specs hold design, not checklists).

<Callout type="info" title="Why status alone is not enough">

`status` is a *lifecycle* axis: `todo → in_progress → done / cancelled / blocked`. "Is this task real?" and "should it be active right now?" are different questions. Cramming them into `status` loses information (a deferred task forgets it was `in_progress`; a speculative task can't also be high-priority) and forces every existing status filter to change meaning. Two new fields keep the axes independent and composable.

</Callout>

## Why we need it

<FiveWhys problem="AI-created tasks pollute the active list and get auto-started">

<Why>AI agents generate tasks speculatively ("we could also refactor X") while doing other work.</Why>
<Why>Those land as `status=todo`, indistinguishable from tasks the user actually asked for.</Why>
<Why>A later AI session reads the task list, sees `todo`, and treats speculative ideas as agreed work — starting or reporting them.</Why>
<Why>There is no human triage step (the AI sets all state), so nothing downgrades a speculative task.</Why>
<Why>The data model has no axis for "is this agreed?" — only lifecycle status. Root cause: missing commitment axis.</Why>

</FiveWhys>

## Use cases

<Tabs>

<Tab label="Speculative (proposed)">

Agent is fixing a bug and notices a possible refactor. It records the idea so it isn't lost, but it is **not** agreed work:

```
lore task add "Refactor auth middleware" --tasklist=tlt_x --commitment=proposed
```

It will NOT appear in the default `lore task list`, will NOT be picked up as work by a future session, and shows only under `lore task triage`.

</Tab>

<Tab label="Agreed (accepted)">

User explicitly asked for it, or the agent is about to do it now:

```
lore task add "Add --json to plugin search" --tasklist=tlt_x --commitment=accepted
lore task start T-42        # also force-promotes commitment=accepted
```

Appears in the default list; counts as real work.

</Tab>

<Tab label="Parking lot (someday)">

An idea worth keeping with zero commitment and no date:

```
lore task add "Explore vector reranking" --tasklist=tlt_x --commitment=someday
```

Hidden from the active list; surfaces only under `lore task someday`.

</Tab>

<Tab label="Deferred (snooze)">

Real, accepted, but not now — auto-resurfaces when the date passes:

```
lore task edit T-42 --defer-until=2026-07-01
```

Disappears from the active list until 2026-07-01, then returns automatically. No manual un-defer.

</Tab>

</Tabs>

## Decision

<ADR status="accepted" id="ADR-TASK-1" date="2026-05-17" title="Commitment axis + deferral, no silent defaults, agent-required flag">

### Context

AI agents are the sole actors: they create tasks and drive every status transition. There is no human triage. Speculative and agreed tasks currently look identical (`status=todo`). A silent default for the new axis is unsafe in both directions — defaulting to `accepted` lets speculative junk pollute the active list (the original complaint); defaulting to `proposed` silently hides real work (violates the repo skeleton-honesty rule).

### Decision

- `status` stays lifecycle-only — **unchanged** (`todo / in_progress / done / cancelled / blocked`).
- Add `commitment` enum: `accepted | proposed | someday`. Schema default `accepted` (safe for non-agent / manual callers).
- Add `deferred_until` (nullable timestamp). Orthogonal snooze; auto-resurfaces.
- **Agent-required, loud:** when the resolved identity kind is `agent`, `lore task add` **requires** `--commitment` — missing → hard error (`E_INVALID_INPUT`), no default applied. Convention enforced by a check that errors loudly, not by a silent default.
- **Auto-promote:** `lore task start` / `lore task done` force `commitment=accepted` and clear `deferred_until`. Work in progress is committed and un-deferred by definition; self-heals a mis-`proposed` task.
- One shared `ActiveTask` predicate, defined once, consumed by `task list` default, the new views, and the task FTS fetch:
  `status NOT IN (done,cancelled) AND commitment='accepted' AND (deferred_until IS NULL OR deferred_until <= now)`.
- New read-only surfacing views so nothing is hidden silently: `lore task triage` (proposed), `lore task someday`, `lore task deferred`. Plus widening flags on `list` / `search`: `--commitment`, `--include-proposed`, `--include-someday`, `--include-deferred`, `--all`.
- The directive block gains one rule instructing the agent which commitment to pass.

### Consequences

- `render` is unaffected — it pins only rules + hotfixes, never tasks. No canary impact.
- Every existing task-status reader must be checked (Rule 2 list below) — the default `list` filter changes from "hide done+cancelled" to the `ActiveTask` predicate.
- Non-agent / manual `task add` keeps working (schema default `accepted`); only agent callers are forced to choose.
- New fields are additive; existing rows get `commitment=accepted`, `deferred_until=NULL` via schema default.

</ADR>

## Model

```sql
ALTER TABLE tasks ADD COLUMN commitment    TEXT NOT NULL DEFAULT 'accepted';  -- accepted|proposed|someday
ALTER TABLE tasks ADD COLUMN deferred_until INTEGER;                          -- nullable; snooze wake time
CREATE INDEX task_proj_commitment ON tasks(project_id, commitment);

-- ActiveTask (the one shared predicate):
--   status NOT IN ('done','cancelled')
--   AND commitment = 'accepted'
--   AND (deferred_until IS NULL OR deferred_until <= :now)
```

## Rule-2 shared sites (must all be checked)

| Site | File | Change |
|---|---|---|
| Status/commitment enum | `dbent/schema/enums.go` | add `TaskCommitment` + values |
| Task fields + index | `dbent/schema/task.go` | add `commitment`, `deferred_until`, index |
| `task add` | `saas/cmd/cli/task.go` ~314 | `--commitment` / `--defer-until`; agent-required loud error |
| `task edit` | `saas/cmd/cli/task.go` ~212 | `--commitment` / `--defer-until` / `--clear-defer` |
| default `task list` | `saas/cmd/cli/task.go` ~432 | swap to `ActiveTask`; widening flags |
| `task start/done` | `saas/cmd/cli/task.go` ~511 | auto-promote `accepted` + clear defer |
| new views | `saas/cmd/cli/task.go` | `triage` / `someday` / `deferred` |
| task FTS fetch | `saas/cmd/cli/task.go` ~79 | `ActiveTask` default + `--all` |
| actor kind | `identity.Resolve().Kind == "agent"` | gate the required-flag check |
| directive | `saas/cmd/cli/directive.go` ~59 | commitment decision rule |
| skill docs | `skills/COMMANDS.md`, `skills/examples/06-task-tracking.md` | document flag + views |
| README | `README.md` | mention commitment axis |
| TUI (generated) | `saas/pkg/aicoder/tuigen/register_task.go` | regenerated by codegen |

## Out of scope (v1)

- Global cross-entity `lore search` task-slice filtering by commitment — explicitly deferred; tracked in `TASKS.md`, not silently skipped.
- A background "wake" sweep that *notifies* on deferral expiry — `deferred_until` auto-resurfaces by query predicate; no scheduler in v1.
- Per-tasklist/mission default commitment.
