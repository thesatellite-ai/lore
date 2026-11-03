# Example: Capture an architectural decision with rationale

**In short:** when the team makes a choice, save it as a decision with the reasons and what was turned down. Six months later nobody has to guess why.

**Scenario:** Alice (an agency developer) and the team just chose Stripe for the client's payments, over a bank's own payment page. They want everyone who comes later, people and agents, to know WHY.

## Session transcript

```
Alice:  OK, we're going with Stripe for payments. Save that.

Agent:  Should I phrase it as a rule or a decision? Rules say WHAT,
        decisions say WHY. For "we picked X over Y", a decision is
        the right shape.

Alice:  Yes — decision. Reasons: the client already has an account,
        it handles cards and refunds, and setup takes a day.
```

```bash
lore decision add \
    --title="We use Stripe for payments" \
    --body="Why:
- The client already has a Stripe account.
- It handles cards, refunds and receipts for us.
- Setup takes about a day.

Looked at and said no to:
- The bank's own payment page: cheaper, but no refunds from our side.
- Invoices only: the client wants people to pay at checkout.

Look at this again when:
- the client sells outside Europe
- fees go above 3% of sales

Decided: 2026-04-30 by @alice, @bob, @carol."
# ✓ dec_<id> dec_019e...

lore render
```

```
Agent:  Captured as dec_<id> with full rationale and revisit triggers.
        Anyone (person or agent) reading CLAUDE.md will now see this
        decision and its constraints.
```

## What just happened

- `decisions` table got a structured decision record (the choice, the reasons, what was turned down)
- Render places it in CLAUDE.md inside `<decision id="dec_<id>" status="accepted">` tags
- The "revisit when" criteria are now explicit — the next person will know when to reopen it

## Anti-pattern this avoids

Without a decision record, six months later someone says "let's switch to the bank's page" and **the team has lost the context for why they didn't.** A decision record keeps the reasons next to the choice, so nobody has to guess.

## When to use decision vs rule vs memory

| Shape | Use when |
|---|---|
| `decision add` | The WHY matters more than the rule. Has alternatives considered + revisit criteria. |
| `rule add` | A must-follow instruction that blocks or warns when broken. WHAT matters more than why. |
| `memory add` | A plain fact to remember. Nothing is enforced and no reason is needed. |

A decision can BECOME a rule: "we decided on Stripe → so the rule: all payments go through the Stripe module."

## Pitfalls

- To change a decision, add the new one with `--supersedes dec_<old id>` instead of editing the old one, so the history of the choice stays readable.
- Write the reasons in plain words; the person reading it later may not have been in the meeting.
