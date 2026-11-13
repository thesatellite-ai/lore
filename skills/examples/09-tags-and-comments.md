# Example: Tags + comments for cross-cutting metadata

**In short:** a tag is a one-word label you can put on any row ("draft", "blocked"); a comment is a short note attached to one row, like a sticky note. Use tags to group rows and comments to talk about one.

**Scenario:** Alice (an agency developer) wants to mark some notes as "draft", add review comments to decisions, and find every "blocked" task in any mission.

## Tags

### Create + list

```bash
lore tag add --name urgent     --color "#ff0033"
lore tag add --name draft      --color "#888888"
lore tag add --name needs-review
lore tag add --name blocked

lore tag list --json | jq -r '.data[] | "\(.name) (\(.uses // 0) uses)"'
```

### Attach to ANY entity (polymorphic)

```bash
# Tag a memory
lore tag attach --on-table memories  --on-id mem_019e... --tag draft

# Tag a task
lore tag attach --on-table tasks     --on-id tsk_019e... --tag blocked

# Tag a decision
lore tag attach --on-table decisions --on-id dec_019e... --tag needs-review

# Tag a hotfix
lore tag attach --on-table hotfixes  --on-id hfx_019e... --tag urgent
```

`--on-table` accepts any entity table name (`memories`, `rules`, `decisions`, `hotfixes`, `tasks`, `missions`, `playbooks`, etc.). Pretty IDs (`mem_<id>`, `dec_<id>`) NOT accepted — use the opaque `<prefix>_<hex>` form.

### Detach

```bash
lore tag detach --on-table tasks --on-id tsk_019e... --tag blocked
```

### Find everything tagged X

The `list` commands of memories, rules, decisions and hotfixes filter by tag:

```bash
lore memory list --tag blocked
lore rule list --tag blocked --json | jq -r '.data[].id'
```

Or via JSON:

```bash
lore comment list --json | jq '.data[]'   # (replace with eventual `tag uses` cmd in v0.2)
```

## Comments

### Add a comment on any entity

```bash
lore comment add \
    --on-table decisions \
    --on-id dec_019e... \
    --body "Checked again on 2026-08-01. Still right: Stripe fees are below 3% of sales."
```

### List comments

```bash
# All comments
lore comment list --json

# Comments on a specific entity
lore comment list --on-table decisions --on-id dec_019e... --json
```

### Comments are timeline-style

Each comment has a `created_at` timestamp. They're naturally ordered by insertion. Use comments for:

- Periodic check-ins on decisions
- Status notes on tasks
- "Did this in PR #42" cross-references
- Discussion of memories that need refining before promotion to rule

## When to use tag vs comment vs separate entity

| Need | Use |
|---|---|
| Single-word label, applies to many entities | **tag** |
| Free-form note attached to one specific entity | **comment** |
| Full standalone artifact with its own ID/lifecycle | **memory / rule / decision / hotfix** |

A `tag` is for filtering. A `comment` is for context. A `memory` is for the thing itself.

## Polymorphic shape (one of lore's best ideas)

`entity_table + entity_id` lets tags + comments attach to **every** entity without requiring a column for each. So a tag named "needs-review" works equally well on memories, decisions, tasks, hotfixes, playbooks — all 24+ entity kinds.

This is why `--on-table` is required: SQLite has no foreign-key polymorphism, so the table name + id together form the link.

## Pitfalls

- `--on-id` needs the full row id (`dec_` plus 32 hex digits), not a short form.
- A tag must exist (`lore tag add`) before you attach it.
