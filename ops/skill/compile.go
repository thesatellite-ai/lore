// Package skill defines `lore skill compile` (layer L5, CLI_REFERENCE 3.17
// "existing", PLUGIN_SYSTEM_DESIGN 4.1): read the canonical skill bundle
// (SKILL.md and its sibling docs) and ask a model for a compressed `mini`
// bundle for small-context or weak-instruction-following models. The
// output is a DRAFT (benched ~70-75% against the hand-tuned SKILL-mini.md),
// so the default path is SKILL-<target>-draft.md and never the canonical
// file.
//
//	skill.compile ─▶ collect --include globs under --source-dir (SKILL.md first)
//	              ─▶ compression brief + bundle ─▶ Env.SkillModel ─▶ Model.Compress
//	              ─▶ strip any preamble / code fence
//	              ─▶ --dry-run: return the text │ else write --output
//
// It is a Local operation (op.Spec.Local, P3-CFG-1): it opens no store and
// works outside a repo. The model call sits behind the Model port
// (adapters/llmcall through wire), so this package does no network I/O.
// Ported from the old binary's skill_compile.go (PASS3 ONE-PATH,
// P3-SKILL-1).
package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore skill compile`).
const (
	Noun      = "skill"
	OpCompile = "skill.compile"
)

// restNoun keeps the REST collection "skill" (the v0.1.17 singular noun).
const restNoun = "skill"

// Defaults of v0.1.17's flags (applied to zero values: the op inputs carry
// no default tag).
const (
	// TargetMini is the only compression target.
	TargetMini = "mini"
	// DefaultSourceDir is the canonical bundle folder.
	DefaultSourceDir = "./skill"
	// DefaultModel is a strong model: compression is editorial work and
	// small models under-compress or drop too much.
	DefaultModel = "claude-sonnet-4-6"
	// DefaultBudgetBytes is the size the model is told to aim for.
	DefaultBudgetBytes = 14000
	// DefaultTemperature keeps the output stable.
	DefaultTemperature = 0.2
	// DefaultTimeoutSec bounds the model call.
	DefaultTimeoutSec = 300
	// maxOutputTokens is ample for a ~14 KB bundle.
	maxOutputTokens = 16000
	// draftNameFmt is the default output name inside the source folder.
	draftNameFmt = "SKILL-%s-draft.md"
	// outputFileMode is owner read/write, like every file lore writes.
	outputFileMode = 0o600
)

// DefaultIncludes are the globs, relative to the source folder, read by
// default.
var DefaultIncludes = []string{"*.md", "examples/*.md", "playbooks/*.md"}

// CallOptions are the knobs of one model call.
type CallOptions struct {
	Temperature float64
	MaxTokens   int
	Timeout     time.Duration
}

// Reply is what a model call returns.
type Reply struct {
	// Output is the model's raw text.
	Output string
	// CostUSD is an estimate, not a bill.
	CostUSD   float64
	ElapsedMs int64
}

// Model runs one compression call; adapters/llmcall implements it.
type Model interface {
	Compress(ctx context.Context, model, prompt string, opts CallOptions) (Reply, error)
}

// Env is what skill.compile needs; *wire.App implements it.
type Env interface {
	// SkillModel is the model caller (E_UNSUPPORTED when the binary has none).
	SkillModel(ctx context.Context) (Model, error)
}

// CompileIn is the input of skill.compile (the v0.1.17 flags).
type CompileIn struct {
	Target      string   `json:"target,omitempty" flag:"target" desc:"compression target (only mini; default mini)"`
	SourceDir   string   `json:"source_dir,omitempty" flag:"source-dir" desc:"directory holding the canonical bundle (.md files; default ./skill)"`
	Output      string   `json:"output,omitempty" flag:"output" desc:"output path (default <source-dir>/SKILL-<target>-draft.md; the compiler output is never canonical: bench it, then pass --output to promote)"`
	Model       string   `json:"model,omitempty" flag:"model" desc:"compressor model; must be a strong model (default claude-sonnet-4-6; ollama:<model> for a local one)"`
	BudgetBytes int      `json:"budget_bytes,omitempty" flag:"budget-bytes" desc:"byte budget the model aims for (default 14000)"`
	Temperature float64  `json:"temperature,omitempty" flag:"temperature" desc:"sampling temperature (default 0.2; raise to 0.5+ for bolder edits)"`
	TimeoutSec  int      `json:"timeout,omitempty" flag:"timeout" desc:"model call timeout in seconds (default 300)"`
	DryRun      bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"print the result instead of writing the file"`
	Include     []string `json:"include,omitempty" flag:"include" desc:"file globs relative to source-dir to read (repeatable; default *.md, examples/*.md, playbooks/*.md)"`
}

