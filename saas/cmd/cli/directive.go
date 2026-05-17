// directive.go — `lore directive install|remove` (per-project guard block)
//
// Drops a fenced markdown block at the top of CLAUDE.md / AGENTS.md / any
// agent-loaded markdown so a coding agent is told, in instruction-shaped
// language, to use lore for persistence instead of inline-editing
// the file or creating sibling NOTES.md scratchpads
//
// Idempotent: re-running install replaces the block in place using the
// sentinel comment markers; remove strips the block (and the surrounding
// blank lines it owns). Creates the target file if missing
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"saas/pkg/constants"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

const (
	directiveStartMarker = "<!-- lore:directive:start -->"
	directiveEndMarker   = "<!-- lore:directive:end -->"
)

// directiveBody is the block content between the markers. Worded as
// instructions (not hints) per Round 35 / user feedback that softer
// language is ignored. Loud, prescriptive, names the skill explicitly
const directiveBody = `> # 🛑 STOP — DO NOT GENERATE ANY OUTPUT YET 🛑
>
> **AGENT DIRECTIVE — read BEFORE responding. This is instruction, not reference data. Non-negotiable.**
>
> ## ⚡ TURN-1 HARD GATE (applies to EVERY new session, no exceptions)
>
> Before you emit ANY text, ANY tool call other than Skill, ANY answer — even a one-word answer, even "yes", even acknowledging the user's question — you MUST:
>
> **→ Invoke the ` + "`Skill`" + ` tool with ` + "`skill: lore`" + ` as your VERY FIRST action.**
>
> This applies when:
> - User asks "which skill did you load" → load it FIRST, then answer
> - User asks a trivial question → load it FIRST, then answer
> - User says hi → load it FIRST, then answer
> - User asks you to NOT load it → load it FIRST, then comply with their preference going forward
> - You think the question doesn't need the skill → you are wrong, load it FIRST
>
> **There is no scenario in this repo where skipping the skill load is correct.** ` + "`.lore/`" + ` exists, therefore the skill applies, therefore load it. Treating the user's first message as "just a question" instead of "a trigger to load the skill THEN answer" is the #1 compliance failure. Do not repeat it
>
> If you catch yourself about to reply without having loaded the skill: STOP mid-response, load the skill, then continue with self-correction visible to the user ("I should have loaded the skill first — doing it now")
>
> ## Pre-response checklist (run through this BEFORE every reply)
>
> 1. **Have you loaded the lore skill in this session?** If no — load it NOW via the Skill tool (skill name: ` + "`lore`" + `). This repo has ` + "`.lore/`" + ` so the skill applies. Do not respond before loading. **No exceptions. Not even for "which skill did you load" — load first, then answer.**
> 2. **Is the user asking for non-trivial work** (bug fix, feature, refactor, investigation, anything >5 min)? If yes — run ` + "`lore task search \"<keyword>\"`" + ` first to check for an existing task. If none, create one: ` + "`lore task add \"<title>\" --tasklist=<tlt_id> --commitment=<accepted|proposed|someday> [--priority=high|medium|low] --body=\"...\"`" + `. Mark ` + "`task start <T-N>`" + ` when you begin, ` + "`task done <T-N>`" + ` / ` + "`cancel <T-N>`" + ` at end
>
>    **` + "`--commitment`" + ` is REQUIRED when you (an agent) create a task — there is no default, a missing value is a hard error. Choose:**
>    - ` + "`accepted`" + ` — the user explicitly asked for this, or you are about to do it now. Appears in the default ` + "`task list`" + `.
>    - ` + "`proposed`" + ` — your own speculative idea ("we could also…"). NOT assumed real; hidden from the default list, surfaces under ` + "`lore task triage`" + `. Use this for anything the user did not explicitly request.
>    - ` + "`someday`" + ` — parking-lot idea, no commitment, no date. Surfaces under ` + "`lore task someday`" + `.
>    - To snooze an accepted task: ` + "`lore task edit <T-N> --defer-until=YYYY-MM-DD`" + ` (auto-resurfaces). ` + "`task start`" + ` / ` + "`task done`" + ` auto-promote to ` + "`accepted`" + `.
>
>    **Task body MUST preserve the original request — verbatim — plus surrounding context.** The body is the future session's only handle on what the user actually wanted. Use this template (or richer if more context is available):
>
>    ` + "```" + `
>    User asked (verbatim):
>      <quote the user's prompt exactly, including URLs, file paths, error messages>
>
>    Context:
>      URL/page: <full URL or path the user was looking at, if any>
>      Files: <files they referenced or implied>
>      Conversation thread: <one-line summary of what we'd been discussing>
>
>    Acceptance:
>      <minimum bar to call this done — derived from the user's words, not invented>
>
>    Implementation notes (only if explicitly agreed):
>      <leave empty if the user didn't specify; do NOT invent solutions>
>    ` + "```" + `
>
>    Hard rules for the body:
>    - **Quote the user verbatim** in the first section. Don't paraphrase. Don't trim. URLs / file paths / error messages / pretty IDs go in unchanged. This is the load-bearing rule — losing the original prompt = losing the context the future session needs to navigate back
>    - **Preserve URL anchors and pretty IDs** (` + "`/prj/prj_xxxx/library`" + ` stays as ` + "`/prj/prj_xxxx/library`" + `). Same for ` + "`T-19`" + `, ` + "`MS-2`" + `, ` + "`mem_018f…`" + `
>    - **Elaboration is fine** — adding inferred implementation notes, related files, architectural ideas — but it goes in the ` + "`Implementation notes`" + ` section, AFTER the verbatim quote and context. Never replace the user's words with your interpretation
>    - **Title stays short** (≤80 chars, single-line summary). Detail goes in ` + "`--body`" + `, not the title
>    - If the user's prompt is one line and zero context, the body still has the verbatim quote + a Context section noting "no additional context — single-line ad-hoc request."
> 3. **Open a run BEFORE doing the work** (this is the step most often skipped):
>
>    ` + "`RID=$(lore run start --task=<T-N> --model=<your-model-id> --agent=claude-code --goal=\"<one-line description>\")`" + `
>
>    Keep ` + "`$RID`" + ` in scope for the rest of the turn. Log significant steps as you go:
>
>    - ` + "`lore run step $RID --kind=tool --name=<tool> --summary=\"...\" --duration-ms=<n>`" + ` for tool calls
>    - ` + "`lore run step $RID --kind=decide --name=<short> --summary=\"...\"`" + ` for design choices
>    - ` + "`lore run step $RID --kind=verify --name=<check> --passed=true|false`" + ` for tests / build / lint
>    - ` + "`lore run step $RID --kind=error --summary=\"...\" --payload-stdin <<<\"<full stack>\"`" + ` for failures
>
>    Close the run at the end of the turn:
>
>    - ` + "`lore run end $RID --outcome=success --summary=\"...\"`" + ` if shipped
>    - ` + "`lore run end $RID --outcome=partial --summary=\"...\"`" + ` if partially done
>    - ` + "`lore run end $RID --outcome=failed --error=\"...\"`" + ` if blocked
>    - ` + "`lore run cancel $RID --reason=\"...\"`" + ` if scope changed mid-turn
>
>    **If the work shipped as a git commit, link it.** Right after closing the run (or whenever a commit lands):
>
>    - ` + "`lore link add --entity=<T-N> --commit=HEAD`" + ` — anchors the commit to the task
>    - ` + "`lore link add --entity=$RID --commit=HEAD`" + ` — anchors the commit to the run
>
>    Auto-captures sha + message + author + committed-at via git. Same commit can be linked to multiple entities (one commit closing two tasks: link twice). Skip when no commit was created this turn
>
>    **Even when the task is already marked done**, still open a run for verification work — the run captures what you did to confirm the state, not just net-new code. If you do truly nothing (the task is done and there's nothing to check), say so to the user explicitly instead of silently skipping
>
> 4. **Did the user give feedback / a correction / a "we always do X" statement?** Capture via ` + "`lore rule add`" + ` or ` + "`memory add`" + ` — never via Claude Code's auto-memory. **Then run ` + "`lore render`" + `** so the new rule surfaces in CLAUDE.md for the next session
>
> 5. **Before substantive responses, search for relevant captured knowledge.** One call covers all 23 entities:
>
>    ` + "`lore search \"<2-3 keywords from the user's prompt>\"`" + `
>
>    If hits return rules / decisions / hotfixes / memories that apply, factor them into your response and **cite by ID** ("per R-15, ..." / "as decided in D-7, ..."). If captures contradict your default training-data knowledge, the captures win — they're project-specific overrides
>
>    Skip search only for:
>    - Pure meta questions ("which skill did you load")
>    - Trivial one-line factual lookups with no project context
>    - Follow-up turns where you already searched the same topic this session
>
> ## Source of truth
>
> This project uses **lore** (CLI on PATH) as the single source of truth for persisted knowledge AND work tracking
> Any ` + "`<rule>`" + ` / ` + "`<hotfix>`" + ` / ` + "`<memory>`" + ` blocks elsewhere in this file are *generated output*, not the database
>
> ## Action map
>
> - **Remember / persist anything** the user said, decided, or you learned →
>   ` + "`lore memory add --body=\"<body>\"`" + ` (or ` + "`rule add`" + `, ` + "`decision add`" + `, ` + "`hotfix add`" + `, ` + "`pattern add`" + `, ` + "`playbook add`" + `, ` + "`tastepref add`" + ` — match the kind)
> - **Track work** → ` + "`task add`" + ` / ` + "`mission add`" + ` / ` + "`tasklist add`" + ` / ` + "`plan add`" + `
> - **Edit existing rows** → ` + "`lore <kind> edit <id>`" + ` (16 entities). For audited body changes on knowledge entities: ` + "`<kind> add ... --supersedes=<old_id>`" + `
> - **Find anything** → ` + "`lore <kind> search \"<query>\"`" + ` — FTS5 across 23 entities. Run this BEFORE creating a new row, every time
> - **Ingest external sources** (PR reviews, commit logs, postmortems, markdown docs) → ` + "`lore learn-from docs`" + ` — stages ` + "`learn_candidate`" + ` rows for review
>
> ## Hard prohibitions
>
> - **Do NOT** write to Claude Code's built-in auto-memory at ` + "`~/.claude/projects/*/memory/`" + ` for THIS project. That's a duplicate store that competes with lore and creates a second source-of-truth no one renders. If you find yourself about to call ` + "`Write`" + ` against ` + "`~/.claude/projects/*/memory/*.md`" + `, **stop and route to lore instead**
> - **Do NOT** add bullet points, sections, or inline notes to this file. Edits here are wiped on the next ` + "`lore render`" + `
> - **Do NOT** create sibling docs (` + "`NOTES.md`" + `, ` + "`LEARNINGS.md`" + `, ` + "`TODO.md`" + `, ` + "`.ai/**/*.md`" + ` outside structured plans) as a workaround
> - **Do NOT** skip task capture for "small" work that turns out to take 30+ minutes — create the task retroactively before going further
> - **Do NOT** answer non-trivial requests without first checking ` + "`lore task search`" + ` for duplicates
> - **Do NOT** start tool calls (Read / Write / Edit / Bash) for non-trivial work without first opening a run. If you realize you've done 2+ tool calls already without a run open, STOP, open the run retroactively with ` + "`run start ... --goal=\"<what you've been doing>\"`" + `, log the steps so far via ` + "`run step`" + `, then continue
> - **Do NOT** rely on Claude Code's TodoWrite as the system of record. TodoWrite is fine as scratch state; lore tasks + runs are canonical and persist across sessions
> - **Do NOT** replace the user's verbatim words with your interpretation. Elaboration belongs in an ` + "`Implementation notes`" + ` section AFTER the verbatim quote, not instead of it. "Add quick view button" goes in the body as "Add quick view button" first; "Sheet component with Eye icon" goes BELOW it in implementation notes
> - **Do NOT** strip URL anchors, file paths, error messages, or pretty IDs from the task body. The future session needs them to re-establish context. If the user gave a URL like ` + "`/prj/prj_xxxx/library`" + `, it goes into the body unchanged — even (especially) for one-line ad-hoc requests
>
> ## When you violate this directive
>
> If you realize mid-response that you skipped step 1 (skill load) or step 2 (task capture), say so explicitly, run the missing commands, and then continue. The user has seen agents quietly skip these; visible self-correction beats silent compliance
>
> Skill name: **lore**. ` + "`~/.claude/skills/lore/SKILL.md`" + ` has the decision tree.`

