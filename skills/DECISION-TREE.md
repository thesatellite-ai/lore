# Decision tree — pick the right command

When the user's intent is ambiguous, walk this tree top-down.

## "I want to persist something"

```
Is it a hard CONSTRAINT we should enforce?
│
├── Yes, blocking violation         → rule add --severity=must
├── Yes, warn on violation          → rule add --severity=should
├── Yes, suggest only               → rule add --severity=may
│
└── No, it's not a constraint:
    │
    Is the WHY more important than the WHAT?
    │
    ├── Yes (architectural choice, has alternatives) → decision add
    │
    └── No:
        │
        Do we KEEP HITTING this issue?
        │
        ├── Yes, loud recurring warning  → hotfix add --severity=high
        │
        └── No, just a fact:
            │
            Is it reusable code/template?
            │
            ├── Code shape         → pattern add
            ├── Multi-step process → playbook add
            ├── LLM prompt         → prompt add
            ├── Architecture note  → architecturenote add
            │
            └── Generic fact       → memory add
```

## "I want to track work"

```
Is it a discrete unit of work?
│
├── Yes, single item                → task add --tasklist=<tlt_id> --priority=<p>
│                                     (tasklist required; create one first if needed)
│
├── Yes, multi-item initiative     → mission add  (then task add --tasklist=<tlt_id> --mission=<id>)
│
├── Multi-phase plan with intent   → plan add
│
└── No, it's a reminder:
    │
    Recurring?
    │
    ├── Yes  → reminder add --due=<next> --recurrence=<7d|30d|1m|3m|6m|1y>
    └── No   → reminder add --due=YYYY-MM-DD
```

## "I want to find something"

```
Do I know which entity kind?
│
├── Yes:
│   │
│   Just one row?  → lore <kind> show <id>
│   Multiple?      → lore <kind> list --json
│   Substring?     → lore memory search "<q>" --json   (FTS5 BM25)
│
└── No, cross-kind:
    │
    Fall back to shell + jq across kinds (see examples/07-search-patterns.md)
```

## "I want to label / discuss an entity"

```
Single-word label that applies to many entities?  → tag attach
Free-form note on ONE specific entity?            → comment add
```

## "Something is wrong"

```
Diagnose first:  lore doctor --json
│
├── doctor OK    → check `lore errors list --json` for the specific code you saw
│
└── doctor BROKEN:
    │
    Backup exists?  ls .lore/backups/
    │
    ├── Yes  → lore repair --tier=2 --confirm
    │
    └── No:
        │
        Is CLAUDE.md in git?
        │
        ├── Yes → lore repair --tier=3 --confirm  (then learn-from docs)
        │
        └── No  → support-bundle + file bug + restore from a clone if possible
```

## "I want to render"

```
Just regenerate?           → lore render
                             (hybrid: pins only directive + must-rules
                              + critical/high hotfixes; everything else via search)
Preview without writing?   → lore render --dry-run
Different output file?     → lore render --target=AGENTS.md
Different scope?           → lore render --repo=<mount>
```

## What goes into CLAUDE.md vs what lives in search

```
Pinned in CLAUDE.md (every session sees automatically):
  - Directive block
  - Rules where severity=must
  - Hotfixes where severity=critical or high

Searched on demand (run `lore search "<keywords>"` per turn):
  - Rules where severity=should or may
  - Hotfixes where severity=medium or low
  - Memories, decisions, patterns, architecturenotes, taste prefs, playbooks
  - Anything else captured but not in the "non-skippable" tier
```

## "I want to know what the AI saw"

```
Most recent render?        → lore why-context --last-render
Print the rendered text?   → lore why-context --last-render --rendered
JSON for piping?           → lore why-context --last-render --json
```

## "I want to introspect at runtime"

```
What commands exist?   → lore --help
What flags for X?      → lore <X> --help
What errors can occur? → lore errors list --json
What version?          → lore version --json
```

---

## Summary card

```
intent                     command
─────────────────────────  ─────────────────────────
hard rule                  rule add --severity=must|should|may
choice with rationale      decision add --title= --body=
recurring warning          hotfix add --severity=…
plain fact                 memory add
code template              pattern add
procedure                  playbook add
prompt                     prompt add
work item                  task add --tasklist=… --priority=… --mission=… [--plan=… --assigned-to=…]  (tasklist required)
initiative                 mission add
multi-phase plan           plan add
reminder                   reminder add --due=… [--recurrence=…]
label                      tag attach --on-table=… --on-id=… --tag=…
note                       comment add --on-table=… --on-id=…
search (memory only)       memory search "<q>" --json
search (any entity)        <kind> search "<q>" --json   (23 entities: task/rule/decision/hotfix/pattern/playbook/prompt/architecturenote/behaviour/cookbookrecipe/incident/suggestion/tastepref/snapshot/handoff/mission/tasklist/plan/workflow/workspace/techdoc/comment)
search syntax              auth*  /  "exact phrase"  /  redis OR cache  /  auth NOT login
search index admin         search status (drift health) | search rebuild [--kind=<e>]
list / show                memory list | memory show <id>  (and `<kind> list / show` for every entity)
post-upgrade migrate       setup  (runs ent migrate + rebuilds FTS index + stamps fingerprint; auto-runs in init)
edit in place              <kind> edit <id> --field=…  (16 entities; only flags you pass are applied)
move/reparent task         task edit <id> --tasklist=… --mission=… --plan=… --assigned-to=… (+ --clear-* for nullable fields)
archive (soft-delete)      <kind> archive <id>  (memory|rule|decision|hotfix|pattern|playbook|prompt|project|repo)
unarchive                  <kind> unarchive <id>
supersede with audit       <kind> add ... --supersedes=<old_id>  (memory|rule|decision|hotfix|pattern)
invalidate (bitemporal)    memory invalidate <id>  (sets valid_until=now; preserves history)
pause/resume mission       mission pause <id> | mission resume <id>
ack handoff                handoff ack <id>
install agent directive    directive install [--target=CLAUDE.md,AGENTS.md]
inspect render             why-context --last-render
recompile context          render
diagnose                   doctor
fix DB                     repair --tier=1|2|3 --confirm
back up DB                 backup
restore                    restore <path> --confirm
report bug                 support-bundle
```
