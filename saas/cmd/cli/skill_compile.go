// skill_compile.go — `lore skill compile` command
//
// Reads the canonical bundle (SKILL.md + sibling docs) and asks an LLM
// to produce a compressed `mini` bundle suitable for small-context or
// weak-instruction-following models. The output is a single .md file
//
// The compiler is deliberately a wrapper around an LLM call — no
// hand-coded parsers or templates. That choice trades determinism for
// editorial flexibility: the LLM handles the "what to keep, what to
// drop" judgment that's hard to express in code. Every regeneration
// drifts slightly; treat the output as a draft, review before commit
//
// Wire-up:
//
//	lore skill compile --target=mini [--source-dir=./skill] [--output=SKILL-mini.md]
//	                      [--model=claude-sonnet-4-6]
//	                      [--budget-bytes=14000]
//
// The default model is sonnet-4-6 because compression is editorial
// work that benefits from a strong model; haiku tends to under-compress
// or drop too aggressively
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"saas/pkg/constants"
	"sort"
	"strings"
	"time"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/llmcall"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

func newSkillCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Meta-tools for the skill bundle (compile, check)",
	}
	cmd.AddCommand(newSkillCompileCommand())
	return cmd
}

type skillCompileFlags struct {
	target       string
	sourceDir    string
	output       string
	model        string
	budgetBytes  int
	temperature  float64
	timeoutSec   int
	dryRun       bool
	jsonOut      bool
	includeGlobs []string
}