// directiveBlock is the full fenced block (markers + body + trailing newline)
func directiveBlock() string {
	return directiveStartMarker + "\n" + directiveBody + "\n" + directiveEndMarker + "\n"
}

// directiveRegex matches an existing block (across newlines, non-greedy)
var directiveRegex = regexp.MustCompile(
	`(?s)` + regexp.QuoteMeta(directiveStartMarker) + `.*?` + regexp.QuoteMeta(directiveEndMarker) + `\n?`,
)

func newDirectiveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "directive",
		Short: "Install/remove the lore agent-directive block in agent-loaded markdown",
		Long: `Installs a fenced block at the top of CLAUDE.md / AGENTS.md (or any --target
file) telling the AI agent to persist knowledge via lore commands
instead of inline-editing the file or creating sibling NOTES.md scratchpads

Idempotent: re-running install replaces the block in place. Remove strips it.`,
	}
	cmd.AddCommand(newDirectiveInstallCommand())
	cmd.AddCommand(newDirectiveRemoveCommand())
	cmd.AddCommand(newDirectiveShowCommand())
	return cmd
}

func newDirectiveInstallCommand() *cobra.Command {
	var targets []string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install (or refresh) the directive block at the top of one or more files",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(targets) == 0 {
				targets = []string{"CLAUDE.md"}
			}
			for _, t := range targets {
				if err := installDirective(t); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&targets, constants.FlagTarget, nil,
		"target file(s) — repeatable or comma-separated (default: CLAUDE.md at cwd)")
	return cmd
}

