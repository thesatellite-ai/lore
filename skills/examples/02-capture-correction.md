# Example: Capture a correction in real time

**Scenario:** Bob is pair-programming with Claude. Claude wrote `fmt.Errorf("op: %w", err)` on a stdlib call. Bob corrects: "no, we don't wrap stdlib errors."

## Session transcript

```
Bob:    No — we don't wrap stdlib errors with fmt.Errorf here.
        Just return err.

Claude: Got it. Let me capture this so I don't forget.
```

```bash
lore rule add \
    --severity=must \
    --activation=glob \
    --globs='["**/*.go", "!**/*_test.go"]' \
    "Do not wrap stdlib errors with fmt.Errorf; return them directly."
# ✓ rul_<id> rul_019e...
#   severity:   must
#   activation: glob

lore render
# ✓ CLAUDE.md written (2,901 bytes, 12 memories, 1 rule)
```

```
Claude: Captured as a must-severity rule scoped to non-test .go files.
        Future sessions will see this in CLAUDE.md and respect it.
```

## What just happened

- `rules` table got a new row with severity=must, activation=glob
- Rule was rendered into CLAUDE.md inside a `<rule severity="must">` block
- The rule is NOT semantic — it's hard-coded into the AI's context every session

## Verify the AI will see it

```bash
lore why-context --last-render --rendered | grep -A2 "rul_<id>"
# <rule id="rul_<id>" severity="must" activation="glob">
# Do not wrap stdlib errors with fmt.Errorf; return them directly.
# </rule>
```

## When NOT to use a rule

- "I prefer this style" → `memory add` (not a hard constraint)
- "We decided X because Y" → `decision add` (rationale matters more than the conclusion)
- "We keep hitting this" → `hotfix add` (loud warning, never truncated)

Rule = a thing we will block / warn / nudge on. Reserve for actual constraints.