// CompileOut is the output of skill.compile.
type CompileOut struct {
	Target       string  `json:"target"`
	Output       string  `json:"output" desc:"the file written (or that would be, with --dry-run)"`
	Model        string  `json:"model"`
	SourceFiles  int     `json:"source_files"`
	SourceBytes  int     `json:"source_bytes"`
	BudgetBytes  int     `json:"budget_bytes"`
	OutputBytes  int     `json:"output_bytes"`
	CompressionX float64 `json:"compression_x" desc:"source bytes / output bytes"`
	CostUSD      float64 `json:"cost_usd" desc:"estimated model cost"`
	ElapsedMs    int64   `json:"elapsed_ms"`
	DryRun       bool    `json:"dry_run,omitempty"`
	// Compiled is the compressed bundle, set only with --dry-run (nothing
	// is written then).
	Compiled string `json:"compiled,omitempty"`
}

// Text prints the v0.1.17 report.
func (o CompileOut) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== skill compile → %s ===\n", o.Target)
	fmt.Fprintf(&b, "  source:       %d files, %d bytes\n", o.SourceFiles, o.SourceBytes)
	fmt.Fprintf(&b, "  budget:       %d bytes (target)\n", o.BudgetBytes)
	fmt.Fprintf(&b, "  model:        %s\n", o.Model)
	fmt.Fprintf(&b, "  output:       %s\n", o.Output)
	fmt.Fprintf(&b, "\n  output size:  %d bytes (%.1f× compression)\n", o.OutputBytes, o.CompressionX)
	fmt.Fprintf(&b, "  est. cost:    $%.4f  (elapsed: %.1fs)\n", o.CostUSD, float64(o.ElapsedMs)/msPerSecond)
	if o.DryRun {
		b.WriteString("\n─── BEGIN COMPILED OUTPUT (dry-run) ───\n")
		b.WriteString(o.Compiled)
		return b.String()
	}
	fmt.Fprintf(&b, "\n  ✓ wrote %s\n", o.Output)
	b.WriteString("  diff before committing — the compiler is non-deterministic\n")
	return b.String()
}

// codeFence opens and closes a markdown code block.
const codeFence = "```"

// msPerSecond converts ElapsedMs for the report.
const msPerSecond = 1000.0

// Ops returns the skill operations bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpCompile, Summary: "Compress the canonical skill bundle into a smaller draft .md through a model",
			Permission: op.PermWrite, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			Errors:   []errs.Code{errs.InvalidInput, errs.NotFound, errs.Unsupported, errs.Internal},
			Examples: []op.Example{{Title: "compile the mini bundle and print it", CLI: "lore skill compile --dry-run", Want: ""}},
		}, func(ctx context.Context, in CompileIn) (CompileOut, error) {
			m, err := env.SkillModel(ctx)
			if err != nil {
				return CompileOut{}, err
			}
			return Compile(ctx, m, in)
		}),
	}
}

