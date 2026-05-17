# Example: Mission with tasks across a sprint

**Scenario:** Frank is starting a 2-week sprint to ship v0.2. Wants tracked work items, priorities, due dates, and a single command to see "what's next?".

## Day 0 — create the mission

```bash
M=$(lore mission add \
    "Ship v0.2 — FTS5 + plugins + MCP server" \
    --target=2026-06-15 \
    --body="Sprint goal: open the plugin protocol, ship MCP server alpha, expand FTS5 to all entity types." \
    --json | jq -r '.data.id')
echo "Mission: $M"
# Mission: msn_019e...
```

## Day 0 — populate tasks

```bash
# Agents MUST pass --commitment (no default). Work the user agreed to = accepted;
# your own speculative ideas = proposed (they land under `lore task triage`, not the
# default list); parking-lot ideas = someday.
lore task add "Design plugin manifest schema"         --mission=$M --commitment=accepted --priority=high --due=2026-05-20
lore task add "Wire MCP server stub"                  --mission=$M --commitment=accepted --priority=high --due=2026-05-25
lore task add "Extend FTS5 to rules + decisions"      --mission=$M --commitment=accepted --priority=medium --due=2026-05-30
lore task add "Document plugin lifecycle"             --mission=$M --commitment=accepted --priority=medium --due=2026-06-05
lore task add "v0.2 release notes draft"              --mission=$M --commitment=proposed --priority=low  --due=2026-06-10
lore task add "macOS notarization config"             --mission=$M --commitment=someday --priority=low

lore mission show $M --json | jq '.data.tasks | length'
# 6
```

## Daily — what's next?

```bash
# All open tasks ordered by priority
lore task list --status=todo --json | jq -r '
    .data
    | sort_by(.priority)
    | .[]
    | "T-\(.id) [\(.priority)] \(.title) (due \(.due_at // "—"))"
'

# Just urgent + high
lore task list --status=todo --json | jq -r '
    .data[] | select(.priority == "urgent" or .priority == "high") | "T-\(.id) \(.title)"
'

# What did I close today?
lore task list --status=done --json | jq -r '
    .data[] | select(.completed_at | startswith(now | strftime("%Y-%m-%d"))) | "T-\(.id) \(.title)"
'
```

## During work — start / done

```bash
lore task start tsk_<id>     # status: todo → in_progress, sets started_at
# ... do the work ...
lore task done tsk_<id>      # status: in_progress → done, sets completed_at
```

## End-of-sprint review

```bash
# Mission report
lore mission show $M --json | jq '{
    mission: .data.title,
    target:  .data.target_date,
    status:  .data.status,
    todo:    (.data.tasks | map(select(.status == "todo")) | length),
    doing:   (.data.tasks | map(select(.status == "in_progress")) | length),
    done:    (.data.tasks | map(select(.status == "done")) | length)
}'
```

```
{
  "mission":  "Ship v0.2 — FTS5 + plugins + MCP server",
  "target":   "2026-06-15",
  "status":   "active",
  "todo":     1,
  "doing":    1,
  "done":     4
}
```

## Mark the mission complete

```bash
lore mission done $M
# ✓ msn_<id> completed
```

## Tips

- **Use `--mission`** rather than naming the goal in every task title.
- **Pretty IDs are persistent** within a project (`tsk_<id>` always means task id 1 in this project).
- **Use tags for cross-cutting concerns** (`lore tag attach --on-table=tasks --on-id=tsk_<id> --tag=blocked-on-vendor`).
- **Reminders attach to tasks**: `lore reminder add "Follow up on tsk_<id>" --due=2026-05-25 --on-table=tasks --on-id=tsk_<id>`.
