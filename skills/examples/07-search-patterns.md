# Example: Search patterns — getting the right rows back

**In short:** `lore memory search` finds notes by words, best match first. Quote a phrase, combine words with OR / NOT, end a word with `*` to match its longer forms, and use `--repo` to look in one repo only.

**Scenario:** Bob (an agency developer) has a project with 500 notes saved over a year. He wants to find one fast.

## Basic search

```bash
lore memory search "invoice"
# Best matches first. Upper and lower case do not matter: "Invoice" and "INVOICE" match too.
```

```bash
lore memory search "invoice" --json | jq '.count, .results[0:3]'
```

## Phrase search

```bash
lore memory search '"unpaid invoice"'
# Quoted phrase — matches the words IN ORDER, adjacent.
```

## Boolean

```bash
lore memory search "invoice OR receipt"
# Either word. Write OR / AND / NOT in capitals.
```

```bash
lore memory search "client NOT staging"
# Has "client" but not "staging".
```

## Prefix

```bash
lore memory search "pay*"
# Matches pay, payment, payments, paying, etc.
```

## Scope

```bash
# Default: the repo you are in (if any) + master (the shared notes every repo sees)
lore memory search "invoice" --repo=app

# All repos
lore memory search "invoice" --all-repos

# Master only (notes for every repo)
lore memory search "invoice" --master-only

# Strict scope (don't include master fallback)
lore memory search "invoice" --repo=app --no-inherit

# Include archived
lore memory search "invoice" --include-archived
```

## Limits

```bash
lore memory search "" --limit=5         # last 5 memories
lore memory search "" --limit=0         # unlimited (careful)
```

## Empty query = listing

```bash
lore memory search "" --json
# No search at all: returns everything, newest first, scoped by flags.
# Same as `lore memory list --json` but supports scope flags.
```

## Filter results with jq

```bash
# Notes about invoices, only well-trusted ones (trust_score: how much lore trusts a note, 0 to 1)
lore memory search "invoice" --json | jq '.results[] | select(.trust_score >= 0.7)'

# IDs only (for piping into another command)
lore memory search "WhatsApp" --json | jq -r '.results[].id'

# Count by kind
lore memory list --json | jq -r '.data[].kind' | sort | uniq -c
```

## Cross-entity search (workaround for v0.1)

There is no single search across every kind of row (notes, rules, decisions and so on) yet. Loop over the kinds instead:

```bash
for kind in memory rule decision hotfix pattern playbook; do
    hits=$(lore $kind list --json | jq --arg q "invoice" -r '
        .data[] | select((.body // .title // "") | test($q;"i"))
                | "[\(input_line_number)] \(.id) \($q)"' 2>/dev/null)
    [ -n "$hits" ] && echo "=== $kind ===" && echo "$hits"
done
```

## Why "no results" might happen

| Reason | Fix |
|---|---|
| The search index is not built yet for that database | `lore doctor` — first read triggers backfill |
| Wrong scope | Try `--all-repos`, `--master-only`, or remove `--repo=` |
| Memory archived | `--include-archived` |
| The search split your query into words in an odd way | Try prefix (`foo*`) or phrase (`"foo bar"`) |
| Genuine miss | The fact wasn't captured — `lore memory add` to fix the gap |

## Pitfalls

- By default search looks in the current repo plus the shared notes; pass `--all-repos` to look everywhere.
- Archived notes are left out unless you pass `--include-archived`.
