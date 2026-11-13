# Example: Mission with tasks across a sprint

**In short:** a mission is one goal ("launch the new website"); tasks are the steps toward it. Add the tasks once, then ask lore what is next each day and mark each one done.

**Scenario:** Alice (an agency developer) has two weeks to launch the client's new website. She wants a list of the work with priorities and due dates, and one command that says "what's next?".

## Day 0 — create the mission

```bash
M=$(lore mission add \
    --title "Launch the new website" \
    --target=2026-06-15 \
    --body="Goal: the new website is live before the client's Friday demo." \
    --json | jq -r '.data.id')
echo "Mission: $M"
TL=$(lore tasklist add --title="launch" --body="work for the launch" --json | jq -r '.data.id')   # every task belongs to a tasklist
# Mission: msn_019e...
```

## Day 0 — populate tasks

```bash
# Agents MUST pass --commitment (no default). Work the user agreed to = accepted;
# your own speculative ideas = proposed (they land under `lore task triage`, not the
# default list); parking-lot ideas = someday.
lore task add --title "Make the buttons blue"                 --tasklist=$TL --mission=$M --commitment=accepted --priority=high --due=2026-05-20
lore task add --title "Add the contact form"                  --tasklist=$TL --mission=$M --commitment=accepted --priority=high --due=2026-05-25
lore task add --title "Run the tests before you push"         --tasklist=$TL --mission=$M --commitment=accepted --priority=medium --due=2026-05-30
lore task add --title "Write the launch checklist"            --tasklist=$TL --mission=$M --commitment=accepted --priority=medium --due=2026-06-05
lore task add --title "Send the invoice reminder before Friday's demo" --tasklist=$TL --mission=$M --commitment=proposed --priority=low  --due=2026-06-10
lore task add --title "Add a dark mode"                       --tasklist=$TL --mission=$M --commitment=someday --priority=low

lore mission show --id $M --json | jq '.data.tasks | length'
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
lore task start --id tsk_<id>     # status: todo → in_progress, sets started_at
# ... do the work ...
lore task done --id tsk_<id>      # status: in_progress → done, sets completed_at
```

## End-of-sprint review

```bash
# Mission report
lore mission show --id $M --json | jq '{
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
  "mission":  "Launch the new website",
  "target":   "2026-06-15",
  "status":   "active",
  "todo":     1,
  "doing":    1,
  "done":     4
}
```

## Mark the mission complete

```bash
lore mission done --id $M
# ✓ msn_<id> completed
```

## Tips

- **Use `--mission`** rather than naming the goal in every task title.
- **Pretty IDs are persistent** within a project (`tsk_<id>` always means task id 1 in this project).
- **Use tags for cross-cutting concerns** (`lore tag attach --on-table=tasks --on-id=tsk_<id> --tag=waiting-for-client`).
- **Reminders attach to tasks**: `lore reminder add --message "Follow up on tsk_<id>" --due=2026-05-25 --on-table=tasks --on-id=tsk_<id>`.

## Pitfalls

- Agents must pass `--commitment`; a task an agent made up on its own (`proposed`) waits in `lore task triage` until a person accepts it.
- Run `lore task start` before `lore task done`, so the task records when work began.
