# Example: Multi-repo project with scoped knowledge

**Scenario:** Carol owns a platform with three repos under one product: `web` (React), `admin` (React+Mantine), `api` (Go+gqlgen). She wants per-repo memories AND cross-cutting shared knowledge.

## Setup

```bash
mkdir -p ~/projects/platform && cd ~/projects/platform
git init -q
lore init --non-interactive --name=platform

# Register the three repos
lore repo add web   --origin=git@github.com:acme/web.git
lore repo add admin --origin=git@github.com:acme/admin.git
lore repo add api   --origin=git@github.com:acme/api.git
```

## Capture knowledge with scope

```bash
# Repo-scoped (only visible in that repo's context)
lore memory add "Tailwind v4 only — no Mantine here"  --repo=web
lore memory add "Mantine v7 for forms + tables"        --repo=admin
lore memory add "gqlgen schema-first; resolvers ONLY in resolver/" --repo=api

# Cross-cutting (visible to ALL repos)
lore memory add "All times in UTC; client charges USD"
lore memory add "Conventional commits: type(scope): description"

lore render --repo=web   --target=../web/CLAUDE.md
lore render --repo=admin --target=../admin/CLAUDE.md
lore render --repo=api   --target=../api/CLAUDE.md
```

## What each repo's CLAUDE.md contains

| Repo | Sees |
|---|---|
| `web/CLAUDE.md` | Tailwind v4 memory + 2 cross-cutting |
| `admin/CLAUDE.md` | Mantine memory + 2 cross-cutting |
| `api/CLAUDE.md` | gqlgen memory + 2 cross-cutting |

The Mantine memory does NOT appear in `web/CLAUDE.md`. The Tailwind memory does NOT appear in `admin/CLAUDE.md`. Cross-cutting memories appear everywhere.

## Search variations

```bash
# Default: --repo=web sees web rows + master rows.
lore memory search "" --repo=web --json | jq '.count'
# 3

# Strict scope (no master fallback)
lore memory search "" --repo=web --no-inherit --json | jq '.count'
# 1

# Master only
lore memory search "" --master-only --json | jq '.count'
# 2

# Everything everywhere
lore memory search "" --all-repos --json | jq '.count'
# 5
```

## Why this matters

Without scope, every repo would see every other repo's noise. The web frontend dev would see "gqlgen resolvers in resolver/" — wrong context.

The right mental model: **scope = audience**. Who needs to know this?
- Only this repo? → `--repo=<name>`
- Every repo in this project? → no flag (master scope)
- Every project on this machine? → not aicoder's job; use a global tool
