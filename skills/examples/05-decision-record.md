# Example: Capture an architectural decision with rationale

**Scenario:** Eve and the team just chose pgvector over Pinecone for vector storage. The decision was non-obvious and they want future selves (and AIs) to know WHY.

## Session transcript

```
Eve:    OK, we're going with pgvector. Capture it.

Claude: Should I phrase it as a rule or a decision? Rules say WHAT,
        decisions say WHY. For "we picked X over Y", a decision is
        the right shape.

Eve:    Yes — decision. Rationale: ops simplicity, single DB, good-enough
        recall, avoid vendor lock-in.
```

```bash
lore decision add \
    --title="Use pgvector for embeddings (not Pinecone)" \
    --body="Rationale:
- Ops simplicity: one DB, not two.
- Vendor independence: no lock-in to Pinecone pricing.
- Recall is good enough at our scale (<1M vectors).
- HNSW index in pgvector 0.7 handles our latency target (p95 < 50ms).

Considered + rejected:
- Pinecone: better recall on adversarial queries, but ops cost too high.
- Weaviate self-hosted: extra moving part.
- Qdrant: similar to pgvector but adds infra.

Revisit when:
- vector count > 10M
- p95 latency > 100ms
- recall@10 < 0.85 on benchmark

Decided: 2026-04-30 by @eve, @bob, @carol."
# ✓ dec_<id> dec_019e...

lore render
```

```
Claude: Captured as dec_<id> with full rationale and revisit triggers.
        Anyone (human or AI) reading CLAUDE.md will now see this
        decision and its constraints.
```

## What just happened

- `decisions` table got a structured ADR
- Render places it in CLAUDE.md inside `<decision id="dec_<id>" status="accepted">` tags
- The "revisit when" criteria are now explicit — future-Eve will know when to reopen

## Anti-pattern this avoids

Without a decision record, six months later someone says "let's switch to Pinecone" and **the team has lost the context for why they didn't.** Decisions are a stake-in-the-ground.

## When to use decision vs rule vs memory

| Shape | Use when |
|---|---|
| `decision add` | The WHY matters more than the rule. Has alternatives considered + revisit criteria. |
| `rule add` | Hard constraint that should fail/warn on violation. WHAT matters more than why. |
| `memory add` | Loose fact. No enforcement, no rationale needed. |

A decision can BECOME a rule: "we decided pgvector → ergo rule: no direct Pinecone client imports."