// withDefaults fills v0.1.17's flag defaults into zero values.
func withDefaults(in CompileIn) CompileIn {
	if in.Target == "" {
		in.Target = TargetMini
	}
	if in.SourceDir == "" {
		in.SourceDir = DefaultSourceDir
	}
	if in.Model == "" {
		in.Model = DefaultModel
	}
	if in.BudgetBytes <= 0 {
		in.BudgetBytes = DefaultBudgetBytes
	}
	if in.Temperature <= 0 {
		in.Temperature = DefaultTemperature
	}
	if in.TimeoutSec <= 0 {
		in.TimeoutSec = DefaultTimeoutSec
	}
	if len(in.Include) == 0 {
		in.Include = DefaultIncludes
	}
	if in.Output == "" {
		// Always a DRAFT path: never overwrite the canonical
		// SKILL-<target>.md by accident.
		in.Output = filepath.Join(in.SourceDir, fmt.Sprintf(draftNameFmt, in.Target))
	}
	return in
}

// Compile is skill.compile over a Model.
func Compile(ctx context.Context, m Model, in CompileIn) (CompileOut, error) {
	in = withDefaults(in)
	if in.Target != TargetMini {
		return CompileOut{}, errs.New(errs.InvalidInput, "only --target=mini is supported in this version")
	}
	files, totalBytes, err := collectSkillSources(in.SourceDir, in.Include)
	if err != nil {
		return CompileOut{}, err
	}
	if len(files) == 0 {
		return CompileOut{}, errs.New(errs.NotFound,
			fmt.Sprintf("no source files found under %s matching globs %v", in.SourceDir, in.Include)).
			WithHint("pass --source-dir with the folder holding the skill's .md files (default ./skill), or --include globs that match them")
	}
	prompt := buildSkillCompressPrompt(files, in.BudgetBytes)
	resp, err := m.Compress(ctx, in.Model, prompt, CallOptions{
		Temperature: in.Temperature, MaxTokens: maxOutputTokens,
		Timeout: time.Duration(in.TimeoutSec) * time.Second,
	})
	if err != nil {
		return CompileOut{}, errs.Wrap(errs.Internal, "model call", err)
	}
	compressed := extractCompressedOutput(resp.Output)
	out := CompileOut{
		Target: in.Target, Output: in.Output, Model: in.Model,
		SourceFiles: len(files), SourceBytes: totalBytes, BudgetBytes: in.BudgetBytes,
		OutputBytes: len(compressed), CostUSD: resp.CostUSD, ElapsedMs: resp.ElapsedMs, DryRun: in.DryRun,
	}
	if out.OutputBytes > 0 {
		out.CompressionX = float64(totalBytes) / float64(out.OutputBytes)
	}
	if in.DryRun {
		out.Compiled = compressed
		return out, nil
	}
	if werr := os.WriteFile(in.Output, []byte(compressed), outputFileMode); werr != nil {
		return CompileOut{}, errs.Wrap(errs.Internal, "write output", werr)
	}
	return out, nil
}

// collectSkillSources walks source-dir and returns matching files sorted
// in a stable order. SKILL.md always comes first so the model sees the
// canonical structure before any sibling expansion
func collectSkillSources(sourceDir string, globs []string) (map[string]string, int, error) {
	files := map[string]string{}
	totalBytes := 0
	seen := map[string]bool{}

	for _, g := range globs {
		matches, err := filepath.Glob(filepath.Join(sourceDir, g))
		if err != nil {
			return nil, 0, errs.New(errs.InvalidInput,
				fmt.Sprintf("bad glob %q: %v", g, err))
		}
		for _, m := range matches {
			if seen[m] {
				continue
			}
			seen[m] = true
			info, err := os.Stat(m)
			if err != nil || info.IsDir() {
				continue
			}
			content, err := os.ReadFile(filepath.Clean(m))
			if err != nil {
				return nil, 0, errs.New(errs.Internal,
					fmt.Sprintf("read %s: %v", m, err))
			}
			rel, _ := filepath.Rel(sourceDir, m)
			files[filepath.ToSlash(rel)] = string(content) // one header spelling on every OS
			totalBytes += len(content)
		}
	}
	return files, totalBytes, nil
}

