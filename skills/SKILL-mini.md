---
name: lore
description: |
  Local-first memory + context-management CLI for coding agents. Persists rules, decisions, memories, tasks, hotfixes, patterns, playbooks, prompts, and more into a SQLite DB that compiles into CLAUDE.md for every future session.

  Load when user says: remember, don't forget, we decided, we keep hitting, we always/never X, corrects your approach, confirms a non-obvious decision, mentions lore/.lore/CLAUDE.md/render/memory add/rule add, asks "what do we know about X", wants to bootstrap from existing markdown, reports a DB/corruption/lock issue. Also load proactively in any repo containing .lore/.
---

**In short:** lore is where you, the coding agent, save what you learn about a project: rules, decisions, notes and tasks. Answer a save request with the `lore` command itself, then `lore render`, so every later session sees it.

## OUTPUT CONTRACT

Respond with ONE fenced bash block containing literal `lore` commands. No prose before or after the block. No roleplay of lore's post-run output. After any capture command always emit `lore render` on its own line.

GOOD — memory trigger:
```bash
lore memory add --body "We use UTC for all timestamps"
lore render
```

GOOD — rule trigger:
```bash
lore rule add --severity must --body "Never wrap stdlib errors with fmt.Errorf"
lore render
```

BAD — roleplaying output instead of emitting the command:
```
Memory added:
    mem_<id> We use UTC for all timestamps
    created: 2026-05-11
```

BAD — running setup commands on every capture trigger:
```bash
lore init --non-interactive
lore render
```

## BOOTSTRAP — ensure binary before any command

If `lore` is not on PATH, install it (don't ask, don't hand-edit CLAUDE.md):

```bash
command -v lore >/dev/null 2>&1 || { command -v brew >/dev/null 2>&1 && brew install thesatellite-ai/tap/lore || curl -fsSL https://raw.githubusercontent.com/thesatellite-ai/lore/main/install.sh | sh; }
```

New project (no `.lore/`) + user wants lore: `lore init --non-interactive`. Install fails → tell user one line, stop; never degrade to prose in CLAUDE.md.

## ❌ FORBIDDEN PATTERN — read this first

When the user statement contains the word **"decided"** AND a **"because"** clause:

DO NOT EVER emit this:
```bash
lore rule add --severity must --body "<X> because <Y>"
```
That is the wrong verb: a choice plus its reason is a decision (`decision add`), not a rule.

You MUST emit this instead:
```bash
lore decision add --title "<X>" --body "<Y>"
lore render
```

The exact user phrase "We decided … because …" maps to `decision add`. No exceptions.

## ⛔ DO NOT CAPTURE when the user is just musing / exploring

Capture triggers fire on **committed statements of fact, rule, or decision**, NOT on speculation. If the user is brainstorming, asking your opinion, or hedging with conditional language, **respond conversationally — do NOT emit `lore … add`**.

Musing/hedging signal words (these DEFEAT capture triggers):
- "**maybe**", "**someday**", "**perhaps**", "**possibly**", "**what if**"
- "**I'm wondering**", "**I'm thinking about**", "**I'm musing**", "**not sure but**"
- "**what do you think about**", "**should we…?**", "**could we…?**"
- "**we might want to**", "**we could consider**", "**eventually**", "**at some point**"

Examples of NON-triggers (no capture command — respond in prose):

| User says | You should |
|---|---|
| "Maybe someday we should look into pgvector — what do you think?" | Discuss pros/cons. NO `lore add`. |
| "I'm wondering if we should drop the cron job." | Discuss. NO `lore add`. |
| "Should we move to gRPC?" | Discuss. NO `lore add`. |
| "What if we cached the auth lookups?" | Discuss. NO `lore add`. |
| "My commands keep failing with E_STORE_LOCKED — **what's the right way to handle this?**" | Explain retry/backoff. NO `lore hotfix add` — it's a HOW-question, not a recurring trap report. |
| "**How do I** bootstrap lore?" / "**What's the procedure** for X?" | Answer with the command sequence in prose/bash. NO capture. |

vs. real triggers (emit capture command):

| User says | You should |
|---|---|
| "We decided to use pgvector because…" | `lore decision add` — "decided" + "because" |
| "Remember: we use pgvector now." | `lore memory add` — direct statement of fact |
| "We always use pgvector in this codebase." | `lore rule add --severity must` — "always" |

The decisive signal: is the user **committing to a stance** or **floating an idea**? Only commitments get captured.

**SIX HARD STOP RULES** (a single ❌ here defeats any other signal in the prompt):

1. **If the prompt ENDS with a question mark (`?`)** — `"what do you think?"`, `"should we…?"`, `"could we…?"` — the user is asking, not committing. Respond in prose only. NO `lore add`, even if the prompt also mentions a topic that looks rule-shaped.

2. **If the prompt mentions a topic but the user hasn't asserted ANYTHING about it as fact** — `"Maybe X"`, `"thinking about X"`, `"what about X"` — there's no proposition to capture. Discuss the topic; do not invent content for `<text>` and fabricate a memory entry.

3. **If the prompt explicitly says "don't capture", "don't save", "just explain", "tell me what this means", "in plain English", "interpret this output"** — DO NOT EMIT ANY `lore add`. The user is asking for prose explanation only. Respect explicit no-capture directives unconditionally; they OVERRIDE every other signal in the prompt.

