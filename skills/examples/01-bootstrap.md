# Example: Start using lore in an existing project

**In short:** turn the notes file you already have into lore rows, then let lore write that file for you from now on. Do it once per project.

**Scenario:** Alice (an agency developer) works on the client's website at `~/projects/website`. She wants her coding agent to remember what she teaches it. She has an old `CLAUDE.md` (the notes file her agent reads) that she has been editing by hand.

## Session transcript

```
Alice: Let's set up lore for this project.

Agent:  I see ~/projects/website has an existing CLAUDE.md.
        I'll initialize lore, ingest your existing markdown,
        then render a fresh CLAUDE.md from the structured store.
```

```bash
cd ~/projects/website
lore init --non-interactive
# ✓ lore initialized at ~/projects/website
#     project_id: prj_019e...
#     name:       website
#     identity:   human:alice@example.com (git config)

lore learn-from docs
# Ingested 12 candidates from CLAUDE.md
#   review: lore learn list

lore learn list --json | jq -r '.data[] | "\(.id) \(.body[0:60])"'
# lcd_019e... "Run the tests before you push..."
# lcd_019e... "Never put passwords in the code..."
# lcd_019e... "Buttons are blue..."
# ...
```

```
Alice: They all look good, accept them all.
```

```bash
for id in $(lore learn list --json | jq -r '.data[].id'); do
    lore learn promote "$id" --target=memories
done
# ✓ 12 candidates accepted

lore render
# ✓ CLAUDE.md written (2,847 bytes, 12 memories, 0 rules)
```

```
Agent:  Done. CLAUDE.md is now rendered from the lore DB.
        Next session I'll see the same context.
        Going forward, when you correct me, I'll capture it
        with `lore rule add` or `lore memory add` and
        re-render so it persists.
```

## What just happened

- `.lore/lore.db` created (SQLite, WAL mode, auto-vacuum)
- 12 facts extracted from existing CLAUDE.md → `memories` table
- New CLAUDE.md generated deterministically from the DB
- `.gitignore` updated to exclude `.lore/lore.db` (the DB is user-local)

## What to commit

```bash
git status
# new file:    CLAUDE.md           ← commit this
# modified:    .gitignore          ← commit this
# new file:    .lore/           ← ignored (per .gitignore)
```

Only `CLAUDE.md` and `.gitignore` enter version control. The DB is per-developer.

## Pitfalls

- Review the candidates before promoting them all: `lore learn list` shows each one, and a wrong note promoted here reaches every future session.
- Commit the files lore creates (`.lore/` data and the rendered file); a teammate who clones the repo gets the same notes.