func newDirectiveRemoveCommand() *cobra.Command {
	var targets []string
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Strip the directive block from one or more files",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(targets) == 0 {
				targets = []string{"CLAUDE.md"}
			}
			for _, t := range targets {
				if err := removeDirective(t); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&targets, constants.FlagTarget, nil,
		"target file(s) — repeatable or comma-separated (default: CLAUDE.md at cwd)")
	return cmd
}

func newDirectiveShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the directive block (for piping or review) without writing any file",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Print(directiveBlock())
		},
	}
}

func installDirective(target string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return errcodes.New(errcodes.BadPath, "resolve "+target).WithCause(err)
	}

	existing := ""
	if data, err := os.ReadFile(abs); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return errcodes.New(errcodes.Internal, "read "+abs).WithCause(err)
	}

	block := directiveBlock()
	var out string
	switch {
	case directiveRegex.MatchString(existing):
		// Replace existing block in place
		out = directiveRegex.ReplaceAllString(existing, block)
	case existing == "":
		// New file
		out = block
	default:
		// Prepend with a separating blank line
		out = block + "\n" + existing
	}

	if out == existing {
		fmt.Printf("%s %s (no change)\n", style.Muted("="), abs)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return errcodes.New(errcodes.Internal, "mkdir parent").WithCause(err)
	}
	if err := os.WriteFile(abs, []byte(out), 0o644); err != nil {
		return errcodes.New(errcodes.Internal, "write "+abs).WithCause(err)
	}
	fmt.Printf("%s %s\n", style.Success("✓"), abs)
	return nil
}

func removeDirective(target string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return errcodes.New(errcodes.BadPath, "resolve "+target).WithCause(err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("%s %s (not present)\n", style.Muted("="), abs)
			return nil
		}
		return errcodes.New(errcodes.Internal, "read "+abs).WithCause(err)
	}
	existing := string(data)
	if !directiveRegex.MatchString(existing) {
		fmt.Printf("%s %s (no block found)\n", style.Muted("="), abs)
		return nil
	}
	stripped := directiveRegex.ReplaceAllString(existing, "")
	// Collapse any leading blank-line residue
	stripped = strings.TrimLeft(stripped, "\n")
	if err := os.WriteFile(abs, []byte(stripped), 0o644); err != nil {
		return errcodes.New(errcodes.Internal, "write "+abs).WithCause(err)
	}
	fmt.Printf("%s %s (directive stripped)\n", style.Success("✓"), abs)
	return nil
}