// buildSkillCompressPrompt assembles the model prompt: a system-y preamble
// stating the compression rules, then the concatenated source bundle
// with file headers
func buildSkillCompressPrompt(files map[string]string, budgetBytes int) string {
	// Stable order: SKILL.md first, then alphabetical
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i] == "SKILL.md" {
			return true
		}
		if keys[j] == "SKILL.md" {
			return false
		}
		return keys[i] < keys[j]
	})

	var b strings.Builder
	b.WriteString(compressionRules(budgetBytes))
	b.WriteString("\n\n---\n\n# SOURCE BUNDLE\n\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "\n## FILE: %s\n\n", k)
		b.WriteString(files[k])
		b.WriteString("\n")
	}
	b.WriteString("\n---\n\nNow produce the compressed `SKILL-mini.md`. ")
	b.WriteString("Respond with the compressed markdown ONLY — no preamble, no explanation, no closing remarks. ")
	b.WriteString("Start your response with the YAML frontmatter (`---\\n`) and end with the last line of the bundle. ")
	b.Write(fmt.Appendf(nil, "Aim for approximately %d bytes total.\n", budgetBytes))
	return b.String()
}

// compressionRules is the heart of the compiler — it's the editorial
// brief the model follows. Tuned against the hand-authored SKILL-mini.md
// that scored 100% on the 3-eval bench at qwen3-coder + qwen2.5-coder:1.5b
//
// TOOL NEUTRAL: the brief's examples name the generated LORE.md and real
// commands (`lore learn-from docs`), never one AI tool's file.
func compressionRules(budgetBytes int) string {
	return fmt.Sprintf(`# TASK

Compress the lore skill bundle below into a single ~%d-byte `+"`SKILL-mini.md`"+` file
suitable for small-context (32K) and weak-instruction-following models

# COMPRESSION RULES (apply in order)

1. **Output contract first.** Open with an OUTPUT CONTRACT section telling the model:
   "respond with ONE fenced bash block, no prose, no roleplay of aicoder's post-run output,
   always emit `+"`lore render`"+` after a capture command." Include 2 GOOD examples (one memory, one rule)
   and 2 BAD examples (roleplaying output, running setup commands on every trigger)

2. **Activation section comes AFTER trigger grammar, not before.** Mark it clearly as
   "session-start only — do NOT run on every request." Otherwise weak models will run
   `+"`lore init`"+` on capture triggers

3. **Drop tutorial prose.** Cut sentences explaining "why" / "this is important" / motivation
   Cut every "Tip:" / "Note:" callout. Cut transitional paragraphs between tables. Keep only
   the facts the model needs to emit correct commands

4. **Convert tables to a trigger DSL.** Markdown user-says tables become:
        T[remember|forget|save]   → memory add "<fact>"
        T[always|must]            → rule add --severity=must "<X>"
   This compresses 3-5× per table and is easier for code-trained models to pattern-match

5. **Collapse playbooks to command sequences.** Each P1..P20 becomes:
        P1 bootstrap:
          lore init --non-interactive
          lore render
   Drop the "when" / "why" prose around each step

6. **Eliminate cross-file redundancy.** Scope flags, polymorphic syntax, severity enums —
   document each once. Pick the most concise location

7. **Drop the examples/ directory entirely.** The walkthroughs there are 90%% narrative for
   humans onboarding; the canonical commands they teach are already in the playbooks or
   trigger grammar. EXCEPTION: if examples/07-search-patterns.md teaches FTS5 syntax
   (prefix `+"`foo*`"+`, `+"`AND/OR/NOT`"+`, phrase `+"`\"foo bar\"`"+`) — include those operator examples in a
   compact SEARCH section

8. **Strip markdown chrome.** No emoji. No `+"`**bold**`"+`. No `+"`> blockquote`"+`. No headers deeper
   than ## except where genuinely helpful. No `+"`[link](./path.md)`"+` references — bench has no
   filesystem access, so cross-doc links are dead bytes

9. **Always include these sections (in this order):**
   - YAML frontmatter (name, description)
   - OUTPUT CONTRACT (with 2 good + 2 bad examples)
   - ⛔ DO NOT CAPTURE when musing/exploring (anti-trigger section — see rule 11)
   - ❌ FORBIDDEN PATTERN — rule-vs-decision (see rule 12)
   - CRITICAL DISCRIMINATION RULES — 5 verb-selection rules (see rule 13)
   - ACTIVATION (session-start-only warning)
   - ENTITY MODEL (one line per entity kind with verb)
   - SCOPE FLAGS (--scope, --repo, --all-repos, --master-only, --no-inherit, --read-only)
   - TRIGGER GRAMMAR (the T[…] DSL — include MID-SENTENCE EXAMPLES sub-block)
   - POLYMORPHIC: tag + comment (the singular-vs-plural gotcha)
   - RENDER + INTROSPECT
   - SEARCH (FTS5 BM25 syntax: prefix, OR/AND/NOT, phrase)
   - ENUMS (severity, priority, status, scope)
   - ID PREFIXES (opaque ids: mem_, rul_, dec_, hfx_, tsk_, msn_, …; there are no short mem_<id> / tsk_<id> forms)
   - 20 PLAYBOOKS (cmd sequences only; P14 + P15 must say "ALL STEPS REQUIRED")
   - TOP ERROR CODES (one line each)
   - ANTI-PATTERNS (numbered list, 6-10 items)
   - FEW-SHOTS (8-12 input/output pairs; lead with mid-sentence + decision)

10. **Anti-pattern list must explicitly include:**
    - "Don't simulate lore output" (roleplay)
    - "Don't say 'got it' / 'noted' instead of emitting a capture command"
    - "Don't omit `+"`lore render`"+` after a capture"
    - "Don't use singular table names in --on-table" (it's `+"`tasks`"+` not `+"`task`"+`)
    - "Don't run `+"`lore init`"+` on every request" (session-start only)
    - "Don't copy a few-shot's content verbatim — synthesize from the user's actual words"
    - "Don't capture musings — only commitments. 'Maybe someday' is NOT a trigger."

11. **REQUIRED: ⛔ DO NOT CAPTURE block.** Insert immediately after OUTPUT CONTRACT. Must list these hedging signal words that DEFEAT capture triggers: "maybe", "someday", "perhaps", "possibly", "what if", "I'm wondering", "I'm thinking about", "I'm musing", "not sure but", "what do you think about", "should we…?", "could we…?", "we might want to", "we could consider", "eventually", "at some point". ALSO include question-form anti-triggers: "**how do I X**", "**what's the right way to X**", "**my commands keep failing — how do I handle this**", "**what's the procedure for X**" — these are HOW-questions, NOT capture triggers (answer in prose/bash, do NOT emit `+"`hotfix add`"+` or `+"`memory add`"+`). Include a 6-row wrong-vs-right table contrasting musings + how-questions with real triggers ("We decided X because Y" / "Remember: …" / "We always X"). State the decisive signal: "is the user **committing** or **floating** or **asking how**?" Only commitments get captured

12. **REQUIRED: ❌ FORBIDDEN PATTERN block.** Insert after the ⛔ DO NOT CAPTURE block. Must explicitly forbid:
    DO NOT EVER emit `+"`lore rule add --severity=must \"<X>\" --body=\"<Y>\"`"+` (rule has no --body)
    The exact user phrase "We decided … because …" MUST map to `+"`lore decision add --title=\"<X>\" --body=\"<Y>\"`"+`
    State: "No exceptions."

13. **REQUIRED: CRITICAL DISCRIMINATION RULES block.** Insert after the FORBIDDEN PATTERN block. Five rules, in priority order:
    1. "we decided X because Y" / "X because of Y" / "X so that Y"  → ALWAYS `+"`decision add`"+` (NEVER `+"`rule add`"+`)
    2. "we always/must/never X" / "don't X"                         → ALWAYS `+"`rule add --severity=must`"+` (no rationale)
    3. "remember" / "don't forget" / "save this" / bare fact         → ALWAYS `+"`memory add`"+`
    4. "we keep hitting X" / "watch out for X" / "this bit us"       → ALWAYS `+"`hotfix add --severity=high`"+`
    5. "we tried X and it broke" / post-mortem                       → ALWAYS `+"`incident add --title=<t> --body=<what>`"+`
    End with: "Do NOT copy any few-shot example's verb verbatim — synthesize the correct verb from the rules above, then fill in the user's actual content."

14. **TRIGGER GRAMMAR must include a MID-SENTENCE EXAMPLES sub-block.** Show 3 examples of the capture trigger word buried in surrounding chatter ("Yeah let me think — oh by the way, **remember** we always X. Anyway, what's next?") followed by "Do NOT respond conversationally ('got it!' / 'noted!') — emit the command, then answer the rest." Plus an explicit RULE vs DECISION discrimination paragraph (the "because Y" signal is decisive for decision over rule)

15. **Playbook P14 (promote memory → rule) and P15 (demote rule → memory) must be marked "(ALL STEPS REQUIRED — do NOT skip the show)"** and the first step must have an inline comment "# MUST fetch the actual body first; never invent the rule text."

16. **TRIGGER GRAMMAR must include conflict-resolution triggers:**
    T[scratch the previous rule about X]      → rule search "X"; archive rul_<id>; (optionally add new)
    T[update what we said about X]            → rule search "X"; rule edit rul_<id> (or archive + add)
    T[reverse the rule about X]               → rule search "X"; archive rul_<id>; add new with opposite content
    These must be emitted as ONE atomic shell script using `+"`$(…)`"+` command substitution to extract the rule code in-line — NOT as multi-step "Step 1 / Step 2" examples (weak models stop after step 1). Example form:
        RID=$(lore rule search --query "<keyword>" --json | jq -r '.data[0].code')
        lore rule archive --id "$RID"
        lore rule add --severity=should --body "<new stance>"
        lore render

17. **FEW-SHOTS section must include:**
    - A render-flags combo example: `+"`lore render --budget=4000 --dry-run`"+` for "budget-trimmed LORE.md, preview only"
    - An error-recovery example for E_DB_LOCKED showing retry-with-backoff in prose+bash (NOT a capture command)
    - A conflict-resolution example using the `+"`$(…)`"+` atomic-script form (rule 16)
    - An FTS5 boolean example showing `+"`(auth OR session) NOT logging`"+` and the explicit note "do NOT post-filter with jq when FTS5 can do it natively" — models tend to default to `+"`jq | select(... | not)`"+` instead of the in-query NOT operator

18. **REQUIRED: 🛑 DESTRUCTIVE OPERATIONS block.** Insert immediately after the ⛔ / ❌ / 🔑 blocks (before ACTIVATION). Triggers on: "delete", "wipe", "drop", "rm -rf", "start fresh", "nuke", "reset the DB". Protocol: (1) warn user the action is irreversible; (2) emit `+"`lore backup`"+` first; (3) suggest soft-delete equivalent (`+"`<kind> archive`"+`) where applicable; (4) only emit destructive command after explicit confirmation AND with `+"`--confirm`"+` flag where supported. NEVER emit a bare `+"`rm -rf .lore`"+`, `+"`DROP TABLE`"+`, or destructive SQL. Include a 4-row table showing the wrong-vs-right behavior for "delete .lore dir", "drop all rules", "reset DB", "start fresh"

19. **TRIGGER GRAMMAR must include scope-widening + scope-override triggers** (scope flags work on EVERY read/write, including `+"`render`"+` previews — not just capture):
    T[across every repo | all repos | every repo in this project | not scoped to any one repo]
                                             → <kind> list --all-repos --json
    T[master-only | project-master rules]    → <kind> list --master-only --json
    T[strict scope | only this scope, no inheritance] → <kind> list --no-inherit --json
    T[as if I were on the canary scope | preview from canary | what would canary render look like]
                                             → render --scope=canary --dry-run
    T[preview from master scope | what would master-only render look like]
                                             → render --master-only --dry-run
    Bundle should explain: without these flags, default reads scope to the current repo + inherit project-master rows. Scope flags apply to `+"`render`"+` itself, so previewing "what canary would see" is just `+"`render --scope=canary --dry-run`"+`

20. **TRIGGER GRAMMAR must include audit-trail trigger** (no dedicated CLI verb yet for audit log; access is via raw SQL):
    T[who modified X | audit trail for X | who changed X | when was X last edited]
        → sqlite3 .lore/lore.db "SELECT tx_at,actor_id,op,entity_id FROM audit_log WHERE entity_id='<X>' OR entity_table='<X>' ORDER BY tx_at DESC LIMIT 20"
    Mention inline that this is the raw-SQL access path (the bundle should not invent `+"`lore audit`"+` as if it were a CLI verb)

21. **TRIGGER GRAMMAR must include identity + support-bundle triggers:**
    T[who am I | what identity | whoami in aicoder]
        → lore identity show
    T[set my identity to X | record me as X]
        → lore identity set --actor="<X>"
    T[file a bug | make a support bundle | generate a bundle for the issue]
        → lore support-bundle --out=/tmp/aicoder-bundle.tar.gz
    Mention inline that `+"`support-bundle`"+` omits memory/rule content by default; `+"`--include-content`"+` opts in

22. **ENTITY MODEL must include rare entities** (don't drop them in compression):
    behaviour, cookbookrecipe, tastepref, suggestion, workspace, handoff — one line each with verb and short purpose. Critical: `+"`tastepref`"+` is distinct from `+"`memory`"+` — subjective preferences ("I prefer X", "my taste is Y") map to `+"`tastepref add`"+`, NOT `+"`memory add`"+`. Add a corresponding trigger to TRIGGER GRAMMAR:
    T[my taste preference is X | I prefer X | I strongly prefer X]
        → tastepref add "<X>"   (NOT memory_add)

23. **No learn or bench examples.** `+"`lore learn`"+`, `+"`lore learn-from`"+` and `+"`lore bench`"+` are not commands of this release; drop every trigger, playbook step or few-shot that uses them

24. **Note the CLI gaps that don't have direct verbs.** Mention briefly that:
    - There is NO `+"`lore task block <tsk_id>`"+` transition (despite `+"`blocked`"+` being a valid status enum); the workaround is `+"`lore comment add --on-table=tasks --on-id=tsk_<id> --body=\"blocked: <reason>\"`"+`
    - There is NO `+"`lore audit`"+` CLI verb yet; audit-log access is raw SQL (covered in rule 20)

25. **No bench triggers** (see rule 23)

26. **TRIGGER GRAMMAR must distinguish workflow from playbook** (both are "named procedures" and weak models default to `+"`playbook`"+`):
    T[capture our X workflow | deploy workflow | CI workflow | release workflow]
        → workflow add --name=<n> --body="<steps>"
    T[the procedure for X is …]
        → playbook add --name=<n> --body="<steps>"
    Decisive signal: if the user says the word "workflow", use `+"`workflow add`"+`. If they say "procedure" or "playbook", use `+"`playbook add`"+`. Add a few-shot example for the workflow case explicitly so models don't default to `+"`playbook`"+` for "deploy workflow" prompts

27. **FEW-SHOTS must include a negative-trigger example with the exact "musing" prompt pattern.** Without it, models will paraphrase a question into a fake memory capture. Required few-shot:
    User: I'm musing here, but maybe someday we should look into using pgvector — what do you think about that as a future direction?
    You: (NO CAPTURE — discuss pros/cons in prose only. Do NOT emit `+"`lore memory add`"+`.)
    [followed by 2-3 sentences of actual discussion content]

28. **TRIGGER GRAMMAR must distinguish suggestion from memory** (subtle but bench-confirmed):
    T[suggestion: … | quick suggestion | tentative idea: … | not a commitment but … | worth thinking about: … | we could probably X]
        → suggestion add --title=<t> --body=<b>
    Decisive signal: phrases like "tentative", "not a commitment", "worth thinking about", "we could probably" — use `+"`suggestion add`"+`, NOT `+"`memory add`"+`. Memory is for known facts; suggestion is proposed-but-not-committed

29. **TRIGGER GRAMMAR must distinguish architecturenote from decision** (both record reasoning but architecturenote is for multi-component system descriptions):
    T[the architecture is … | architecture decision: … | note our architecture | system architecture: …]
        → architecturenote add --title=<t> --body=<b>
    The word "architecture" wins over "decision" when both appear in the same prompt. If content is component-listing ("backend is X, DB is Y, events are Z"), use `+"`architecturenote`"+`. If content is single-choice-with-rationale ("we decided X because Y"), use `+"`decision add`"+` (rule 13)

30. **TRIGGER GRAMMAR must include mission lifecycle:**
    T[mark msn_<id> done | mission msn_<id> is complete | ship msn_<id>]
        → mission done msn_<id>
    Bundle must say explicitly: mission has its own done verb; do NOT use `+"`task done`"+` for an msn_<id> ID

31. **TRIGGER GRAMMAR must include decision-revisit pattern** (flag without archiving):
    T[flag dec_<id> for re-eval | revisit dec_<id> | re-evaluate decision | conditions changed for dec_<id> but don't archive yet]
        → lore comment add --on-table=decisions --on-id=dec_<id> --body="REVISIT: <why>; conditions changed <when>"
          OR: lore tag attach --on-table=decisions --on-id=dec_<id> --name=revisit
    Critical: do NOT create a NEW decision when the user wants to flag an existing one. The old decision stays active until explicitly archived

32. **No bench grader triggers** (see rule 23)

33. **TRIGGER GRAMMAR must include cancel-with-reason pattern** (CLI has no native --reason flag, so workaround is cancel + comment):
    T[cancel tsk_<id> because <reason> | cancel with note]
        → task cancel tsk_<id>; lore comment add --on-table=tasks --on-id=tsk_<id> --body="cancelled: <reason>"

34. **Capture-vs-question disambiguation rule.** Question forms like "what does X check?" / "what does X do?" / "what's X's behavior?" are HOW-questions and should be answered with prose or `+"`<cmd> --help`"+`, NOT by running the command silently. Add to the ⛔ DO NOT CAPTURE block

# OUTPUT FORMAT

Respond with ONLY the compressed markdown — no preamble, no "Here is the compressed
version:", no closing remarks. Start with `+"`---`"+` (the frontmatter open) and end with the
last byte of the bundle. Aim for ~%d bytes total
`, budgetBytes, budgetBytes)
}