func newSkillCompileCommand() *cobra.Command {
	f := &skillCompileFlags{}
	cmd := &cobra.Command{
		Use:   "compile",
		Short: "Compress the canonical bundle into a smaller .md via LLM",
		Long: `Reads every .md file under --source-dir (defaults to ./skill), concatenates
them with file headers, and asks the LLM to produce a compressed bundle
following the compression rules baked into this command

Currently supported targets:
  --target=mini    ~13KB compressed bundle; trigger DSL + few-shots; fits in 32K-ctx models

The output is written to --output (defaults to SKILL-<target>-draft.md inside
source-dir). The compiler is a DRAFT generator, not ship-ready: head-to-head
benching showed auto-compiled output scores ~70-75% vs the hand-tuned canonical
at 100%. Always bench-verify before promoting a draft over SKILL-<target>.md
The compiler is non-deterministic — re-running produces slightly different output.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillCompile(cmd.Context(), f)
		},
	}
	cmd.Flags().StringVar(&f.target, constants.FlagTarget, "mini",
		"compression target (currently only `mini` is supported)")
	cmd.Flags().StringVar(&f.sourceDir, "source-dir", "./skill",
		"directory containing the canonical bundle (.md files)")
	cmd.Flags().StringVar(&f.output, "output", "",
		"output path (default: <source-dir>/SKILL-<target>-draft.md — compiler output is NEVER canonical; bench-verify and pass --output explicitly to promote)")
	cmd.Flags().StringVar(&f.model, constants.FlagModel, "claude-sonnet-4-6",
		"compressor model — must be strong (sonnet or better); haiku tends to under-compress")
	cmd.Flags().IntVar(&f.budgetBytes, constants.FlagBudgetBytes, 14000,
		"target byte budget for the output (LLM is told to aim for this size)")
	cmd.Flags().Float64Var(&f.temperature, "temperature", 0.2,
		"sampling temperature (low for stability; raise to 0.5+ for more aggressive editorial choices)")
	cmd.Flags().IntVar(&f.timeoutSec, "timeout", 300,
		"LLM call timeout in seconds")
	cmd.Flags().BoolVar(&f.dryRun, constants.FlagDryRun, false,
		"print the result to stdout instead of writing the file")
	cmd.Flags().BoolVar(&f.jsonOut, constants.FlagJSON, false, "JSON output envelope")
	cmd.Flags().StringSliceVar(&f.includeGlobs, "include", []string{"*.md", "examples/*.md", "playbooks/*.md"},
		"file globs relative to source-dir to include (repeatable)")
	return cmd
}

func runSkillCompile(ctx context.Context, f *skillCompileFlags) error {
	if f.target != "mini" {
		return errcodes.New(errcodes.InvalidInput,
			"only --target=mini is supported in this version")
	}
	if f.output == "" {
		// Always default to a DRAFT path. The compiler is a draft generator
		// (benched ~70-75% vs hand-tuned 100%); never overwrite canonical
		// SKILL-<target>.md by accident. Caller must explicitly pass
		// --output=skill/SKILL-mini.md to promote
		f.output = filepath.Join(f.sourceDir, "SKILL-"+f.target+"-draft.md")
	}

	// 1. Collect source files
	files, totalBytes, err := collectSkillSources(f.sourceDir, f.includeGlobs)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errcodes.New(errcodes.NotFound,
			fmt.Sprintf("no source files found under %s matching globs %v", f.sourceDir, f.includeGlobs))
	}

	// 2. Build the compression prompt
	prompt := buildSkillCompressPrompt(files, f.budgetBytes)

	if !f.jsonOut {
		fmt.Printf("=== skill compile → %s ===\n", style.Code(f.target))
		fmt.Printf("  source:       %d files, %d bytes\n", len(files), totalBytes)
		fmt.Printf("  budget:       %d bytes (target)\n", f.budgetBytes)
		fmt.Printf("  model:        %s\n", f.model)
		fmt.Printf("  output:       %s\n", f.output)
		fmt.Println("  calling LLM…")
	}

	// 3. Call the LLM
	caller := llmcall.Auto()
	resp, err := caller.Call(ctx, f.model, prompt, llmcall.Options{
		Temperature: f.temperature,
		MaxTokens:   16000, // ample for ~14KB output
		Timeout:     time.Duration(f.timeoutSec) * time.Second,
	})
	if err != nil {
		return errcodes.New(errcodes.Internal, "llm call").WithCause(err)
	}

	// 4. Extract just the compressed markdown — strip any preamble like
	//    "Here is the compressed version:" before the first heading
	compressed := extractCompressedOutput(resp.Output)
	outBytes := len(compressed)

	if !f.jsonOut {
		ratio := 0.0
		if totalBytes > 0 {
			ratio = float64(totalBytes) / float64(outBytes)
		}
		fmt.Printf("\n  output size:  %d bytes (%.1f× compression)\n", outBytes, ratio)
		fmt.Printf("  est. cost:    $%.4f  (elapsed: %.1fs)\n",
			resp.CostUSDEstim, float64(resp.ElapsedMs)/1000.0)
	}

	// 5. Write or print
	if f.dryRun {
		if !f.jsonOut {
			fmt.Println()
			fmt.Println(style.Muted("─── BEGIN COMPILED OUTPUT (dry-run) ───"))
		}
		fmt.Println(compressed)
		return nil
	}

	if err := os.WriteFile(f.output, []byte(compressed), 0o644); err != nil {
		return errcodes.New(errcodes.Internal, "write output").WithCause(err)
	}

	if f.jsonOut {
		printJSON(constants.KindSkillCompile, map[string]any{
			"target":        f.target,
			"output":        f.output,
			"source_bytes":  totalBytes,
			"output_bytes":  outBytes,
			"compression_x": float64(totalBytes) / float64(outBytes),
			"cost_usd":      resp.CostUSDEstim,
			"elapsed_ms":    resp.ElapsedMs,
			"model":         f.model,
		}, 0)
	} else {
		fmt.Printf("\n  %s wrote %s\n", style.Success("✓"), f.output)
		fmt.Println(style.Muted("  diff before committing — the compiler is non-deterministic"))
	}
	return nil
}

// collectSkillSources walks source-dir and returns matching files sorted
// in a stable order. SKILL.md always comes first so the LLM sees the
// canonical structure before any sibling expansion
func collectSkillSources(sourceDir string, globs []string) (map[string]string, int, error) {
	files := map[string]string{}
	totalBytes := 0
	seen := map[string]bool{}

	for _, g := range globs {
		matches, err := filepath.Glob(filepath.Join(sourceDir, g))
		if err != nil {
			return nil, 0, errcodes.New(errcodes.InvalidInput,
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
			content, err := os.ReadFile(m)
			if err != nil {
				return nil, 0, errcodes.New(errcodes.Internal,
					fmt.Sprintf("read %s: %v", m, err))
			}
			rel, _ := filepath.Rel(sourceDir, m)
			files[rel] = string(content)
			totalBytes += len(content)
		}
	}
	return files, totalBytes, nil
}

// buildSkillCompressPrompt assembles the LLM prompt: a system-y preamble
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
	b.WriteString(fmt.Sprintf("Aim for approximately %d bytes total.\n", budgetBytes))
	return b.String()
}

// compressionRules is the heart of the compiler — it's the editorial
// brief the LLM follows. Tuned against the hand-authored SKILL-mini.md
// that scored 100% on the 3-eval bench at qwen3-coder + qwen2.5-coder:1.5b
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
          lore import --from-claude-md
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
   - ID PREFIXES (M-N, R-N, etc.)
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
    T[scratch the previous rule about X]      → rule search "X"; archive R-N; (optionally add new)
    T[update what we said about X]            → rule search "X"; rule edit R-N (or archive + add)
    T[reverse the rule about X]               → rule search "X"; archive R-N; add new with opposite content
    These must be emitted as ONE atomic shell script using `+"`$(…)`"+` command substitution to extract the rule code in-line — NOT as multi-step "Step 1 / Step 2" examples (weak models stop after step 1). Example form:
        RID=$(lore rule search "<keyword>" --json | jq -r '.data[0].code')
        lore rule archive "$RID"
        lore rule add --severity=should "<new stance>"
        lore render

17. **FEW-SHOTS section must include:**
    - A render-flags combo example: `+"`lore render --budget=4000 --dry-run`"+` for "budget-trimmed CLAUDE.md, preview only"
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
    T[anonymize captures | hide my name | anonymous mode | don't record my identity]
        → lore identity anonymize
    T[set my identity to X | record me as X]
        → lore identity set --actor="<X>"
    T[file a bug | make a support bundle | generate a bundle for the issue]
        → lore support-bundle --out=/tmp/aicoder-bundle.tar.gz
    Mention inline that `+"`support-bundle`"+` omits memory/rule content by default; `+"`--include-content`"+` opts in

22. **ENTITY MODEL must include rare entities** (don't drop them in compression):
    behaviour, cookbookrecipe, tastepref, suggestion, workspace, handoff — one line each with verb and short purpose. Critical: `+"`tastepref`"+` is distinct from `+"`memory`"+` — subjective preferences ("I prefer X", "my taste is Y") map to `+"`tastepref add`"+`, NOT `+"`memory add`"+`. Add a corresponding trigger to TRIGGER GRAMMAR:
    T[my taste preference is X | I prefer X | I strongly prefer X]
        → tastepref add "<X>"   (NOT memory_add)

23. **FEW-SHOTS must include a learn-workflow example** (two-step list+promote as ONE atomic script):
    User: I ran lore learn-from docs and got a bunch of candidates. Show me the list and accept the first three as memories
    You:
        lore learn list --json
        for id in $(lore learn list --json | jq -r '.data[0:3][].id'); do
          lore learn promote "$id" --target=memories
        done
        lore render
    Weak models stop at `+"`learn list`"+` if shown as multi-step "Step 1 / Step 2". Always use the atomic-script form for sequential ops

24. **Note the CLI gaps that don't have direct verbs.** Mention briefly that:
    - There is NO `+"`lore task block <T-N>`"+` transition (despite `+"`blocked`"+` being a valid status enum); the workaround is `+"`lore comment add --on-table=tasks --on-id=T-N --body=\"blocked: <reason>\"`"+`
    - There is NO `+"`lore audit`"+` CLI verb yet; audit-log access is raw SQL (covered in rule 20)

25. **TRIGGER GRAMMAR must include bench-meta triggers** (meta != capture). The bench commands MANAGE eval suites; do not confuse "set up an eval FOR rule R-N" with "capture rule R-N":
    T[set up a benchmark eval for X | author a bench task for rule R-N]
        → lore bench eval add --category=<…> --link=<kind>:<id> --prompt-file=- --grader-kind=programmatic --grader-cmd='…'
        # CRITICAL: do NOT emit `+"`rule add`"+` for "set up an eval for rule R-N" — that's the META verb, not capture
    T[run the benchmark | execute the eval set]
        → lore bench run start --model=<m> --runs-per-arm=<n> --parallel=<p>
    T[list benchmark evals | show me the eval set]
        → lore bench eval list --json
    T[regrade after fixing the grader]    → lore bench result regrade --run=<run>  (free; no LLM cost)
    T[live tally during a bench run]      → lore bench result stats                (no args = latest run)
    T[summary of run X | headline numbers for X]
        → lore bench report summary <X>          (no arg = latest)
    T[compare run A and run B | diff two runs]
        → lore bench report compare <A> <B>
    T[trend over time | how have we improved | bench progress]
        → lore bench report trend --since=30d [--by-model]
    T[is the delta significant | statistical analysis | p-value]
        → lore bench report analyze <run>
    T[which evals regressed | what got worse]
        → lore bench report regressions --since=last-week
    T[per-category breakdown for run X]   → lore bench report by-category <X>

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
    T[mark M-N done | mission M-N is complete | ship M-N]
        → mission done M-N
    Bundle must say explicitly: mission has its own done verb; do NOT use `+"`task done`"+` for an M-N ID

31. **TRIGGER GRAMMAR must include decision-revisit pattern** (flag without archiving):
    T[flag D-N for re-eval | revisit D-N | re-evaluate decision | conditions changed for D-N but don't archive yet]
        → lore comment add --on-table=decisions --on-id=D-N --body="REVISIT: <why>; conditions changed <when>"
          OR: lore tag attach --on-table=decisions --on-id=D-N --name=revisit
    Critical: do NOT create a NEW decision when the user wants to flag an existing one. The old decision stays active until explicitly archived

32. **TRIGGER GRAMMAR must include bench grader meta-tools:**
    T[test the grader against a sample file | check if X would pass without running LLM]
        → lore bench grader test <eval-code> --output-file=<path>   (offline; no LLM cost)
    T[why did this result fail | grader trace for one result]
        → lore bench grader debug <result-id>
    T[which graders are broken | audit graders for flakiness]
        → lore bench grader audit

33. **TRIGGER GRAMMAR must include cancel-with-reason pattern** (CLI has no native --reason flag, so workaround is cancel + comment):
    T[cancel T-N because <reason> | cancel with note]
        → task cancel T-N; lore comment add --on-table=tasks --on-id=T-N --body="cancelled: <reason>"

34. **Capture-vs-question disambiguation rule.** Question forms like "what does X check?" / "what does X do?" / "what's X's behavior?" are HOW-questions and should be answered with prose or `+"`<cmd> --help`"+`, NOT by running the command silently. Add to the ⛔ DO NOT CAPTURE block

# OUTPUT FORMAT

Respond with ONLY the compressed markdown — no preamble, no "Here is the compressed
version:", no closing remarks. Start with `+"`---`"+` (the frontmatter open) and end with the
last byte of the bundle. Aim for ~%d bytes total
`, budgetBytes, budgetBytes)
}

// extractCompressedOutput strips any leading prose like "Here is the
// compressed bundle:" before the first `---` frontmatter line. LLMs
// often add such preambles despite being told not to
func extractCompressedOutput(raw string) string {
	// Strip code fence wrapping if the LLM put the whole thing in ```markdown blocks
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
			trimmed = trimmed[idx:]
		}
	}
	return trimmed + "\n"
}
