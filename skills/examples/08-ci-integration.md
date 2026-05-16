# Example: CI integration — fail-fast on stale CLAUDE.md

**Scenario:** Hiro wants the CI to verify that `CLAUDE.md` in the PR matches what `lore render` would produce locally. If they diverge, the PR is failing to refresh.

## .github/workflows/aicoder.yml

```yaml
name: lore context check
on: [pull_request]

jobs:
  render-diff:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install aicoder
        run: |
          curl -L https://github.com/.../aicoder-linux-x64 -o /usr/local/bin/aicoder
          chmod +x /usr/local/bin/aicoder

      - name: Doctor
        run: LORE_READ_ONLY=1 lore doctor --json | jq -e '.db_ok'

      - name: CLAUDE.md is up to date
        run: |
          diff -u CLAUDE.md <(LORE_READ_ONLY=1 lore render --dry-run) \
            || { echo "::error::CLAUDE.md out of date — run 'lore render' locally"; exit 1; }

      - name: No secrets in DB
        run: |
          # Optional: scan all memory bodies via the same patterns lore uses on write
          LORE_READ_ONLY=1 lore memory list --json --include-archived \
            | jq -r '.data[].body' \
            | grep -E "AKIA[0-9A-Z]{16}|sk-[a-zA-Z0-9]{20,}|ghp_[a-zA-Z0-9]{36}" \
            && exit 1 || true
```

## Why LORE_READ_ONLY=1?

In CI, the runner has no business mutating the project DB. Read-only mode:

- Skips lock acquisition (faster CI)
- Returns `E_READ_ONLY` on any write attempt (catches bugs in your CI script)
- Safe to run in parallel with other jobs

## What this catches

| Failure | Catches |
|---|---|
| Developer added a rule but forgot to render | CLAUDE.md diff fails |
| DB itself is corrupt or missing | `doctor` fails first |
| A secret got into a memory | grep scan fails |
| Schema migrated by a newer lore | `doctor` reports `E_SCHEMA_VERSION_MISMATCH` |

## Pre-commit hook (developer side)

`.git/hooks/pre-commit`:

```bash
#!/usr/bin/env bash
# Auto-render before commit if the DB changed.
if git diff --cached --name-only | grep -q '^\.lore/'; then
    echo "lore DB changed; re-rendering..."
    lore render
    git add CLAUDE.md
fi
```

Or a Husky / lefthook config that calls `lore render` automatically.

## Pre-push hook (extra safety)

`.git/hooks/pre-push`:

```bash
#!/usr/bin/env bash
LORE_READ_ONLY=1 lore doctor --json | jq -e '.db_ok' >/dev/null \
    || { echo "lore doctor failed; refusing to push"; exit 1; }
```

## Render-on-demand vs render-in-CI

There are two ways to keep CLAUDE.md fresh:

1. **Render-in-CI**: developer commits a possibly-stale CLAUDE.md, CI catches it. ← shown above.
2. **Render-on-commit**: pre-commit hook auto-renders every time. ← simpler, but adds latency to every commit.

Pick one. Don't do both — they fight.