4. **Sarcasm / mock-tone** — phrases like "yeah right", "totally", "lol", "obviously" combined with capture-like vocabulary are NOT real triggers. The user is mocking, not asserting. Signal words to detect: "yeah right", "TOTALLY" (caps), "obviously", "lol", "/s", "amirite". When you see them around a capture-shaped statement, treat it as conversation — do NOT capture, and do NOT invert and capture the opposite either. Just acknowledge in prose and ask what they actually want.

5. **Counterfactual / hypothetical** — "if we HAD done X, we wouldn't have Y" / "had we used X" / "in retrospect we should have" / "imagine if we" express regret about an UNCHOSEN path. They are NOT current facts to capture. Do not emit `lore add` for these; do not invert the hypothetical into a positive claim either (that's hallucination). Respond in prose about the lesson.

6. **Invalid enum values in flags** — if the user types a flag value that is NOT in the documented enum (memory `--kind`: core|retrieved|episodic|procedural|archival; severity: must|should|may; priority: low|medium|high|urgent; status: todo|in_progress|blocked|done|canceled|archived; on-table: must be PLURAL), DO NOT FORWARD that bad value to the CLI. The OUTPUT CONTRACT's "one bash block" requirement is OVERRIDDEN here — emit a brief PROSE note listing the valid values, then optionally a placeholder command with `<valid-value>` for them to fill in. Never emit `--kind foobar` / `--severity critical` / `--priority blocker` / `--on-table task` etc. just because the user said them. (Scoping has no `--scope` flag: use `--repo <mount>` for a repo, or omit it for project-master.)

If you find yourself paraphrasing the user's question into a statement to capture, STOP. That's a hallucinated capture; the user didn't actually commit.

## CRITICAL DISCRIMINATION RULES

Apply these BEFORE choosing a verb. The presence of specific signal words is decisive.

1. "we **decided** X **because** Y" / "X **because of** Y" / "X **so that** Y" → ALWAYS `lore decision add --title "<X>" --body "<Y>"` (NEVER `rule add`)

2. "we **always** X" / "we **must** X" / "we **never** X" / "**don't** X" → ALWAYS `lore rule add --severity must` (no rationale = rule, not decision)

3. "**remember**" / "**don't forget**" / "**save** this" / bare fact statement → ALWAYS `lore memory add` (free-form, no severity, no rationale)

4. "we **keep hitting** X" / "**watch out** for X" / "**this bit us**" / recurring trap → ALWAYS `lore hotfix add --severity high --title "<headline>"`

5. "we **tried** X **and it broke**" / post-mortem → ALWAYS `lore incident add --title <t> --body <what-happened>`

Do NOT copy any few-shot example's verb verbatim — synthesize the correct verb from the rules above, then fill in the user's actual content.

## 🛑 DESTRUCTIVE OPERATIONS — never silent, always confirm + commit first

When the user asks to **delete**, **wipe**, **drop**, **rm -rf**, **start fresh**, or **nuke** any lore data (the `.lore/` directory, the DB, all rules, all memories, etc.), DO NOT silently execute. The protocol is:

1. Warn the user that the action is irreversible.
2. Make sure `.lore/data` is committed first (git is the copy that lets them undo it).
3. Suggest the soft-delete equivalent (`lore X archive`) if it applies.
4. Only emit the destructive command after explicit confirmation, AND with the `--confirm` flag where the command supports it.

Examples:

| User says | You should emit |
|---|---|
| "Delete the .lore dir entirely — wipe everything." | check `git status .lore/data` is clean first, then warn: "This is irreversible. Confirm and I'll proceed." Do NOT emit `rm -rf .lore` without confirmation. |
| "Drop all rules." | suggest `lore rule list --json` to review, then `lore rule archive` per rule (or `rule delete --yes`, which moves it to the trash). |
| "Reset the DB." | `lore doctor` first; the DB is a cache of `.lore/data`: `rm -f ~/.lore/cache/repo/<repo folder>-*`: the next command rebuilds it. |
| "Start completely fresh." | Same as "delete": commit first, confirm, then proceed. |

Never emit a bare `rm -rf .lore` or `DROP TABLE` or destructive SQL.

## ACTIVATION (session-start only — do NOT run on every request)

Run these ONCE at first setup, only if `.lore/` is absent AND user explicitly asks:

```bash
[ -d .lore ] || lore init --non-interactive
lore render
```

Existing CLAUDE.md / AGENTS.md notes: no importer in 0.1.18. Read them and add each item that is still true with `rule add`, `decision add` or `memory add`.

Every other user statement → TRIGGER GRAMMAR below.

## ENTITY MODEL

```
memory          free-form fact                memory add --body "<text>"
rule            constraint + severity         rule add --severity must|should|may --body "<text>"
decision        ADR-style with rationale      decision add --title "<t>" --body "<why>"
hotfix          loud recurring warning        hotfix add --severity high --title "<t>" --body "<text>"
pattern         reusable code shape           pattern add --title "<t>" --body "<code>"
playbook        reusable procedure            playbook add --title "<t>" --body "<steps>"
prompt          model prompt template         prompt add --title "<t>" --body "<text>"
architecturenote                              architecturenote add --title "<t>" --body "<b>"
incident        post-mortem                   incident add --title "<t>" --body "<b>"
mission         initiative with tasks         mission add --title "<title>" --target YYYY-MM-DD
task            work item                     task add --title "<title>" --priority low|medium|high|urgent
tasklist|plan   task grouping                 tasklist add | plan add
behaviour       configurable agent behaviour  behaviour add --title "<t>" --body "<spec>"
cookbookrecipe  longer multi-step recipe      cookbookrecipe add --title "<t>" --body "<steps>"
tastepref       subjective preference         tastepref add --body "<text>"   (NOT memory — for "I prefer X" / "my taste is Y")
suggestion      tentative idea, not committed suggestion add --title "<t>" --body "<b>"
workspace       editor / IDE configuration    workspace add --title "<t>" --body "<spec>"
handoff         session-to-session note       handoff add --for @<handle> [--task <id>] --body "<context>"
reminder        time-based, recurrence-aware  reminder add --message "<msg>" --due YYYY-MM-DD [--recurrence <dur>]
tag             polymorphic label             tag attach --on-table <plural> --on-id <id> --tag <n>
                                              tag add --name <n>     # pre-create standalone (no attachment yet)
comment         polymorphic discussion        comment add --on-table <plural> --on-id <id> --body "<text>"
```

Every entity: `add | list | show | edit | archive`. All accept `--json`.

## SCOPE FLAGS

```
--repo <mount>     scope to one repo in multi-repo project
--all-repos        read across every repo
--master-only      master rows only, no repo overlay
--no-inherit       strict; don't include broader-scope rows
--read-only        refuse writes (CI safe)
```

Default = current project + master inheritance.

## TRIGGER GRAMMAR

Fires even mid-sentence. Capture immediately, then continue with the rest of the request.

MID-SENTENCE EXAMPLES (the trigger word can be buried in surrounding chatter — ALWAYS extract it):
- "Yeah let me think — **oh by the way, remember** we always X. Anyway, what's next?" → still a `memory add` (or `rule add` if the verb is "always/must/never").
- "I was looking at the build today and **we never use globals** here. Moving on…" → still a `rule add --severity must`.
- "OK so before that — **we decided** to use Y because Z. Now back to the task." → still a `decision add --title <Y> --body <Z>`.

Do NOT respond conversationally to mid-sentence triggers ("got it!" / "noted!"). Emit the command, then answer the rest.

RULE vs DECISION discrimination (important — weak models confuse these):
- "we always/must/never X"            → `rule add --severity must "<X>"`   (no rationale)
- "we decided X **because** Y"        → `decision add --title "<X>" --body "<Y>"`   (rationale present)
- The presence of "because <rationale>" is the decisive signal for decision over rule.

```
T[remember|don't forget|save]            → memory add "<fact>"
T[we always X | we must X]               → rule add --severity must "<X>"
T[we should X]                           → rule add --severity should "<X>"
T[we never X | don't X]                  → rule add --severity must "Do not <X>"
T[we decided X because Y]                → decision add --title "<X>" --body "<Y>"
T[we keep hitting X | watch out for X]   → hotfix add --severity high "<X>"
T[this pattern: <code>]                  → pattern add --title <t> --body "<code>"
T[the procedure for X is …]              → playbook add --title <t> --body "<steps>"
T[capture our X workflow | deploy workflow | CI workflow | release workflow | named multi-step process]
                                          → workflow add --title <t> --body "<steps>"
                                          # workflow vs playbook: if user says "workflow" → workflow add. If they say "procedure" or "playbook" → playbook add.
T[use this prompt: …]                    → prompt add --title <t> --body "<text>"
T[the architecture is … | architecture decision: … | note our architecture | system architecture: …]
                                         → architecturenote add --title <t> --body <b>
                                         # "architecture" word → architecturenote, even if "decision" also appears.
                                         # Pure decision (no architecture word): "we decided X because Y" → decision add (see CRITICAL DISCRIMINATION RULES).
T[my taste preference is X | I prefer X | I strongly prefer X]
                                         → tastepref add "<X>"   (NOT memory_add — preferences have a dedicated entity)
T[here's a longer recipe: …]             → cookbookrecipe add --title <t> --body "<steps>"
T[suggestion: … | quick suggestion | tentative idea: … | not a commitment but … | worth thinking about: … | we could probably X]
                                         → suggestion add --title <t> --body <b>
                                         # suggestion vs memory: if the user signals "tentative" / "not a commitment" / "worth thinking about" / "we could probably" — use `suggestion add`, NOT `memory add`. Memory is for known facts; suggestion is for proposed-not-committed ideas.

T[no, that's not how we do it]           → rule add --severity must "<correct way>"
T[actually we have a rule against that]  → rule list | grep -i …; if absent add it
T[capture X twice | duplicate this rule | does this already exist?]
                                          → ALWAYS search first: lore rule search "X" --json; report whether it exists; only add if absent. The CLI does NOT auto-dedupe — adding the same rule twice creates two rows.
T[scratch the previous rule about X]     → rule search "X" --json; lore rule archive rul_<id> (or rule edit)
T[update the X prompt | edit the X template | the X prompt needs an update]
                                          → lore prompt edit <X> --body "<new body>"
                                          # edit MUTATES in place; no need to show first if user provided the new content
T[update the X pattern | edit pattern X]  → lore pattern edit <X> --body "<new body>"
T[flag dec_<id> for re-eval | revisit dec_<id> later | re-evaluate decision | conditions changed for dec_<id> but don't archive yet]
                                          → lore comment add --on-table decisions --on-id dec_<id> --body "REVISIT: <why>; conditions changed <when>"
                                          # OR: lore tag attach --on-table decisions --on-id dec_<id> --name revisit
                                          # Do NOT create a NEW decision; the existing dec_<id> is still active.
T[update what we said about X]           → rule search "X" --json; lore rule edit --id rul_<id> (or archive + add)
T[reverse the rule about X]              → rule search "X" --json; lore rule archive rul_<id> (then add a new rule with the opposite content)
T[yes, that's the right call] (non-obvious) → decision add
T[we tried X and it broke]               → incident add --title <t> --body <what>

T[add a task to X]                       → task add "<X>" --priority medium
T[high priority: X]                      → task add "<X>" --priority high
T[urgent: X]                             → task add "<X>" --priority urgent
T[track an initiative: X]                → mission add "<X>" --target YYYY-MM-DD
T[what tasks are open?]                  → task list --status todo --json
T[what is assigned to me / to X?]        → task list --assignee @me --json   (or act_<id> / stable_key / display name)
T[start work on tsk_<id>]                     → task start tsk_<id>
T[mark tsk_<id> done]                         → task done tsk_<id>
T[mark msn_<id> done | mission msn_<id> is complete | ship msn_<id>]
                                         → mission done msn_<id>
                                         # mission has its own done verb; do NOT use task done for an msn_<id> ID
T[cancel tsk_<id>]                            → task cancel tsk_<id>
T[cancel tsk_<id> because <reason> | cancel with note]
                                         → task cancel tsk_<id>; lore comment add --on-table tasks --on-id tsk_<id> --body "cancelled: <reason>"
                                         # capture the reason so future readers see WHY, not just that it was cancelled
T[remind me on <date> to X]              → reminder add "<X>" --due <date>
T[every week, remind me to X]            → reminder add "<X>" --due <next> --recurrence 7d

T[what do we know about X?]              → memory search "X" --json
T[what's the JSON envelope | shape of --json output | how do I parse the JSON]
                                          → (PROSE answer, NOT a command) Every `--json` output: {schema_version: 1, kind: "<n>.list|.show", count: <n>, data: [...]|{...}}. Pin parsing to schema_version: 1.
T[what rules exist?]                     → rule list --json
T[show all rules including archived | audit including soft-deleted | include archived]
                                          → <kind> list --json --include-archived
T[what decisions about X?]               → decision list --json | jq '.data[]|select(.title|test("X";"i"))'
T[who am I | what identity | whoami in lore]
                                          → lore identity show
T[DB is broken | rebuild the DB | E_DB_CORRUPT]
                                          → rm -f ~/.lore/cache/repo/<repo folder>-*   (the next command rebuilds from .lore/data)

T[remove my persisted identity | go back to auto-detected | unset identity | reset identity file]
                                          → lore identity unset
                                          # clears identity: in ~/.lore/config.yaml → fall back to auto-resolve
T[set my identity to X | record me as X]  → lore identity set "<X> <email>"
T[file a bug | make a support bundle | generate a bundle for the issue]
                                          → lore support-bundle --out /tmp/lore-bundle.tar.gz
                                          # (default omits memory/rule content; pass --include-content if rule bodies should be in the bundle)
T[who modified X | audit trail for X | who changed X | when was X last edited]
                                          → lore audit log --limit 0 --json | jq '.data[] | select(.target_id == "<X>" or .target_table == "<X>")'
                                          # newest first; `lore audit verify` checks the chain
T[why did you do X?]                     → why-context --rendered
T[show me mem_<id> | tsk_<id> | rul_<id> | dec_<id>]        → <kind> show <code>

T[capture for <repo> repo: <text>]       → rule add --repo <repo> "<text>"; render
T[pre-create a tag | create tag X without attaching | reserve tag name]
                                          → tag add --name <X>
                                          # use this when user wants to define the tag entity FIRST, then attach later
T[preview what render would produce | dry-run the rendered body]
                                          → render --dry-run   (always project-master + project rows; no master-only flag)
T[across every repo | all repos | every repo in this project | not scoped to any one repo]
                                         → <kind> list --all-repos --json   (default scope is current repo; use --all-repos to widen)
T[master-only | project-master rules]    → <kind> list --master-only --json
T[in a different project | switch to <project> project | peek into <project> project | rules under project X]
                                          → <kind> list --project <X> --json
                                          # --project flag is a one-shot override; doesn't change your current working project
T[strict scope | only this scope, no inheritance] → <kind> list --no-inherit --json
T[master rule: X]                        → rule add "<X>"; render   (master is the default when --repo is omitted)
```

## POLYMORPHIC: tag + comment

Attach to any entity via `--on-table <plural>` + `--on-id <id-or-code>`.

`--on-table` values: `tasks memories rules decisions hotfixes missions incidents patterns playbooks prompts reminders architecturenotes`

`--on-id` is the opaque ID printed by `list` / `add` (`tsk_…`, `mem_…`, `dec_…`, `rul_…`).

```bash
lore tag attach --on-table tasks     --on-id tsk_<id>   --tag backend
lore tag attach --on-table memories  --on-id mem_<id>   --tag security
lore comment add --on-table decisions --on-id dec_<id> --body "follow-up note"
lore tag list --json                                    # the project's tags (no per-row listing yet)
lore comment list --on-table hotfixes --on-id H-2  --json
```

Common slip: singular table name (`task` not `tasks`). Always plural.

## RENDER + INTROSPECT

```bash
lore render                        # compile DB → .lore/LORE.md + @import pointer in CLAUDE.md (run after every capture)
lore render --dry-run              # preview the .lore/LORE.md body without writing
lore render --target AGENTS.md    # stitch the pointer into a different agent file
lore render --out docs/LORE.md    # change the generated knowledge-file path
lore render --no-pointer          # write the generated file only; leave the agent file untouched
lore why-context    # what was included and why
lore why-context --rendered  # full rendered text
lore why-context --json      # pipe-friendly
lore doctor                        # DB integrity + FTS5 check; exit 0/1/2
```

## AGENT WORKFLOW (every session)

1. Read the core: `.lore/LORE.md` is already loaded through the pointer file (must rules, critical/high hotfixes, topic index). Obey it.
2. Before touching a path: `lore context --for-file <path>` (rules, hotfixes, notes for that file and its folders). Task: `lore context --for-task <tsk_id>`. Topic from the index: `lore context --topic <t>`.
3. Everything else: `lore search --query "<keywords>"` before a substantive answer; cite hits by id.

## SEARCH (FTS5 BM25)

```bash
lore memory search --query "redis"              # single keyword, BM25-ranked
lore memory search --query "redis OR cache"     # boolean OR / AND / NOT (uppercase)
lore memory search --query "auth*"              # prefix: auth, authenticate, authorize
lore memory search --query '"connection pool"'  # exact phrase (words in order, adjacent)
lore memory search --query "auth NOT deprecated" --json
lore memory search --query "" --json            # no FTS; all newest-first
lore memory search --query "tailwind" --repo web --no-inherit
lore memory search --query "tailwind" --all-repos
```

`search` works on every entity kind (`rule search`, `decision search`, etc.).

## ENUMS

```
severity:    must  should  may
priority:    low  medium  high  urgent
status:      todo  in_progress  done  cancelled  blocked  archived
recurrence:  7d  30d  1m  3m  6m  1y
scope:       no --scope flag — use --repo <mount> for a repo, omit for project-master
             (list/search filters: --repo  --all-repos  --master-only  --no-inherit)
```

## ID PREFIXES

Every id is `<prefix>_<32 hex>`; find one with `lore <kind> list` or `search`. There are no short forms (`M-3`, `T-19` were removed).

```
mem_  memory      rul_  rule        dec_  decision    hfx_  hotfix
tsk_  task        tlt_  tasklist    msn_  mission     pln_  plan
pat_  pattern     pbk_  playbook    prm_  prompt      snp_  snapshot
inc_  incident    hnd_  handoff     rmd_  reminder    cmt_  comment
```

## 20 PLAYBOOKS

```
P1  bootstrap new project
    lore init --non-interactive
    # existing notes: add each true item with rule add / decision add / memory add
    lore render

P2  capture a correction
    lore rule add --severity must --body "<corrected behavior>"
    lore render

P3  capture a decision
    lore decision add --title "<short>" --body "<rationale + revisit criteria>"
    lore render

P4  capture a recurring warning
    lore hotfix add --severity high --title "<headline>" --body "<warning>"
    lore render

P5  multi-repo scoping
    lore repo add --mount-name web   --origin git@…
    lore repo add --mount-name api   --origin git@…
    lore memory add --body "<web-only fact>"  --repo web
    lore memory add --body "<cross-cutting>"
    (cd web && lore render)
    (cd api && lore render)

P6  notes shared by sibling projects (a store; Mode B is retired)
    lore store init --name shared --url "file://$HOME/stores/shared"   # first repo
    lore store add --name shared --url "file://$HOME/stores/shared"    # each other repo
    # old Mode B repo: lore migrate run

P7  disaster recovery
    lore doctor --json
    rm -f ~/.lore/cache/repo/<repo folder>-*   # the next command rebuilds the cache from .lore/data
    lore search rebuild
    # .lore/data itself deleted: git checkout HEAD -- .lore/data
    lore render

P8  mission + tasks
    M=$(lore mission add --title "<title>" --target YYYY-MM-DD --json | jq -r '.data.id')
    TL=$(lore tasklist list --json | jq -r '.data[0].id')   # every task needs --tasklist
    lore task add --title "<t>" --tasklist $TL --mission $M --commitment accepted --priority high
    lore task list --mission $M --json

P9  periodic review reminder
    lore reminder add --message "review rules" --due YYYY-MM-DD --recurrence 30d

P10 inspect what the agent is seeing
    lore render --dry-run
    lore why-context --rendered

P11 migrate from .cursorrules / AGENTS.md
    # read the file; add each true item with its own command
    lore rule add --severity must --body "<rule from the file>"
    lore memory add --body "<fact from the file>"
    lore render

P12 CI integration (read-only)
    LORE_READ_ONLY=1 lore doctor --json | jq -e '.db_ok'
    diff .lore/LORE.md <(LORE_READ_ONLY=1 lore render --dry-run) \
      || { echo ".lore/LORE.md stale — run lore render"; exit 1; }

P13 onboard new teammate
    git add .lore/data .gitattributes .lore/LORE.md CLAUDE.md && git commit -m "lore knowledge"
    # teammate clones, then ANY lore command builds their cache from .lore/data:
    lore memory list

TEAM SYNC (.lore/data/ = one committed JSON file per shared row; automatic)
    commit .lore/data + .gitattributes WITH the code change that produced the knowledge
    lore sync status | conflicts                      inspect
    lore trash list --local                           rows sync removed (file disappeared)
    lore sync merge-rows --table <table> --keep <id> --drop <id>   fold a duplicate (refs move, drop → trash)
    lore sync resolve --id <n> --take kept|other           after the user picks a side
    lore sync peek --ref origin/<branch> [--table table]            another branch, read-only
    lore sync promote --ids <id> --to main                  local commit on main (confirm first)
    PR conflict in .lore/data on GitHub → on the branch: git pull origin main; settle; push
    lore sync install-action [--auto]                 opt-in workflow that does that for PRs (ask first);
                                                      runs only by hand unless --auto (spends Actions time)
    conflict markers live INSIDE the JSON value; `lore <entity> show` has both sides,
    settle with `lore <entity> edit --id <id> --body "…"` after the user picks; never delete the file

P14 promote memory → rule  (ALL 4 STEPS REQUIRED — do NOT skip the show)
    lore memory show --id mem_<id> --json | jq -r '.data.body'   # MUST fetch the actual body first;
                                                          # never invent the rule text.
    lore rule add --severity must --body "<text from show>"
    lore memory archive --id mem_<id>
    lore render

P15 demote rule → memory
    lore rule show --id rul_<id> --json | jq -r '.data.body'
    lore memory add --body "<text>"
    lore rule archive --id rul_<id>
    lore render

P16 periodic review
    lore doctor --json | jq '.status'
    lore reminder list --json | jq '.data[] | select(.due_at <= "<7-days-out>")'
    lore hotfix list --json
    lore task list --status in_progress --json
    lore render

P17 export project knowledge
    for kind in memory rule decision hotfix pattern playbook prompt task mission; do
      lore $kind list --json > export/$kind.json
    done

P18 audit log forensics
    lore audit log --limit 0 --json | jq '.data[] | select(.target_table == "rules")' | head -40

P19 render slice (budget control)
    (cd web && lore render)
    # plain `lore render` is always project-master + project; no master-only flag
    lore render --dry-run | wc -c

P20 migrate to new lore version
    git status --porcelain -- .lore/data   # commit first
    lore upgrade                           # nothing else to run: the cache rebuilds itself
    lore migrate run --dry-run && lore migrate run   # only for v0.1.17 data
    lore doctor
```

## TEAMS (orgs, live rows, agents)

Tasks are live: they reach everyone on any branch once pushed, and land on main after done; nothing pushes by itself, `lore sync` or `git push` sends them (`--live` / `--no-live` per write). In a client repo a note without `--to` goes to the internal store; move a note to the client on purpose only (`lore move --id <id> --to repo`). Exit 12/13/14 = the task is no longer yours: stop. Agents never approve gates.

```bash
lore whoami                                           # org, why, identity: run once per repo
lore check pr-body --file body.md                            # before posting a PR body or comment
lore move --id mem_01 --to repo                            # deliberate move into the client repo
lore agent start --label agent-backend --owner @alice     # export what it prints
lore inbox
lore task next --mission mis_12                       # exit 10 = no ready work
lore task heartbeat
lore hold claim --name full-suite --ttl 30m                  # one heavy run at a time; hold release after
lore context --internal                               # internal notes as text; never paste where the client reads
lore task note --id tsk_30 --text "progress"
lore task done --id tsk_30 --pr 412 --lesson "what you learned"
```

## TOP ERROR CODES

```
E_NEEDS_CONFIRM        show the person error.question; rerun with --yes only after they agree (exit 26)
needs_person: true     (E_POLICY_DENIED, E_REFUSED) stop; the person runs it in their own terminal
E_INVALID_ID           malformed id; copy it from list --json, never guess (exit 20)
E_STORE_LOCKED         another lore command holds the store lock; wait and retry
E_CONTENDED            another writer won every attempt; retry after a short wait
E_OFFLINE              remote unreachable; nothing queued, retry online
E_META_STORE_UNREADABLE  metadata write in a client repo, org store unreachable; nothing written, retry online
E_META_QUARANTINED     lore meta show --id <id> prints each problem and its fix
E_DB_CORRUPT           rm -f ~/.lore/cache/repo/<repo folder>-* (the next command rebuilds it from .lore/data)
E_NOT_PROJECT_ROOT     cd to project root (--db is refused on row commands)
E_AMBIGUOUS_PROJECT    the repo holds several projects; pass --project
E_SECRET_DETECTED      strip secret, or --secret-ref "<mgr>://<entry>" | --secret-unstored | --not-a-secret --reason "<why>" --yes (bare --yes does not store it)
E_ROOT_REFUSED         run as your user; LORE_ALLOW_ROOT=1 only if truly needed
E_SYMLINK_DB           a database, lock or audit file is a symlink; delete the link, lore recreates the file
E_READ_ONLY            drop --read-only or unset LORE_READ_ONLY
E_NETWORK_FS           project on iCloud/Dropbox/NFS; move it to a local disk
E_NOT_FOUND            well-formed id/name that does not exist here (exit 27)
E_NOT_IMPLEMENTED      not in this release; inform user, don't retry
E_INVALID_INPUT        check flag values against ENUMS section above
```

Match on code, not English message. Full registry: docs/reference/cli-commands.md#errors.

## ⚠ Enum validation (don't pass through obviously wrong values)

When the user uses a value that's NOT in the allowed enum, DO NOT blindly forward it to the CLI. Surface the constraint AND list valid options:

| User says | You should |
|---|---|
| `--kind foobar` (memory) | Note: foobar isn't valid. Valid kinds: `core`, `retrieved`, `episodic`, `procedural`, `archival`. Which did you mean? Don't emit the bad cmd. (There is no `--scope` flag — scope via `--repo` or omit for master.) |
| `--severity critical` | Note: not in enum. Valid: `must`, `should`, `may`. Map to closest (probably `must`) and confirm, OR ask. |
| `--priority blocker` | Not valid. Map to `urgent` and confirm. Valid: `low`, `medium`, `high`, `urgent`. |
| `--status wip` | Not valid. Valid: `todo`, `in_progress`, `blocked`, `done`, `canceled`, `archived`. Map to `in_progress`. |
| `--on-table task` (singular) | Plural required: `tasks`. Fix silently and proceed. |

The CLI will reject invalid enums with `E_INVALID_INPUT` (and unknown flags like `--scope` with an "unknown flag" error) anyway — better to catch them before emitting.

## ANTI-PATTERNS

1. Don't simulate lore output ("Memory added: mem_<id> …"). Emit the literal command.
2. Don't say "got it" / "noted" instead of emitting a capture command — that fails silently.
3. Don't omit `lore render` after a capture — the next session can't see unflushed DB rows.
4. Don't use singular table names in `--on-table` (`--on-table task` is wrong; use `tasks`).
5. Don't run `lore init` on every request — session-start only, when `.lore/` is absent.
6. Don't use `rule add` for soft preferences; use `memory add`. Reserve rules for true constraints.
9. Don't mix `--scope` and `--repo` carelessly; explicit form is `--repo <mount>` for repo-scoped rows.
10. Don't make up subcommands; canonical verbs are: `add list show edit archive search attach detach`.

## FEW-SHOTS

````
User: Quick suggestion to consider later, not a commitment: we could probably drop the in-memory cache layer entirely now that pgbouncer is in place. Worth thinking about.
You: (User said "suggestion", "not a commitment", "worth thinking about" — use `suggestion add`, NOT `memory add`.)
  lore suggestion add --title "Drop in-memory cache layer (pgbouncer is in place)" --body "Worth considering: with pgbouncer fronting the DB, our application-level in-memory cache may be redundant. Not a commitment, just worth evaluating."
  lore render

User: I'm running in CI with --read-only mode and I tried to capture a rule — what happens? Show me the command I'd run.
You: (Show the command WITH `--read-only`, and explain the rejection.)
  lore rule add --read-only --severity must --body "<text>"
  # → fails with E_READ_ONLY: write rejected; --read-only mode refuses all mutations.
  # In CI you should only run read commands (list/show/search/render --dry-run) under --read-only.

User: What's the JSON envelope shape for lore commands? I'm parsing it programmatically.
You: (This is a HOW-question — explain in prose, do NOT emit any `lore add`.)
  Every `--json` output emits:
  ```json
  {
    "schema_version": 1,
    "kind": "memory.list" | "task.show" | ...,
    "count": <n>,            // for list commands
    "data": [...] | {...}    // array for list, object for show
  }
  ```
  Pin parsing to `schema_version: 1`; that's the stable contract.

User: Pattern pat_019e... is obsolete now — archive it but don't delete.
You: (User already gave the opaque ID. Do NOT pipeline `pattern list | jq | xargs` — just archive directly.)
  lore pattern archive --id pat_019e...
  lore render

User: DB feels slow lately, queries are taking longer — what should I check first?
You: (Symptom + "what should I check?" → DIAGNOSTIC question. Do NOT capture as a hotfix; emit doctor + investigation steps.)
  lore doctor
  # also worth checking: the doctor notes, the size of .lore/data, attached stores (lore store list)
  # if confirmed real, THEN capture: lore hotfix add --severity high "<specific finding>"

User: I have a 'commit-msg' prompt template saved. Use it to draft a commit message for X.
You: (User said "I HAVE … saved" → RETRIEVE the existing prompt, do NOT `prompt add` a new one.)
  lore prompt show --id commit-msg
  # then apply the retrieved template to the user's context

User: Note our system architecture decision: backend is a single Go binary, DB is PostgreSQL with pgvector, NATS for events, Redis for cache.
You: (User said "architecture" — use `architecturenote add`, NOT `decision add`. The "architecture" keyword wins over "decision" when both are present, because the content is a multi-component system description, not a single choice-with-rationale.)
  lore architecturenote add --title "System architecture" --body "Backend: single Go binary. DB: PostgreSQL + pgvector. Events: NATS. Cache: Redis."
  lore render

User: Capture our deploy workflow: build with go build, then run task test, then tag the commit, then push. Call it "deploy-flow".
You: (User said "workflow" — use `workflow add`, NOT `playbook add`. The word "workflow" is the decisive signal.)
  lore workflow add --title "deploy-flow" --body "go build; task test; git tag v\$NEXT; git push --tags"
  lore render

User: Capture a rule with --kind foobar that we always lint before commit.
You: (Invalid enum value detected — and `rule add` has no `--kind` flag at all. DO NOT forward bad/unknown flags; the CLI rejects unknown flags. Surface the constraint first; don't blindly forward.)
  Note: `rule add` takes `--severity` (`must` | `should` | `may`), not `--kind`. Scope is `--repo <mount>` or omit for project-master — there is no `--scope` flag.
  Here's what the command should look like:
  ```bash
  lore rule add --severity must "Always lint before commit"
  lore render
  ```

User: Yeah right, we should TOTALLY capture everything someone says as a 'must' rule. That'll really help, lol.
You: (Sarcasm. "Yeah right" + "TOTALLY" in caps + "lol" — NOT a real capture trigger. Do NOT capture this. Do NOT invert it into the opposite and capture that either. Acknowledge and ask what they actually want:)
  Got the joke. If you actually want to set capture thresholds (e.g. when to use `must` vs `should`), let me know — happy to draft that. For now, no `lore add`.

User: If we had used pgvector from the start, we wouldn't have hit the timeout issue last quarter.
You: (Counterfactual — "if we HAD" expresses regret about an unchosen path. NOT a current fact, NOT an instruction to capture. Do NOT emit `lore add`. Do NOT capture the inverted positive ("avoid pgvector early" is the OPPOSITE of what the regret implies). Just acknowledge the lesson in prose:)
  In retrospect, pgvector earlier would have avoided that timeout. Worth keeping in mind for future infra choices. If you want me to capture "consider pgvector at project start" as a forward-looking rule, say so explicitly and I will.

User: I'm musing here, but maybe someday we should look into using pgvector — what do you think about that as a future direction?
You: (NO CAPTURE. The prompt ENDS in `?`, uses "musing", "maybe", "someday", "what do you think" — every signal says "discuss, don't capture". Respond in prose only:)
  pgvector co-locates vector search with the rest of your Postgres data so you don't run a separate service — that's the main draw. The trade-off is recall vs dedicated vector DBs (Pinecone, Qdrant) which can be ~20% better on high-dimensional search. For most apps at moderate scale (< 10M vectors) pgvector is plenty. Worth a spike if you decide to commit.
  ↳ Do NOT emit any capture command here. The user did not commit to anything; you are NOT capturing their question paraphrased as a statement.

User: Yeah let me think — oh by the way, remember we always run migrations in transactions. Anyway, what's next?
You:
  lore rule add --severity must --body "Always run migrations in transactions"
  lore render
  (then answer the user's "what's next?")

User: We decided to use pgvector instead of pinecone because we want vector search co-located with the rest of our Postgres data, no separate service to operate.
You:
  lore decision add --title "Use pgvector instead of Pinecone" --body "Want vector search co-located with rest of Postgres data; no separate service to operate."
  lore render

User: Remember: we use UTC for all timestamps.
You:
  lore memory add --body "We use UTC for all timestamps"
  lore render

User: Don't forget — tests run via task test, not go test.
You:
  lore memory add --body "Tests run via task test, not go test directly"
  lore render

User: We never use globals in this code.
You:
  lore rule add --severity must --body "Do not use package-level globals in this codebase"
  lore render

User: Capture this for the api repo: gqlgen resolvers may NOT call other resolvers directly.
You:
  lore rule add --severity must --repo api --body "gqlgen resolvers may NOT call other resolvers directly"
  lore render

User: Add a task to refactor the auth middleware, high priority.
You:
  lore task add --title "Refactor the auth middleware" --tasklist <tlt_id> --commitment accepted --priority high
  lore render

User: We decided to use pgvector over Pinecone because of ops simplicity and no vendor lock-in.
You:
  lore decision add --title "Use pgvector not Pinecone" --body "Ops simplicity: one DB not two. No vendor lock-in. Recall acceptable at current scale. Revisit if vectors exceed 10M or p95 latency exceeds 100ms."
  lore render

User: Tag tsk_<id> as backend.
You:
  lore tag attach --on-table tasks --on-id tsk_<id> --tag backend

User: What memories do we have about redis or cache?
You:
  lore memory search --query "redis OR cache" --json

User: Find anything starting with auth.
You:
  lore memory search --query "auth*" --json

User: Find memories about auth or session but NOT logging.
You: (Use FTS5 boolean operators in the query string itself — do NOT post-filter with jq when FTS5 can do it natively.)
  lore memory search --query "(auth OR session) NOT logging" --json

User: Render a context that fits under 4000 bytes, but preview first without writing.
You: (There is no `--budget` flag. Render is already a hybrid: it pins only the directive + `must` rules + critical/high hotfixes, so the generated `.lore/LORE.md` stays small. Preview its size without writing; to shrink further, lower rule/hotfix severities so fewer rows pin.)
  lore render --dry-run | wc -c

User: Reduce the rendered file size — show me what would change.
You: (No size flag exists. Preview the body and trim by re-scoping or down-severitying pinned rows.)
  lore render --dry-run | wc -c


User: My commands keep failing with E_STORE_LOCKED — what's the right way to handle this in a script?
You: (NO capture — this is a HOW-question)
  E_STORE_LOCKED means another lore command held the store lock. In a script,
  retry with backoff: sleep 0.5s, try again; give up after ~5 attempts.
  ```bash
  for i in 1 2 3 4 5; do
    lore rule add --body "<text>" && break; sleep 0.5
  done
  ```

User: Scratch the previous rule about avoiding globals — we DO use a single global request-id context now.
You: (REVERSING a captured rule requires search + archive + new-add in ONE response. Emit ALL THREE commands together, using `$(…)` to chain. Do NOT stop after the search.)
  RID=$(lore rule search --query "globals" --json | jq -r '.data[0].id')
  lore rule archive --id "$RID"
  lore rule add --severity should --body "A single global request-id context is allowed for tracing"
  lore render

User: What rules apply to this project?
You:
  lore rule list --json
````
