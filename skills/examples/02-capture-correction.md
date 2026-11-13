# Example: Capture a correction in real time

**In short:** when someone corrects your agent, save the correction as a rule at once, so the next session follows it without being told again.

**Scenario:** Bob (an agency developer) works with his coding agent. The agent wrote a database password straight into a Go file. Bob corrects it: "never put passwords in the code."

## Session transcript

```
Bob:    No — never put passwords in the code.
        Read them from an environment variable.

Agent:  Got it. Let me capture this so I don't forget.
```

```bash
lore rule add \
    --severity must \
    --activation glob \
    --globs '["**/*.go", "!**/*_test.go"]' \
    --body "Never put passwords in the code; read them from environment variables."
# ✓ rul_<id> rul_019e...
#   severity:   must
#   activation: glob

lore render
# ✓ CLAUDE.md written (2,901 bytes, 12 memories, 1 rule)
```

```
Agent:  Captured as a must-severity rule scoped to non-test .go files.
        Future sessions will see this in CLAUDE.md and respect it.
```

## What just happened

- `rules` table got a new row with severity=must, activation=glob
- Rule was rendered into CLAUDE.md inside a `<rule severity="must">` block
- The rule is NOT semantic — it's written into the agent's context every session

## Check that the agent will see it

```bash
lore why-context --rendered | grep -A2 "rul_<id>"
# <rule id="rul_<id>" severity="must" activation="glob">
# Never put passwords in the code; read them from environment variables.
# </rule>
```

## When NOT to use a rule

- "I prefer this style" → `memory add` (not a hard constraint)
- "We decided X because Y" → `decision add` (rationale matters more than the conclusion)
- "We keep hitting this" → `hotfix add` (loud warning, never truncated)

Rule = a thing we will block / warn / nudge on. Reserve for actual constraints.

## Pitfalls

- Choose the scope with care: `--globs` limits the rule to matching files; a pattern that matches nothing hides the rule everywhere.
- A rule is for something you will always enforce. A preference belongs in a memory, a choice with reasons in a decision.