// extractCompressedOutput strips any leading prose like "Here is the
// compressed bundle:" before the first `---` frontmatter line. Models
// often add such preambles despite being told not to
func extractCompressedOutput(raw string) string {
	// Strip code fence wrapping if the model put the whole thing in ```markdown blocks
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "```") {
		// drop first fence line
		if idx := strings.Index(trimmed, "\n"); idx >= 0 {
			trimmed = trimmed[idx+1:]
		}
		// drop trailing fence
		trimmed = strings.TrimSpace(trimmed)
		if strings.HasSuffix(trimmed, "```") {
			trimmed = strings.TrimSpace(trimmed[:len(trimmed)-3])
		}
	}
	// Find frontmatter start
	if idx := strings.Index(trimmed, "---\n"); idx > 0 {
		// only strip if there's clearly prose before it (more than a few chars)
		if idx > 5 && !strings.HasPrefix(trimmed, "---") {
			// A fence opened inside the preamble ("Here it is:\n```markdown\n---")
			// wraps the bundle: its closing fence goes too (P3-SKILL-1).
			wrapped := strings.Contains(trimmed[:idx], codeFence)
			trimmed = trimmed[idx:]
			if t := strings.TrimSpace(trimmed); wrapped && strings.HasSuffix(t, codeFence) {
				trimmed = strings.TrimSpace(strings.TrimSuffix(t, codeFence))
			}
		}
	}
	return trimmed + "\n"
}
