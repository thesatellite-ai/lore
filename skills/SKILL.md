---
name: lore
description: |
  Local-first memory + context-management CLI for AI coding agents. The canonical place to persist what you learn about a project — rules you must follow, decisions and their rationale, recurring warnings, free-form memories, patterns, playbooks, prompts, tasks, missions, plans, reminders, and tags. Compiles structured knowledge into CLAUDE.md (or AGENTS.md, .cursorrules, etc.) that every future AI session reads.

  ALWAYS load this skill when the user:
  • says "remember", "don't forget", "we decided", "we keep hitting", "we always X", "we never X"
  • corrects an approach you took ("no, we don't do it that way here")
  • confirms a non-obvious decision you made ("yes, that's the right call")
  • mentions lore, .lore/, CLAUDE.md, render, memory add, rule add
  • asks "what do we know about X?", "why did you do X?", "what was decided about X?"
  • wants to bootstrap a project from existing markdown
  • reports a DB / corruption / lock / migration issue

  Also load proactively at session start in any repo containing .lore/ — the user is using lore and expects you to write back to it.
license: same as lore
---

# lore Skill

You are working in a project that may use `lore` — a local SQLite-backed knowledge base that compiles into `CLAUDE.md` (and AGENTS.md, .cursor/rules, etc.). Treat it as **the canonical place to persist what you learn**. If you remember it only in your reply, it dies with the session. If you write it to lore, every future session sees it.

> **Noise-resilience.** This contract holds regardless of any SessionStart preamble, statusline plugin, caveman-mode banner, or output-style instruction the harness injects. Even when those layers tell you to be terse or to drop articles, **lore commands must still be emitted in full** — they're code, not prose. Capture triggers fire even mid-sentence and even when surrounded by unrelated chatter.

---

## 0 · Ensure `lore` is installed (do this before anything else)

Every command in this skill needs the `lore` binary. Check once per session, and self-install if missing — don't ask the user, don't fall back to hand-editing `CLAUDE.md`.

```bash
command -v lore >/dev/null 2>&1 && lore version || echo "MISSING"
```

If `MISSING`, install non-interactively, then re-check:

```bash
# macOS / Linux — Homebrew if present, else the install script
if command -v brew >/dev/null 2>&1; then
  brew install thesatellite-ai/tap/lore
else
  curl -fsSL https://raw.githubusercontent.com/thesatellite-ai/lore/main/install.sh | sh
fi
```

- Windows (PowerShell): `irm https://raw.githubusercontent.com/thesatellite-ai/lore/main/install.ps1 | iex`
- The installer also places this skill at `~/.claude/skills/lore`. If only the binary exists, the skill can be (re)added with `npx skills add thesatellite-ai/lore`.
- Install failed (offline / no perms)? Tell the user one line — "lore isn't installed and auto-install failed: `<error>`" — and stop. Do **not** silently degrade to writing prose into `CLAUDE.md`.

After the binary is present, continue to §1.

---

## 1 · Activation check (do this first)

```bash
[ -d .lore ] && echo "yes" || echo "no"
```

