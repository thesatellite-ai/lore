# Example: Multi-repo project with scoped knowledge

**In short:** one lore project can cover several repos. A note can belong to one repo, so only that repo's agent sees it, or to the whole project, so every repo sees it.

**Scenario:** client 2's product is three repos: `app` (the website people use), `admin` (the back office) and `api` (the server). Alice (an agency developer) wants notes that apply to one repo only, and notes that apply to all three.

## Setup

```bash
mkdir -p ~/projects/platform && cd ~/projects/platform
git init -q
lore init --non-interactive --name=platform

# Register the three repos
lore repo add app   --origin=git@github.com:client2/app.git
lore repo add admin --origin=git@github.com:client2/admin.git
lore repo add api   --origin=git@github.com:client2/api.git
```

## Capture knowledge with scope

```bash
# Repo-scoped (only visible in that repo's context)
lore memory add --body="Buttons are blue on every page"         --repo=app
lore memory add --body="Only staff can open the admin pages"   --repo=admin
lore memory add --body="Run the API tests before you push"     --repo=api

# Cross-cutting (visible to ALL repos)
lore memory add --body="All times are shown in the user's own time zone"
lore memory add --body="Never put passwords in the code"

lore render --repo=app   --target=../app/CLAUDE.md
lore render --repo=admin --target=../admin/CLAUDE.md
lore render --repo=api   --target=../api/CLAUDE.md
```

## What each repo's CLAUDE.md contains

| Repo | Sees |
|---|---|
| `app/CLAUDE.md` | the blue-buttons note + 2 shared notes |
| `admin/CLAUDE.md` | the staff-only note + 2 shared notes |
| `api/CLAUDE.md` | the API-tests note + 2 shared notes |

The staff-only note does NOT appear in `app/CLAUDE.md`. The blue-buttons note does NOT appear in `admin/CLAUDE.md`. Shared notes appear everywhere.

## Search variations

```bash
# Default: --repo=app sees web rows + master rows.
lore memory search "" --repo=app --json | jq '.count'
# 3

# Strict scope (no master fallback)
lore memory search "" --repo=app --no-inherit --json | jq '.count'
# 1

# Master only
lore memory search "" --master-only --json | jq '.count'
# 2

# Everything everywhere
lore memory search "" --all-repos --json | jq '.count'
# 5
```

## Why this matters

Without scope, every repo would see every other repo's noise. The website developer would see "only staff can open the admin pages", which is not about their repo.

The right mental model: **scope = audience**. Who needs to know this?
- Only this repo? → `--repo=<name>`
- Every repo in this project? → no flag (master scope)
- Every project on this machine? → not lore's job; use a global tool

## Pitfalls

- `--no-inherit` hides the shared notes too; use it only when you really want one repo's own notes.
- `--repo` takes the name you gave in `lore repo add` (or the `rep_…` id), not the folder name.
