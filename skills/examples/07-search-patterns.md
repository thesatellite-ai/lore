# Example: Search patterns — getting the right rows back

**Scenario:** Greta has a project with 500 memories accumulated over a year. She wants to find specific things fast.

## Basic search

```bash
lore memory search "redis"
# FTS5 BM25 ranking. Matches "redis", "Redis", "REDIS" via unicode61 tokenizer.
```

```bash
lore memory search "redis" --json | jq '.count, .results[0:3]'
```

## Phrase search

```bash
lore memory search '"connection pool"'
# Quoted phrase — matches the words IN ORDER, adjacent.
```

## Boolean

```bash
lore memory search "redis OR cache"
# Either word. Uppercase OR / AND / NOT — FTS5 syntax.
```

```bash
lore memory search "auth NOT deprecated"
# Has "auth" but not "deprecated".
```

## Prefix

```bash
lore memory search "auth*"
# Matches authenticate, authorize, authentication, etc.
```

## Scope

```bash
# Default: cwd's repo (if any) + master
lore memory search "tailwind" --repo=web

# All repos
lore memory search "tailwind" --all-repos

# Master only (cross-cutting)
lore memory search "tailwind" --master-only

# Strict scope (don't include master fallback)
lore memory search "tailwind" --repo=web --no-inherit

# Include archived
lore memory search "tailwind" --include-archived
```

## Limits

```bash
lore memory search "" --limit=5         # last 5 memories
lore memory search "" --limit=0         # unlimited (careful)
```

## Empty query = listing

```bash
lore memory search "" --json
# No FTS5 — returns everything, newest first, scoped by flags.
# Same as `lore memory list --json` but supports scope flags.
```

## Filter results with jq

```bash
# Memories about auth, only high-trust ones
lore memory search "auth" --json | jq '.results[] | select(.trust_score >= 0.7)'

# IDs only (for piping into another command)
lore memory search "deprecated" --json | jq -r '.results[].id'

# Count by kind
lore memory list --json | jq -r '.data[].kind' | sort | uniq -c
```

## Cross-entity search (workaround for v0.1)

There's no global "search across all kinds" yet. Compose:

```bash
for kind in memory rule decision hotfix pattern playbook; do
    hits=$(lore $kind list --json | jq --arg q "tailwind" -r '
        .data[] | select((.body // .title // "") | test($q;"i"))
                | "[\(input_line_number)] \(.id) \($q)"' 2>/dev/null)
    [ -n "$hits" ] && echo "=== $kind ===" && echo "$hits"
done
```

## Why "no results" might happen

| Reason | Fix |
|---|---|
| FTS5 not yet indexed for that DB | `lore doctor` — first read triggers backfill |
| Wrong scope | Try `--all-repos`, `--master-only`, or remove `--repo=` |
| Memory archived | `--include-archived` |
| Tokenizer split your query weirdly | Try prefix (`foo*`) or phrase (`"foo bar"`) |
| Genuine miss | The fact wasn't captured — `lore memory add` to fix the gap |
