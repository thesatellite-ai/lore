# Example: Tags + comments for cross-cutting metadata

**Scenario:** Iris wants to mark certain memories as "draft", attach review comments to decisions, and find all "blocked" tasks regardless of mission.

## Tags

### Create + list

```bash
lore tag add --name=urgent     --color="#ff0033"
lore tag add --name=draft      --color="#888888"
lore tag add --name=needs-review
lore tag add --name=blocked

lore tag list --json | jq -r '.data[] | "\(.name) (\(.uses // 0) uses)"'
```

### Attach to ANY entity (polymorphic)

```bash
# Tag a memory
lore tag attach --on-table=memories  --on-id=mem_019e... --tag=draft

# Tag a task
lore tag attach --on-table=tasks     --on-id=tsk_019e... --tag=blocked

# Tag a decision
lore tag attach --on-table=decisions --on-id=dec_019e... --tag=needs-review

# Tag a hotfix
lore tag attach --on-table=hotfixes  --on-id=hfx_019e... --tag=urgent
```

`--on-table` accepts any entity table name (`memories`, `rules`, `decisions`, `hotfixes`, `tasks`, `missions`, `playbooks`, etc.). Pretty IDs (`mem_<id>`, `dec_<id>`) NOT accepted — use the opaque `<prefix>_<hex>` form.

### Detach

```bash
lore tag detach --on-table=tasks --on-id=tsk_019e... --tag=blocked
```

### Find everything tagged X

There's no first-class "find by tag" command in v0.1, but you can compose:

```bash
sqlite3 .lore/lore.db <<'SQL'
SELECT entity_table, entity_id
FROM entity_tags et
JOIN tags t ON t.id = et.tag_id
WHERE t.name = 'blocked';
SQL
```

Or via JSON:

```bash
lore comment list --json | jq '.data[]'   # (replace with eventual `tag uses` cmd in v0.2)
```

## Comments

### Add a comment on any entity

```bash
lore comment add \
    --on-table=decisions \
    --on-id=dec_019e... \
    "Revisited 2026-08-01. Still holds; pgvector recall acceptable at 5M vectors."
```

### List comments

```bash
# All comments
lore comment list --json

# Comments on a specific entity
lore comment list --on-table=decisions --on-id=dec_019e... --json
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