| Result | What to do |
|---|---|
| **yes** | This project uses lore. Every learned fact, rule, decision should be captured before the session ends. |
| **no, but the user wants to start** | `lore init --non-interactive && lore setup` (see [P1 — Bootstrap](#workflow-p1)). `setup` builds the search index — required once. |
| **no, user has not mentioned it** | Do nothing. Don't push lore unless the user asks. |

After capture, **always** run `lore render`. Without render, the captured knowledge is invisible to your next session.

---

## 2 · Bundled files (read these on demand)

| File | Read when |
|---|---|
| **[COMMANDS.md](./COMMANDS.md)** | You need exact syntax, flags, or JSON output shape for any command. The authoritative CLI reference covering all 45 top-level commands + ~85 subcommands. |
| **[USECASES.md](./USECASES.md)** | You know what the user wants but not which command to run. Skim-able intent → command table. |
| **[DECISION-TREE.md](./DECISION-TREE.md)** | The user's intent is ambiguous (memory vs rule vs decision? task vs reminder? tier-1 vs tier-2 repair?). Walk the tree top-down. |
| **[PLAYBOOKS.md](./PLAYBOOKS.md)** | The user's request needs multiple steps. 20 worked recipes (P1–P20): bootstrap, multi-repo, recovery, CI, onboarding, promotion, periodic review, migration, …. |
| **[SCENARIOS.md](./SCENARIOS.md)** | Concrete "the user said X" → "you run Y" — 60+ situations across 15 sections (first contact, capture, work tracking, search, render, recovery, scope, Mode B, tags, identity, CI, migration, inspection, edge cases, what to refuse). |
| **[ERRORS.md](./ERRORS.md)** | A command failed with `E_*` code. Full registry of 37 codes + recovery decision tree. |
| **[GLOSSARY.md](./GLOSSARY.md)** | You're unsure what a term means (activation, canary, master scope, polymorphic FK, mode A/B, etc.). |
| **[bench.md](./bench.md)** | Anything bench engine — eval / run / result / report / grader. Full command tree + worked flows + anti-patterns. |
| **[SKILL-mini.md](./SKILL-mini.md)** | Compressed (~16KB) bundle for small-context / weak-instruction-following models. Same teaching as SKILL.md in trigger-DSL form. Regenerate via `lore skill compile --target=mini`. |
| **[examples/01-bootstrap.md](./examples/01-bootstrap.md)** | Setting up lore for the first time in an existing project. |
| **[examples/02-capture-correction.md](./examples/02-capture-correction.md)** | User corrected your approach; capture the corrected way as a rule. |
| **[examples/03-multi-repo.md](./examples/03-multi-repo.md)** | Project has multiple repos under one product; scope memories per-repo. |
| **[examples/04-disaster-recovery.md](./examples/04-disaster-recovery.md)** | DB is broken / corrupt / missing — three-tier repair. |
| **[examples/05-decision-record.md](./examples/05-decision-record.md)** | Capturing a non-trivial architectural decision with rationale + revisit criteria. |
| **[examples/06-task-tracking.md](./examples/06-task-tracking.md)** | Mission + tasks across a sprint — populate, query, complete, report. |
| **[examples/07-search-patterns.md](./examples/07-search-patterns.md)** | FTS5 BM25 query syntax (phrase, boolean, prefix), scope flags, jq filtering. |
| **[examples/08-ci-integration.md](./examples/08-ci-integration.md)** | CI: render-diff check, read-only mode, pre-commit / pre-push hooks. |
| **[examples/09-tags-and-comments.md](./examples/09-tags-and-comments.md)** | Polymorphic tags + comments — when to use each vs a standalone entity. |

Read selectively — never dump every file at once. Start here, drill into the specific one you need:

```
unfamiliar with the term     → GLOSSARY.md
intent unclear               → DECISION-TREE.md
intent clear, command fuzzy  → USECASES.md or SCENARIOS.md
command clear, flags fuzzy   → COMMANDS.md
multi-step recipe needed     → PLAYBOOKS.md
got an error                 → ERRORS.md
want a worked transcript     → examples/
```

---

## 3 · Core mental model

| Concept | Meaning | Verb |
|---|---|---|
| **Memory** | Free-form learned fact. "We use Tailwind v4." | `memory add` |
| **Rule** | Hard constraint with severity. `must` blocks, `should` warns, `may` suggests. **Only `must`-severity rules are pinned in `.lore/LORE.md`** (imported by CLAUDE.md); `should`/`may` surface via search. | `rule add --severity=<s>` |
| **Decision** | Architectural choice with rationale. ADR-style. **Not pinned in `.lore/LORE.md`** — surfaces via `lore search`. | `decision add --title=… --body=<why>` |
| **Hotfix** | Loud warning we keep hitting. **Only `critical` + `high` severity hotfixes are pinned in `.lore/LORE.md`** (imported by CLAUDE.md); `medium`/`low` surface via search. Hotfix styling stays loud regardless of where it appears. | `hotfix add --severity=<s>` |
| **Pattern** | Reusable code shape. | `pattern add --name=…` |
| **Playbook** | Reusable procedure. | `playbook add --name=…` |
| **Prompt** | Reusable LLM prompt template. | `prompt add --name=…` |
| **Mission** | Initiative containing tasks. | `mission add "<title>"` |
| **Task** | Discrete work item with status + priority. **Must belong to a tasklist** — `--tasklist=<tlt_id>` is required. | `task add "<title>" --tasklist=tlt_… --priority=…` |
| **TaskList / Plan** | Alternative groupings for tasks. | `tasklist add` / `plan add` |
| **Reminder** | Time-based; supports recurrence. | `reminder add "<msg>" --due=…` |
| **Tag** | Polymorphic label, attaches via `--on-table` + `--on-id`. | `tag attach --on-table=… --on-id=…` |
| **Comment** | Polymorphic discussion, same shape as Tag. | `comment add --on-table=… --on-id=…` |
| **ArchitectureNote, Behaviour, CookbookRecipe, Incident, Suggestion, TastePref, Handoff, Workspace, Workflow** | Same `add | list | show` shape, each captures a different kind of project artifact. | `<kind> add` |

### Cross-cutting relation flags (every `add` accepts the ones its schema supports)

| Flag | Where it applies | Notes |
|---|---|---|
| `--created-by=<act_id>` | every entity with `created_by_actor_id` (memory, rule, decision, hotfix, pattern, playbook, prompt, architecture-note, behaviour, cookbook-recipe, incident, suggestion, tastepref, plan, mission, task, reminder) | Auto-fills from current identity (`identity show`) when omitted. |
| `--supersedes=<id>` | memory, rule, decision, hotfix, pattern | Marks the new row as the replacement for `<id>`. |
| `--repo=<mount\|rep_id>` | memory, rule, decision, hotfix, pattern, task | Scope to a repo within the project (master-scope when omitted). |
| `--mission`, `--tasklist`, `--plan` | task | Parent groupings. `--tasklist` is REQUIRED. |
| `--assigned-to=<act_id>` | task | Assignee. |
| `--validated-by=<act_id>` | memory | Reviewer that validated the entry. |
| `--from=<act_id>`, `--to=<act_id>` | handoff | Sender / receiver. `--from` auto-fills from current identity. |
| `--on-table`, `--on-id` | reminder, tag, comment | Polymorphic target pointer. |

> **Don't confuse `tastepref` with `memory`.** Subjective preferences ("I prefer X", "my taste is Y", "I strongly favor Z") map to `tastepref add`, not `memory add` — preferences have a dedicated entity so they can be rendered separately from facts.

> **Don't confuse `suggestion` with `memory`.** Tentative ideas hedged with "not a commitment", "worth thinking about", "we could probably" → `suggestion add`, not `memory add`. Memory is for known facts; suggestion is for proposed-not-committed ideas.

> **Don't confuse `architecturenote` with `decision`.** Multi-component system descriptions ("backend is X, DB is Y, events are Z") → `architecturenote add` even if the prompt also says "decision". A pure single-choice with rationale ("we decided X because Y") → `decision add`. The word "architecture" wins.

> **Don't confuse `workflow` with `playbook`.** "Deploy workflow / CI workflow / release workflow" → `workflow add`. "The procedure for X" → `playbook add`. Decisive signal is the literal word the user used.

### Scope flags (work on every read/write)

```
--repo=<mount>     scope to a specific repo within the project
--all-repos        all repos in this project
--master-only      project-master rows only (no repo)
--no-inherit       strict scope; don't include broader-scope rows
--read-only        refuse writes (CI safe)
```

### ⚠ Enum validation (don't pass through obviously wrong values)

When the user uses a value that's not in the documented enum, do **not** blindly forward it to the CLI. Surface the constraint and list valid options instead:

| User says | You should |
|---|---|
| `--kind=foobar` (memory) | Note: not valid. Valid kinds: `core`, `retrieved`, `episodic`, `procedural`, `archival`. Don't emit the bad command. (Scoping uses `--repo` / `--master-only`, not a `--scope` flag.) |
| `--severity=critical` | Not in enum. Valid: `must`, `should`, `may`. Map to closest (likely `must`) and confirm. |
| `--priority=blocker` | Not valid. Valid: `low`, `medium`, `high`, `urgent`. Map to `urgent`. |
| `--status=wip` | Not valid. Valid: `todo`, `in_progress`, `blocked`, `done`, `canceled`, `archived`. Map to `in_progress`. |
| `--on-table=task` (singular) | Plural required: `tasks`. Fix silently and proceed. |

CLI will reject bad values with `E_INVALID_INPUT` (and unknown flags like `--scope` with an "unknown flag" error) anyway — catch before emitting.

### Universal output

Every list / show / search command accepts `--json` and emits:

```json
{
  "schema_version": 1,
  "kind": "memory.list" | "task.show" | ...,
  "count": <n>,
  "data": [...] | {...}
}
```

`schema_version: 1` is your **stable contract** — pin parsing to that.

---

## 4 · User-says → command (the map)

This is the most important section in this file. Memorize.

### 🎯 Trigger recognition (read this FIRST)

**ANY user statement of fact, correction, decision, recurring trap, or instruction is a capture trigger** — even when it appears mid-sentence, mid-conversation, surrounded by other prose, or followed by additional unrelated content.

Examples that ARE capture triggers (despite being inside larger utterances):
- "Oh by the way, **remember:** we use UTC for everything." → `memory add`
- "I was thinking about auth today and realized **we never use globals** in this code. Anyway, can we move on to the next task?" → `rule add --severity=must`
- "**Don't forget** — tests run via `task test`, not `go test`." → `memory add`

If you see one of these triggers, **emit the `lore <verb> add` command immediately**, then continue with the rest of the user's request. DO NOT treat the trigger as conversational acknowledgement ("got it!" / "noted!") — that fails silently.

### ⛔ Anti-trigger: do NOT capture when the user is just musing

Capture triggers fire on **commitments**, not exploration. If the user is brainstorming, asking your opinion, or hedging — respond conversationally, do NOT emit `lore … add`.

Hedging signal words that DEFEAT capture triggers (treat the utterance as discussion, not capture):

- **"maybe"**, **"someday"**, **"perhaps"**, **"possibly"**, **"what if"**
- **"I'm wondering"**, **"I'm thinking about"**, **"I'm musing"**, **"not sure but"**
- **"what do you think about"**, **"should we…?"**, **"could we…?"**
- **"we might want to"**, **"we could consider"**, **"eventually"**, **"at some point"**

| User says | You should |
|---|---|
| "Maybe someday we should look into pgvector — what do you think?" | Discuss pros/cons. NO `lore add`. |
| "I'm wondering if we should drop the cron job." | Discuss. NO `lore add`. |
| "Should we move to gRPC?" | Discuss. NO `lore add`. |
| "What if we cached the auth lookups?" | Discuss. NO `lore add`. |
| "My commands keep failing with E_DB_LOCKED — **what's the right way to handle this**?" | Explain retry/backoff in prose + bash. NO `lore hotfix add` — it's a HOW-question, not a recurring-trap report. |
| "**How do I** bootstrap lore?" / "**What's the procedure** for X?" | Answer with the command sequence. NO capture. |

vs. real triggers (emit capture command):

| User says | You should |
|---|---|
| "We decided to use pgvector because…" | `lore decision add` — "decided" + "because" |
| "Remember: we use pgvector now." | `lore memory add` — direct statement of fact |
| "We always use pgvector in this codebase." | `lore rule add --severity=must` — "always" |

**Decisive signal:** is the user *committing to a stance* or *floating an idea*? Only commitments get captured.

#### SIX HARD STOP RULES (a single ❌ here defeats every other signal in the prompt)

1. **Prompt ENDS with `?`** — `"what do you think?"`, `"should we…?"`, `"could we…?"`. The user is asking, not committing. Respond in prose only. No `lore add`, even if the prompt also names a rule-shaped topic.

2. **No proposition stated** — `"Maybe X"`, `"thinking about X"`, `"what about X"`. There's no fact yet. Discuss the topic; don't invent content and capture it.

3. **Explicit no-capture directive** — `"don't capture"`, `"don't save"`, `"just explain"`, `"tell me what this means"`, `"in plain English"`, `"interpret this output"`. The directive OVERRIDES every other signal. Emit zero `lore add` commands.

4. **Sarcasm / mock-tone** — `"yeah right"`, `"TOTALLY"` (all-caps), `"obviously"`, `"lol"`, `"/s"`, `"amirite"` around capture-shaped vocabulary. The user is mocking, not asserting. Don't capture, and don't invert and capture the opposite either. Acknowledge in prose and ask what they actually want.

5. **Counterfactual / hypothetical** — `"if we HAD done X"`, `"had we used X"`, `"in retrospect we should have"`, `"imagine if we"`. These express regret about an unchosen path. They are NOT current facts. Don't capture them; don't invert them into positive claims (that's hallucination). Respond about the lesson in prose.

6. **Invalid enum value in a flag** — user types `--kind=foobar`, `--severity=critical`, `--priority=blocker`, `--on-table=task` (singular instead of plural). DO NOT forward the bad value to the CLI. Surface the constraint in prose, list the valid values, ask for clarification. The output-as-one-bash-block convention is overridden here.

If you find yourself paraphrasing the user's question into a statement and capturing it — STOP. That's a hallucinated capture; the user didn't commit.

### ❌ Forbidden pattern: rule-vs-decision confusion

When the user statement contains the word **"decided"** AND a **"because"** clause, the correct verb is `decision add`, NOT `rule add`.

**Wrong** (two bugs in one line: `rule add` doesn't accept `--body`, and the verb is wrong):
```bash
lore rule add --severity=must "<X>" --body="<Y>"
```

**Right:**
```bash
lore decision add --title="<X>" --body="<Y>"
lore render
```

The exact user phrase "We decided … because …" maps to `decision add`. No exceptions.

### 🛑 Destructive operations — never silent, always confirm + backup first

When the user asks to **delete**, **wipe**, **drop**, **rm -rf**, **start fresh**, or **nuke** any lore data (the `.lore/` directory, the DB, all rules, all memories, etc.), DO NOT silently execute. The protocol is:

1. Warn the user the action is irreversible.
2. Emit `lore backup` first so they have an out.
3. Suggest the soft-delete equivalent (`lore <kind> archive`) where it applies.
4. Only emit the destructive command after explicit confirmation, AND with `--confirm` where supported.

| User says | You should emit |
|---|---|
| "Delete the .lore dir entirely — wipe everything." | `lore backup` first, then warn "This is irreversible — confirm and I'll proceed." Do NOT silently `rm -rf .lore`. |
| "Drop all rules." | `lore backup`, then `lore rule list --json` to review, then `lore rule archive R-N` (soft) or `lore rule delete R-N --confirm` (hard). |
| "Reset the DB." | `lore backup`, then `lore doctor`, then `lore repair` if needed — never just `rm`. |
| "Start completely fresh." | Same as "delete": backup first, confirm, then proceed. |

Never emit a bare `rm -rf .lore`, `DROP TABLE`, or destructive SQL.

### 🔑 Critical discrimination rules (apply BEFORE choosing a verb)

Five rules, priority order. The presence of specific signal words is decisive — synthesize from these, do NOT copy any few-shot example's verb verbatim.

1. **"we decided X because Y"** / "X because of Y" / "X so that Y" → ALWAYS `lore decision add --title="<X>" --body="<Y>"` (NEVER `rule add`)
2. **"we always X"** / "we must X" / "we never X" / "don't X" → ALWAYS `lore rule add --severity=must` (no rationale = rule, not decision)
3. **"remember"** / "don't forget" / "save this" / bare fact statement → ALWAYS `lore memory add` (free-form, no severity, no rationale)
4. **"we keep hitting X"** / "watch out for X" / "this bit us" / recurring trap → ALWAYS `lore hotfix add --severity=high`
5. **"we tried X and it broke"** / post-mortem → ALWAYS `lore incident add --title=<t> --body=<what-happened>`

### ⚠ Anti-pattern: do NOT roleplay lore's output

When the user asks you to add a task / capture a rule / etc., emit the **literal CLI command** for the user (or shell tool) to run. Do NOT simulate the post-run output.

**Wrong** (the model pretends it ran the command):
```
Task added:
    tsk_<id> refactor the auth middleware
        priority: medium
        status: todo
        created: 2026-05-11
```

**Right** (emit the actual command):
```bash
lore task add "refactor the auth middleware" --tasklist=tlt_default --priority=medium
lore render
```

The first form looks helpful but persists nothing — the user is left to wonder whether the task is real. The second form is the contract.

### Knowledge capture

**Body shape:** Every `add` command takes the body as `--body=<text>` (canonical) or piped via stdin. **Positional args are not accepted for body.** Title (when required) is always `--title=<text>`. No mixing positional and flags.

| User says | Run |
|---|---|
| "remember this", "don't forget", "save this" | `lore memory add --body="<the fact>"` |
| "we always X", "we must X" | `lore rule add --severity=must --body="<X>"` |
| "we should X" (soft) | `lore rule add --severity=should --body="<X>"` |
| "we never X", "don't X" | `lore rule add --severity=must --body="Do not <X>"` |
| "we decided X because Y" | `lore decision add --title="<X>" --body="<Y>"` |
| "we keep hitting X", "watch out for X" | `lore hotfix add --severity=high --title="<X>" --body="<longer body>"` |
| "this pattern: …" | `lore pattern add --title="<n>" --body="<code>"` |
| "the procedure for X is …" | `lore playbook add --title="<n>" --body="<steps>"` |
| "use this prompt: …" | `lore prompt add --title="<n>" --body="<prompt>"` |
| "the architecture is …" | `lore architecturenote add --title="<t>" --body="<b>"` |
| "we prefer X" / "I like X" | `lore tastepref add --body="<X>"` |
| "for X, do: …" (recipe) | `lore cookbookrecipe add --title="<X>" --body="<steps>"` |
| "always do X when …" (behaviour) | `lore behaviour add --title="<n>" --body="<body>"` |
| "comment on dec_<id>: …" | `lore comment add --on-table=decisions --on-id=dec_<id> --body="<text>"` |
| "snapshot the project at …" | `lore snapshot add --title="<t>" --body="<body>"` |

**Stdin alternative** (useful for long bodies, file content, multi-line):
```bash
cat ARCHITECTURE.md | lore architecturenote add --title="overview"
echo "$LONG_BODY" | lore memory add
```

**Exceptions (positional is *title*, not body — title is the primary input here):**

| Entity | Shape |
|---|---|
| `task add` | `lore task add "<title>" --tasklist=<id> [--body="..."]` |
| `mission add` | `lore mission add "<title>" [--body="..."]` |

### Revising existing captures (don't create duplicates)

When the user wants to flag, revisit, or update an EXISTING capture, do NOT create a new one. Either edit/archive the existing entity or attach a comment to it:

| User says | Run |
|---|---|
| "Flag D-N for re-evaluation / revisit later" | `lore comment add --on-table=decisions --on-id=D-N --body="REVISIT: <why>"` (D-N stays active) |
| "Mark mission M-N as done / ship M-N" | `lore mission done M-N` (mission has its own `done` verb — NOT `task done` for M-N IDs) |
| "Cancel T-N because <reason>" | `lore task cancel T-N` + `lore comment add --on-table=tasks --on-id=T-N --body="cancelled: <reason>"` (CLI has no native --reason flag) |
| "Unarchive R-N" | `lore rule unarchive R-N` (every entity supports unarchive after archive) |
| "Update D-N's body" / "Edit decision D-N" | `lore decision edit D-N --body="<new>"` (direct mutation; no need to show first if user provided the new content) |
| "Update the X prompt template" | `lore prompt edit <X> --body="<new>"` |
| "Update pattern X" | `lore pattern edit <X> --body="<new>"` |
| "Bulk archive mem_<id>, mem_<id>, mem_<id>" | One `lore memory archive` call per ID (no bulk syntax — emit 3 commands) |
| "Mission M-N is complete / ship M-N" | `lore mission done M-N` (mission has its own `done` verb; do NOT use `task done` for M-N IDs) |
| "Done with mission M-N" | `lore mission done MS-N` (mission/handoff have no `archive` — use status transitions: `mission done`, `mission cancel`, `mission pause`/`resume`, `handoff ack`) |
| "Flag D-N for re-eval (don't archive)" | `lore comment add --on-table=decisions --on-id=D-N --body="REVISIT: <why>"` (D-N stays active; do NOT create a new decision) |

### Correction / confirmation (revising captured knowledge)

When the user asks to **revise**, **scratch**, **reverse**, or **update** an existing rule, the workflow is `search → archive → (optionally add new)`. Emit it as ONE atomic shell script using `$(…)` command substitution — do NOT split into "Step 1 / Step 2" commands, because weak models stop after the search.

```bash
RID=$(lore rule search "globals" --json | jq -r '.data[0].code')
lore rule archive "$RID"
lore rule add --severity=should "A single global request-id context is allowed for tracing"
lore render
```

| User says | Run |
|---|---|
| "Scratch the previous rule about X" | `rule search "X"` → `rule archive R-N` (atomic, via `$(…)`) |
| "Update what we said about X" | `rule search "X"` → `rule edit R-N` or archive + add new |
| "Reverse the rule about X" | `rule search "X"` → `rule archive R-N` → add new with opposite content |

| User says | Run |
|---|---|
| "no, that's not how we do it" | Capture the correct way: `rule add --severity=must` |
| "actually we have a rule against that" | The rule existed but wasn't followed. Search first: `lore rule list \| grep -i …`. If absent, add it. |
| "yes, that's the right call" + non-obvious choice | `lore decision add` to lock in the rationale. |
| "we tried X and it broke" | `lore incident add --title="<X>" --body="<what happened>"` |

### 🔗 Polymorphic attach (tag / comment)

`tag` and `comment` are **polymorphic** — they attach to any other entity via `--on-table=<table-name>` + `--on-id=<id>`. **The on-table value is the literal SQL table name, NOT the singular CLI noun.** Snake_case where the schema uses it:

| CLI noun | `--on-table=` value |
|---|---|
| memory | `memories` |
| rule | `rules` |
| decision | `decisions` |
| hotfix | `hotfixes` |
| pattern | `patterns` |
| playbook | `playbooks` |
| prompt | `prompts` |
| architecture-note | `architecture_notes` ⚠ snake_case |
| behaviour | `behaviours` |
| cookbook-recipe | `cookbook_recipes` ⚠ snake_case |
| incident | `incidents` |
| suggestion | `suggestions` |
| tastepref | `taste_prefs` ⚠ snake_case |
| task | `tasks` |
| tasklist | `task_lists` ⚠ snake_case |
| mission | `missions` |
| plan | `plans` |
| reminder | `reminders` |
| handoff | `handoffs` |
| snapshot | `snapshots` |
| techdoc | `tech_docs` ⚠ snake_case |

`--on-id` accepts the opaque ID (`tsk_…`, `mem_…`, `dec_…`, `tlt_…`, `msn_…`, `pat_…`, `snp_…`, etc.) returned by the corresponding `list` / `add` command.

| User says | Run |
|---|---|
| "pre-create a tag called X (without attaching yet)" | `lore tag add --name=X` (standalone tag entity — distinct from `tag attach`) |
| "tag tsk_<id> as backend" | `lore tag attach --on-table=tasks --on-id=tsk_<id> --name=backend` |
| "remove the backend tag from tsk_<id>" | `lore tag detach --on-table=tasks --on-id=tsk_<id> --name=backend` |
| "tag this memory mem_<id> 'security'" | `lore tag attach --on-table=memories --on-id=mem_<id> --name=security` |
| "add a comment on dec_<id>: foo" | `lore comment add --on-table=decisions --on-id=dec_<id> --body="foo"` |
| "comment on hotfix H-2 that …" | `lore comment add --on-table=hotfixes --on-id=H-2 --body="…"` |
| "show all tags on tsk_<id>" | `lore tag list --on-table=tasks --on-id=tsk_<id> --json` |
| "what's been commented on dec_<id>?" | `lore comment list --on-table=decisions --on-id=dec_<id> --json` |

**Common slip:** singular table name (`task`, `memory`, `decision`). Always plural.

### Project management

| User says | Run |
|---|---|
| "add a task to X" | `lore task add "<X>" --tasklist=<tlt_id> --priority=medium` |
| "high priority task: X" | `lore task add "<X>" --tasklist=<tlt_id> --priority=high` |
| "urgent: X" | `lore task add "<X>" --tasklist=<tlt_id> --priority=urgent` |
| "track an initiative: X" | `lore mission add "<X>" --target=YYYY-MM-DD` |
| "what tasks are open?" | `lore task list --json` (default already hides done + cancelled; use `--all` for full history, `--status=X` for explicit filter) |
| "show everything including done/cancelled" | `lore task list --all --json` |
| "start work on tsk_<id>" | `lore task start tsk_<id>` |
| "mark tsk_<id> done" | `lore task done tsk_<id>` |
| "cancel tsk_<id>" | `lore task cancel tsk_<id>` |
| "move tsk_<id> to tasklist L-5" | `lore task edit tsk_<id> --tasklist=tlt_<id>` (or any other field via `edit`) |
| "reassign tsk_<id> to act_xx" | `lore task edit tsk_<id> --assigned-to=act_xx` |
| "detach tsk_<id> from its mission" | `lore task edit tsk_<id> --clear-mission` |
| "pause mission msn_<id>" | `lore mission pause msn_<id>` |
| "resume mission msn_<id>" | `lore mission resume msn_<id>` |
| "ack handoff hnd_<id>" / "I've processed handoff hnd_<id>" | `lore handoff ack hnd_<id>` |
| "remind me on YYYY-MM-DD to X" | `lore reminder add "<X>" --due=YYYY-MM-DD` |
| "every week, remind me to X" | `lore reminder add "<X>" --due=<next> --recurrence=7d` |

### Lifecycle / cleanup

| User says | Run |
|---|---|
| "archive mem_<id>" / "stop showing mem_<id>" | `lore memory archive mem_<id>` (works for every entity: rule, decision, hotfix, pattern, playbook, prompt, project, repo) |
| "unarchive mem_<id>" | `lore memory unarchive mem_<id>` |
| "mem_<id> is no longer true as of today" (bitemporal — keep history) | `lore memory invalidate mem_<id>` (sets `valid_until=now`; preferred over archive when you want to remember WHEN it became wrong) |
| "supersede mem_<id> with new memory X" | `lore memory add "X" --supersedes=mem_<id>` (preserves chain) |
| "edit mem_<id>" / "fix typo in mem_<id> body" | `lore memory edit mem_<id> --body="..."` (no chain — use `--supersedes` instead if audit trail matters) |
| "archive project PRJ-X" / "retire repo REP-Y" | `lore project archive PRJ-X` / `lore repo archive REP-Y` |

### Directive (lock the file against agent drift)

| User says | Run |
|---|---|
| "tell the agent to use lore, not inline CLAUDE.md" / "stop the AI from writing memories into the file" | `lore directive install` (idempotent; default target CLAUDE.md at cwd) |
| "drop the directive in AGENTS.md too" | `lore directive install --target=AGENTS.md` (or `--target=CLAUDE.md,AGENTS.md`) |
| "what does the directive block say?" | `lore directive show` (prints to stdout, no write) |
| "strip the directive block" | `lore directive remove [--target=...]` |

### Retrieval

Default read scope = current repo + inherits project-master rows. Use scope-widening flags when the user asks for broader coverage:

| User says | Add this flag |
|---|---|
| "across every repo / all repos / not scoped to one repo" | `--all-repos` |
| "master-only / project-master rules / org-wide" | `--master-only` |
| "strict, only this scope, no inheritance" | `--no-inherit` |

| User says | Run |
|---|---|
| "what do we know about X?" | `lore memory search "X" --json` |
| "find any rule/task/decision about X" | `lore <kind> search "X" --json` — works on every entity (memory, rule, decision, hotfix, pattern, playbook, prompt, architecturenote, behaviour, cookbookrecipe, incident, suggestion, tastepref, snapshot, handoff, mission, task, tasklist, plan, workflow, workspace, techdoc, comment) |
| "search across all task fields" | `lore task search "auth*"` — FTS5 BM25 across title + body; relations eager-loaded in JSON |
| "wildcard / phrase / boolean search" | FTS5 syntax: `auth*` (prefix), `"connection pool"` (phrase), `redis OR cache`, `auth NOT login` |
| "restrict search to specific columns" | `lore task search "X" --column=title,body` |
| "list all memories" / "what memories exist?" | `lore memory list [--archived] [--json]` |
| "show mem_<id>" | `lore memory show mem_<id> [--json]` (includes archived_at, valid_until, superseded_by) |
| "is search working / how healthy is the index" | `lore search status` — per-entity row counts + drift detection |
| "rebuild the search index" | `lore search rebuild [--kind=<entity>]` |
| "who modified X / audit trail for X / when was X last changed" | `sqlite3 .lore/lore.db "SELECT tx_at,actor_id,op,entity_id FROM audit_log WHERE entity_id='X' OR entity_table='X' ORDER BY tx_at DESC LIMIT 20"` (raw SQL — no dedicated CLI verb yet) |
| "find anything starting with auth" (prefix) | `lore memory search "auth*" --json` |
| "things mentioning X or Y" | `lore memory search "X OR Y" --json` |
| "things mentioning X or Y but NOT Z" | `lore memory search "(X OR Y) NOT Z" --json` (use FTS5 NOT; **don't** post-filter with `jq | select(... | not)`) |
| "exact phrase 'connection pool'" | `lore memory search '"connection pool"' --json` (quoted = adjacent words in order) |
| "what rules exist?" | `lore rule list --json` |
| "what decisions about X?" | `lore decision list --json \| jq '.data[] \| select(.title \| test("X";"i"))'` |
| "why did you do X?" | `lore why-context --last-render --rendered` |
| "show me mem_<id>", "show me tsk_<id>", "show me dec_<id>" | `lore <kind> show <id>` |
| "show all rules INCLUDING archived" | `lore rule list --include-archived --json` |
| "rules under project X (cross-project peek)" | `lore rule list --project=X --json` (one-shot override, doesn't change cwd project) |

### Render + introspect

| User says | Run |
|---|---|
| "regenerate my context" / "re-render" | `lore render` (writes `.lore/LORE.md` + `@import` pointer into CLAUDE.md; hybrid: only directive + must-rules + critical/high hotfixes pinned) |
| "what would render produce?" | `lore render --dry-run` (prints the `.lore/LORE.md` body) |
| "stitch the pointer into AGENTS.md instead" | `lore render --target=AGENTS.md` |
| "put the generated file somewhere else" | `lore render --out=docs/LORE.md` |
| "write the generated file only, leave CLAUDE.md alone" | `lore render --no-pointer` |
| "render just one repo's slice" | `lore render --repo=<mount> --out=<mount>/.lore/LORE.md --target=<mount>/CLAUDE.md` |
| "what did the AI actually see?" | `lore why-context --last-render` |

### What's pinned in `.lore/LORE.md` vs searched on demand

`lore render` follows a **hybrid model**. The pinned content goes into the generated `.lore/LORE.md` (which CLAUDE.md `@import`s); it's intentionally kept small so it doesn't bloat as project knowledge accumulates:

| Entity | Pinned in `.lore/LORE.md` | Searched on demand via `lore search` |
|---|---|---|
| Directive | always | — |
| Rules | `severity=must` | `severity=should` / `may` |
| Hotfixes | `severity=critical` + `high` | `severity=medium` / `low` |
| Memories, decisions, patterns, architecturenotes, taste prefs, playbooks, … | — | always |

The agent's **directive step 5** (rendered into `.lore/LORE.md`, reached via the CLAUDE.md `@import`) mandates `lore search "<keywords>"` before substantive responses — that's how the non-pinned content reaches the agent. Cite hits by ID (`per dec_<id>, …`).

### Benchmark / evaluation

| User says | Run |
|---|---|
| "set up a benchmark eval for X / author a bench task for rule R-N" | `lore bench eval add --category=… --link=<kind>:<id> --prompt-file=- --grader-kind=programmatic --grader-cmd='…'` — **META, not capture**. Do NOT emit `rule add` for "set up an eval *for* rule R-N" requests; the user wants to test the rule, not re-capture it. |
| "set up a benchmark task" | `lore bench eval add --category=… --link=rule:R-N --prompt-file=… --grader-kind=…` |
| "list benchmark tasks" | `lore bench eval list [--category=…] [--json]` |
| "show task E1-001" | `lore bench eval show E1-001 --json` |
| "edit the grader" | `lore bench eval edit E1-001 --grader-cmd='…'` |
| "soft-delete a task" | `lore bench eval archive E1-001` |
| "clone a task to tweak" | `lore bench eval duplicate E1-001 --as=E1-001b` |
| "import YAML test set" | `lore bench eval import --from=bench/tasks/` |
| "export task set for git" | `lore bench eval export --to=bench/tasks/` |
| "run the benchmark" | `lore bench run start --model=… --runs-per-arm=3 --parallel=8` |
| "free local benchmark" | `lore bench run start --model=ollama:qwen3-coder:latest --parallel=12` |
| "live stats during a run" | `lore bench result stats` (no args = latest) |
| "summarize the latest run" | `lore bench report summary` (no args = latest) |
| "compare runs" | `lore bench report compare <a> <b>` |
| "summary of run X (or latest)" | `lore bench report summary [<X>\|--latest]` |
| "trend over 30 days, by model" | `lore bench report trend --since=30d --by-model` |
| "is the delta significant?" | `lore bench report analyze <run>` (paired t-test, Cohen's d) |
| "per-category breakdown" | `lore bench report by-category <run>` |
| "which evals regressed?" | `lore bench report regressions --since=last-week` |
| "test grader against a sample file (no LLM)" | `lore bench grader test E1-001 --output-file=<path>` |
| "why did result X fail (grader trace)" | `lore bench grader debug <result-id>` |
| "audit graders for flakiness" | `lore bench grader audit` |
| "regrade after fixing the grader (free)" | `lore bench result regrade --run=<run>` |
| "replay (re-LLM-call) one result" | `lore bench result replay <result-id>` ($, vs free `regrade`) |
| "compile a small-model skill bundle" | `lore skill compile --target=mini --model=claude-sonnet-4-6` |

### Health / recovery

| User says | Run |
|---|---|
| "who am I / whoami / what identity" | `lore identity show` |
| "anonymize captures / hide my name" | `lore identity anonymize` (toggles anon mode; file stays) |
| "set my identity to X" | `lore identity set --actor=X` |
| "unset identity / go back to auto-detected" | `lore identity unset` (REMOVES ~/.lore/identity.toml entirely; distinct from `anonymize`) |
| "is everything OK?" | `lore doctor` |
| "what does doctor check?" | `lore doctor --help` (or `--json` to see structured output of checks) |
| "back up the DB" | `lore backup` |
| "back up to a specific path" | `lore backup --out=<path>` |
| "DB is broken" | `lore repair --tier=2 --confirm` (tier-1 = FTS rebuild, tier-2 = restore from latest backup, tier-3 = bootstrap empty DB) |
| "restore from a specific backup file" | `lore restore <backup-path> --confirm` — POSITIONAL backup-path (NOT `--from=`); never `repair --tier=3` for restore-from-file |
| "file a bug" | `lore support-bundle --out=/tmp/bundle.tar.gz` (then attach to issue) |
| "list every error code" | `lore errors list` |
| "what version am I on?" | `lore version` |

If the user uses different phrasing, the principle is: **classify the intent (capture / track / retrieve / render / heal) and pick from the matching subtable.** See [USECASES.md](./USECASES.md) for the full intent table.

### CLI gaps (no native verb yet — use these workarounds)

| User asks for | Status | Workaround |
|---|---|---|
| `lore task block <T-N>` | No native verb (despite `blocked` being a valid status enum) | `lore task edit T-N --status=blocked` (works via universal `edit`) |
| `lore audit` CLI | No native verb yet | Raw SQL: `sqlite3 .lore/lore.db "SELECT … FROM audit_log WHERE …"` |
| Cancel-with-reason as one verb | `task cancel` has no `--reason` flag | Two-cmd: `task cancel T-N` + `comment add --on-table=tasks --on-id=T-N --body="cancelled: <reason>"` |

### Hard delete (escape hatch)

| User says | Run |
|---|---|
| "delete rul_<id> / wipe that rule / nuke mem_<id>" | `lore <kind> delete <id> --confirm` |
| "actually I want soft-delete" | `lore <kind> archive <id>` (reversible via `unarchive`) where archive exists; otherwise delete is the only option |

`<kind> delete <id>` works on 25 entities. **Requires `--confirm`** to fire — accidentally typing it without the flag returns an error pointing at archive. Soft-archive should be preferred whenever the entity supports it (memory, rule, decision, hotfix, pattern, playbook, prompt, snapshot, project, repo); delete is the escape hatch for "I captured a secret" / "I need it gone for real."

> **Closed gaps** (do NOT re-add as workarounds):
> - "Attach tasks to a tasklist" → `lore task edit T-N --tasklist=<tlt_id>` (covered by universal `edit`)
> - "Move task between tasklists / missions / plans" → `task edit T-N --tasklist=… --mission=… --plan=…` (or `--clear-mission` to detach)
> - "Batch reject learn candidates" → `lore learn reject --all` or `--ids=a,b,c`
> - "`learn list --json` not actually JSON" → fixed; emits `{schema_version,kind,data}` envelope
> - "`memory search ""` returns null" → fixed; emits `results: []`
> - "`learn-from docs --paths=<dir>` silently skips directories" → fixed; recurses for `*.md`
> - "Archive memory / rule / decision / etc." → `lore <kind> archive <id>` / `unarchive <id>` (9 entities supported)
> - "Per-entity full-text search" → `lore <kind> search "<q>"` works on 23 entities (FTS5, BM25, stemming, snippets, eager-loaded relations in JSON)

---

## 5 · The capture-render-read loop

```
        ┌───────────────────────────────────────────────┐
        │  USER teaches you / corrects you / decides   │
        └──────────────────────┬────────────────────────┘
                               │
                               ▼
        ┌───────────────────────────────────────────────┐
        │  YOU run:  lore <kind> add "..."          │   ← capture
        │  (memory / rule / decision / hotfix / …)      │
        └──────────────────────┬────────────────────────┘
                               │
                               ▼
        ┌───────────────────────────────────────────────┐
        │  YOU run:  lore render                    │   ← refresh
        │  → writes .lore/LORE.md + @import in CLAUDE.md │
        └──────────────────────┬────────────────────────┘
                               │
                               ▼
        ┌───────────────────────────────────────────────┐
        │  FUTURE SESSION (you or another AI) reads    │   ← consume
        │  CLAUDE.md → context loop closes              │
        └───────────────────────────────────────────────┘
```

**If you skip step 2 (render), the captured knowledge is invisible.** Always end a capture turn with `lore render`.

---

## 6 · Workflow phases (numbered for cross-reference)

These are the major phases. Each links to detailed instructions in [PLAYBOOKS.md](./PLAYBOOKS.md).

<a name="workflow-p1"></a>
### P1 — Bootstrap (new project)
The user wants to start using lore. Use existing CLAUDE.md / AGENTS.md / .cursorrules as the seed. **Full recipe: [PLAYBOOKS.md § P1](./PLAYBOOKS.md), worked example: [examples/01-bootstrap.md](./examples/01-bootstrap.md).**

For default sources (CLAUDE.md, AGENTS.md, README.md, `.ai/**/*.md`):
```bash
lore learn-from docs
```

For explicit source files, use `--paths` (comma-separated):
```bash
lore learn-from docs --paths=docs/CLAUDE.md,docs/CONVENTIONS.md,.ai
```

### P2 — Capture correction
User just corrected you. Write a rule. **Full recipe: [PLAYBOOKS.md § P2](./PLAYBOOKS.md), worked example: [examples/02-capture-correction.md](./examples/02-capture-correction.md).**

### P3 — Capture decision
User just made an architectural decision and gave the rationale. Write a decision. **Recipe: [PLAYBOOKS.md § P3](./PLAYBOOKS.md).**

### P4 — Capture recurring warning
"Ugh, we hit this AGAIN." Write a hotfix. **Recipe: [PLAYBOOKS.md § P4](./PLAYBOOKS.md).**

### P5 — Multi-repo scoping
Platform with web + admin + api repos sharing one project. **Recipe: [PLAYBOOKS.md § P5](./PLAYBOOKS.md), worked example: [examples/03-multi-repo.md](./examples/03-multi-repo.md).**

### P6 — Shared DB across sibling projects (Mode B)
Multiple unrelated repos sharing `~/.lore/shared.db`. **Recipe: [PLAYBOOKS.md § P6](./PLAYBOOKS.md).**

### P7 — Disaster recovery
DB corrupt / missing / truncated. **Recipe: [PLAYBOOKS.md § P7](./PLAYBOOKS.md), worked example: [examples/04-disaster-recovery.md](./examples/04-disaster-recovery.md).**

### P8 — Mission + task tracking
Group tasks under an initiative. **Recipe: [PLAYBOOKS.md § P8](./PLAYBOOKS.md).**

### P9 — Reminder for periodic review
Once-off, recurring, or attached to a specific entity. **Recipe: [PLAYBOOKS.md § P9](./PLAYBOOKS.md).**

### P10 — Inspect what the AI is seeing
"Why is X missing from the context?" **Recipe: [PLAYBOOKS.md § P10](./PLAYBOOKS.md).**

### P11 — Migrate from .cursorrules / AGENTS.md
One-shot ingestion. **Recipe: [PLAYBOOKS.md § P11](./PLAYBOOKS.md).**

### P12 — CI integration
Read-only mode, render-diff check. **Recipe: [PLAYBOOKS.md § P12](./PLAYBOOKS.md).** Worked example: [examples/08-ci-integration.md](./examples/08-ci-integration.md).

### P13 — Onboard a new teammate
Bring a new dev up to speed via committed CLAUDE.md + their own local DB. **Recipe: [PLAYBOOKS.md § P13](./PLAYBOOKS.md).**

### P14 — Promote a memory to a rule
Soft fact turns out to be a hard constraint. **Recipe: [PLAYBOOKS.md § P14](./PLAYBOOKS.md).**

> **ALL 4 STEPS REQUIRED — do NOT skip the show.** The first step (`lore memory show M-N`) fetches the actual body so the rule text is faithful to the original — never invent or paraphrase the rule content. Order: `memory show` → `rule add` → `memory archive` → `render`.

### P15 — Demote a rule to a memory
Rule was too strict; relax it. **Recipe: [PLAYBOOKS.md § P15](./PLAYBOOKS.md).**

> **ALL 4 STEPS REQUIRED — do NOT skip the show.** Same pattern as P14 in reverse: `rule show` → `memory add` → `rule archive` → `render`.

### P16 — Periodic review checklist
Weekly: doctor + backup + reminders + stale tasks + render + commit. **Recipe: [PLAYBOOKS.md § P16](./PLAYBOOKS.md).**

### P17 — Export project knowledge to JSON
Bulk export every entity kind to JSON for portability or fine-tuning. **Recipe: [PLAYBOOKS.md § P17](./PLAYBOOKS.md).**

### P18 — Audit log forensics
"When did this rule get added, by whom?" — direct SQL against `audit_log`. **Recipe: [PLAYBOOKS.md § P18](./PLAYBOOKS.md).**

### P19 — Render only a slice (budget control)
Per-repo / master-only / tag-filtered renders. **Recipe: [PLAYBOOKS.md § P19](./PLAYBOOKS.md).**

### P20 — Migrate to a new lore version
Backup → check schema → auto-migrate → verify. **Recipe: [PLAYBOOKS.md § P20](./PLAYBOOKS.md).**

---

## 7 · Command tree (one-liner per command)

Full reference with flags + examples + JSON output: [COMMANDS.md](./COMMANDS.md).

```
lore
├── init                       create new project at cwd (Mode A)
├── project
│   ├── list                   projects in current DB
│   ├── show <id|name>         project + eager-loaded repos
│   ├── shared-init            create Mode B (shared DB) project
│   └── shared-list            list projects in a shared DB
├── repo
│   ├── add <mount>            register a repo within current project
│   └── list                   repos in current project
├── memory
│   ├── add <body>             persist a free-form fact
│   ├── list                   newest-first listing
│   ├── search <q>             FTS5 BM25; LIKE fallback
│   └── show <id>              single memory
├── rule    add|list|show      hard constraints with severity + activation
├── decision add|list|show     ADR-style choices with rationale
├── hotfix  add|list|show      loud recurring warnings (never truncated)
├── pattern, playbook, prompt, architecturenote, behaviour,
│   cookbookrecipe, incident, suggestion, tastepref,
│   plan, tasklist, workflow, workspace, handoff
│                              all share add|list|show shape
├── mission add|list|done|show containers for tasks
├── task    add|list|start|done|cancel|show
│                              discrete work items
├── reminder add|list|done     time-based; recurrence-aware
├── tag      add|list|attach|detach
│                              polymorphic labels
├── comment  add|list          polymorphic discussions
├── render                     compile .lore/LORE.md from DB + @import pointer in CLAUDE.md
├── why-context                introspect last render
├── learn-from docs            ingest existing markdown
├── learn list|promote|reject  review learn candidates
├── doctor                     health check, exit 0/1/2
├── backup                     online SQLite backup
├── restore <path>             replace current DB
├── repair --tier=1|2|3        disaster recovery
├── identity show|set          actor identity (8-step fallback chain)
├── support-bundle             sanitized incident-report tarball
├── run, session, querylog, renderhistory
│                              read-only inspectors
├── version                    binary + schema + bundle + plugin + mcp versions
└── errors list                full E_* code registry
```

**Universal flags** (work on every command):
`--db PATH`, `--project NAME`, `--repo NAME`, `--read-only`, `--json`, `--color auto|always|never`.

---

## 7b · Entities with NO CLI (intentionally)

The DB has ~63 ent entities. ~32 have user-facing CLI commands (above). The rest are **internal-only** — managed by the system, not the user. Do NOT instruct the user to `add/list/show` these:

| Internal entity | Why no CLI |
|---|---|
| `audit_log` | append-only; querying is forensic (direct SQL or v0.2 `audit search`) |
| `schema_migration`, `dbconfig`, `projectconfig`, `config` | self-managed at startup |
| `knowledge_ref`, `memory_code_ref`, `rule_verifier_ref` | join tables |
| `mount_alias` | renames are via `repo` cmd; alias is internal |
| `code_file`, `code_symbols` | v0.2 code-indexing pipeline |
| `interventionmetric`, `taskview`, `knowledge_revision`, `activityarchive` | analytics + history; read via SQL or v0.2 |
| `assemble_run`, `assemble_citation` | retrieval pipeline runs |
| `compress_run`, `learn_run` | background runs |
| `bench_run`, `bench_eval`, `bench_result` | benchmark harness (v0.2) |
| `external_source` | plugin protocol (v0.2) |
| `identity_profile` | actor backing; manage via `identity` cmd |
| `pii_pattern`, `trusted_plugin` | security registries (v0.2) |
| `actor` | resolved via 8-step chain; `identity` cmd is the surface |
| `entitytag` | backing for `tag attach`; never touched directly |
| `learncandidate` | exposed via `learn` cmd group |
| `draft` | v0.2 staging |
| `tech_doc`, `tech_doc_page` | schema exists; CLI lands in v0.2 |
| `snapshot` | schema exists; CLI lands in v0.2 |

If the user asks about one of these by name, point them at the underlying user-facing command (e.g., `tag attach` for entity_tags) or note that it's deferred to v0.2.

---

## 8 · JSON output contract

Every `list` / `show` / `search` accepts `--json`:

```json
{
  "schema_version": 1,
  "kind": "<entity>.<list|show|search>",
  "count": 3,
  "data": [
    { /* entity-specific fields */ }
  ]
}
```

Eager-loaded relationships (per spec):

| Command | Eager-loads |
|---|---|
| `mission show --json` | `tasks[]` (1:N) |
| `mission list --json` | `tasks[]` per mission |
| `plan show --json` | `tasks[]` |
| `tasklist show --json` | `tasks[]` |
| `project show --json` | `repos[]` |
| `run show --json` | `steps[]` |
| (v0.2) `techdoc show --json` | `pages[]` — schema exists, CLI deferred |

**Rely on `kind` and `schema_version` — never on field order.** New fields may be added in v0.2 without bumping schema_version (additive); removed fields bump it.

---

## 9 · Error handling

Every error has a stable `E_*` code. **Match on code, not English message.**

Standard error envelope (in `--json` mode):

```json
{
  "error": {
    "code": "E_<UPPER_SNAKE>",
    "message": "human-readable",
    "hint":    "what the user should do",
    "doc_url": "optional"
  }
}
```

Get the full registry programmatically: `lore errors list --json` (returns 37 codes).

Full recovery decision tree: [ERRORS.md](./ERRORS.md).

Quick reactions:

| Code | Reaction |
|---|---|
| `E_DB_LOCKED` / `E_LOCK_HELD` | Wait + retry, or kill the other process. |
| `E_DB_CORRUPT` / `E_DB_NOT_FOUND` | `lore repair --tier=2 --confirm`. |
| `E_NOT_PROJECT_ROOT` | Either `cd` to the project, or use `--db=<path>`. |
| `E_AMBIGUOUS_PROJECT` | Both `lore.db` and `lore.toml` exist — remove one. |
| `E_SECRET_DETECTED` | Strip the secret. `--allow-secrets` only with explicit user consent. |
| `E_ROOT_REFUSED` | Set `MINI_ALLOW_ROOT=1` (only if user explicitly wants). |
| `E_SYMLINK_DB` | DB path is a symlink — delete + restore from backup. |
| `E_READ_ONLY` | Drop `--read-only` and unset `LORE_READ_ONLY`. |
| `E_NETWORK_FS` | iCloud/Dropbox/NFS — move DB to a local disk. |
| `E_NOT_IMPLEMENTED` | v0.2 feature — inform user, don't retry. |

---

## 10 · Anti-patterns (don't do these)

| Anti-pattern | Why bad | Do instead |
|---|---|---|
| Capturing in your reply but not calling `lore` | Dies with the session | `lore memory add` |
| Calling `lore memory add` but skipping `lore render` | Knowledge is in DB but invisible to AI | Always `render` after capture |
| Adding a rule for a one-off preference | Pollutes future sessions | Use `memory add` for soft preferences; `rule add` for true constraints |
| Mixing repo-scoped + master memories without `--repo` | Memories leak into other repos' renders | Always pass `--repo=<mount>` for repo-specific captures |
| Bypassing secret-scrub without user consent | Leaks credentials into DB | Refuse loudly; require explicit `--allow-secrets` only with user OK |
| Storing tasks that map 1:1 to GitHub issues | Duplicate state, drift | Use tags + comments to cross-link, don't mirror |
| Using `rule` when `decision` is right | Rules say WHAT, decisions say WHY | Use `decision add` when the rationale matters more than the conclusion |
| Adding rules without severity | Defaults to `must` (blocking) | Always set `--severity=must|should|may` explicitly |
| Calling `repair --tier=3` first | Destroys data | Try `--tier=1`, then `--tier=2`. Tier 3 is the LAST resort. |

---

## 11 · Hard rules (non-negotiable)

1. **Capture is cheap. Forgetting is expensive.** When unsure, write a memory.
2. **Render after every write.** Otherwise the AI doesn't see it.
3. **Never bypass secret-scrub silently.** `--allow-secrets` requires explicit user consent.
4. **Read-only mode is honored.** If `LORE_READ_ONLY=1` or `--read-only`, refuse writes.
5. **Scope before storing.** Repo-specific → `--repo=<mount>`. Cross-cutting → master.
6. **Errors are codes, not prose.** Match `E_*` strings; use `lore errors list --json` for the registry.
7. **Never wrap lore in your own retry loop for `UNIQUE constraint failed`.** lore already retries 8x internally.
8. **Never edit `.lore/lore.db` directly.** Always go through the CLI.
9. **Never commit `.lore/lore.db` to git.** `lore init` writes `.gitignore`. Commit `CLAUDE.md` instead.
10. **One render per capture batch.** Batch N captures, then render once.

---

## 12 · Verification checklist (end of session)

Before ending the session, verify:

```bash
# 1. Captured knowledge persisted
lore doctor --json | jq -e '.db_ok' >/dev/null || echo "WARN: DB issue"

# 2. .lore/LORE.md is up to date with the DB
diff .lore/LORE.md <(lore render --dry-run) >/dev/null || lore render

# 3. No stale lock from a crashed process
ls .lore/state/lock 2>/dev/null && echo "WARN: stale lock present"

# 4. Backup is recent
ls -la .lore/backups/ 2>/dev/null | tail -3
```

If you captured anything during the session, end with:

```bash
lore render
lore backup     # optional but cheap insurance
```

---

## 13 · Quick reference card

```
DETECT      [ -d .lore ] && echo yes || echo no
BOOTSTRAP   lore init --non-interactive
INGEST      lore learn-from docs ; lore learn promote <id>
CAPTURE     lore <memory|rule|decision|hotfix|task|...> add ...
RENDER      lore render
SEARCH      lore memory search "<q>" --json
INTROSPECT  lore why-context --last-render
DIAGNOSE    lore doctor --json
RECOVER     lore repair --tier=2 --confirm
HELP        lore --help  |  lore <cmd> --help  |  lore errors list --json
```

---

## 14 · When you need more

- **Exact CLI flags / JSON shapes** → [COMMANDS.md](./COMMANDS.md)
- **"I want to X" → command** → [USECASES.md](./USECASES.md)
- **Multi-step recipe** → [PLAYBOOKS.md](./PLAYBOOKS.md)
- **Command failed** → [ERRORS.md](./ERRORS.md)
- **Real worked transcript** → [examples/](./examples/)
- **Beyond what's documented** → `lore <cmd> --help` (introspect at runtime)

---

## 15 · Versioning

This skill targets `lore version --json` returning `schema_version: 1`. If you see `schema_version: 2`, additive changes only — re-read this SKILL.md to spot new sections. If `schema_version` ≥ 3, treat as breaking — defer to user.
