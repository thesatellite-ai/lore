// context_cmd.go — `lore context`, the agent's on-demand fetch command
// (CONTEXT_BUDGET_DESIGN 6.8, 6.9) plus `--internal` (§1 Commands, F20).
//
//	lore context --for-file <path>   rules/hotfixes whose globs match (why "glob …"),
//	                                 then rules/hotfixes naming the path or its folder,
//	                                 then up to 5 memories/decisions on the path's words
//	lore context --for-task <id>     the task, its plan/mission/refs, then what applies to
//	                                 the files it names, its tags and words (context_fortask.go)
//	lore context --topic <name>      every row tagged <name>, rules and hotfixes first
//	lore context --topics            the topic index as a list
//	lore context --for-prompt "<t>"  rows matching the prompt's words, best first
//	lore context --hook <tool> --hook-stdin
//	                                 read the tool's hook JSON (file path / prompt),
//	                                 answer in its format, capped at context.hook_cap;
//	                                 never fails the tool: any error prints nothing;
//	                                 skips ids already sent this session (context_hookseen.go)
//	  --session-start                SessionStart: in-flight tasks + rows changed since
//	                                 the last session (context_sessionstart.go)
//	  --max-bytes <n>                cap (default 16,000; hooks: context.hook_cap)
//	lore context --internal          internal, global and personal rows (unchanged)
//
// Output never splits an entry (contextfit.Pick): a piece is full, or its
// line, or the output ends with "more: <command>". Committed-render rules
// apply: only the repo's rows and shareable stores (renderSources); the
// internal rows print only with --internal.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/constants"

	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"

	"github.com/spf13/cobra"
)

// Flags of lore context.
const (
	flagContextInternal  = "internal"
	flagContextTable     = "table"
	flagContextSearch    = "search"
	flagContextForFile   = "for-file"
	flagContextForTask   = "for-task"
	flagContextTopic     = "topic"
	flagContextTopics    = "topics"
	flagContextForPrompt = "for-prompt"
	flagContextHook      = "hook"
	flagContextHookStdin = "hook-stdin"
	flagContextMaxBytes  = "max-bytes"
	// flagContextSessionStart answers the SessionStart hook (6.9).
	flagContextSessionStart = "session-start"
)

// Caps (6.8): the default output cap, the hook cap when the policy cannot
// be read, and how many notes --for-file adds.
const (
	contextDefaultMax  = 16000
	contextHookCapFall = 9000
	contextNotesMax    = 5
	contextMoreReserve = 200
	// scopeNoteBoost lifts a note scoped to the path's folder chain above
	// any word match (CQ12: a scoped memory beats an unscoped one).
	scopeNoteBoost = 1000
)

// errContextNeedsMode: one of the modes is required.
var errContextNeedsMode = errors.New("pass --for-file <path>, --for-task <id>, --topic <name>, --topics, --for-prompt <text>, --hook <tool> --hook-stdin, or --internal")

type contextFlags struct {
	internal, asJSON, topics, hookStdin bool
	sessionStart                        bool
	table, search                       string
	forFile, topic, forPrompt, hook     string
	forTask                             string
	maxBytes                            int
}

func newContextCommand() *cobra.Command {
	f := &contextFlags{}
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Print the rules, hotfixes and notes for a file, topic or prompt (the agent's fetch command)",
		Example: `  lore context --for-file services/billing/invoice.go
  lore context --for-task tsk_01jf8e5w…
  lore context --topic billing
  lore context --topics
  lore context --for-prompt "invoice rounding"
  cat hook.json | lore context --hook claude_code --hook-stdin
  lore context --internal --table rules --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runContext(cmd.Context(), f, os.Stdin, os.Stdout)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&f.internal, flagContextInternal, false, "internal, global and personal rows (outside this checkout)")
	fl.StringVar(&f.table, flagContextTable, "", "with --internal: only this table (rules, memories, decisions, tasks, …)")
	fl.StringVar(&f.search, flagContextSearch, "", "with --internal: only rows whose title or body contains this text")
	fl.StringVar(&f.forFile, flagContextForFile, "", "rules, hotfixes and notes for this path")
	fl.StringVar(&f.forTask, flagContextForTask, "", "the task, its plan and links, then what applies to the files it names")
	fl.StringVar(&f.topic, flagContextTopic, "", "every row tagged with this topic")
	fl.BoolVar(&f.topics, flagContextTopics, false, "list the topics with their counts")
	fl.StringVar(&f.forPrompt, flagContextForPrompt, "", "rows matching this free text")
	fl.StringVar(&f.hook, flagContextHook, "", "answer a tool hook (claude_code, codex)")
	fl.BoolVar(&f.hookStdin, flagContextHookStdin, false, "with --hook: read the hook input JSON from stdin")
	fl.BoolVar(&f.sessionStart, flagContextSessionStart, false, "with --hook: answer SessionStart (in-flight tasks, rows changed since your last session)")
	fl.IntVar(&f.maxBytes, flagContextMaxBytes, 0, "cap the output in bytes (default 16000; hooks: context.hook_cap)")
	fl.BoolVar(&f.asJSON, constants.FlagJSON, false, "machine-readable output")
	return cmd
}

func runContext(ctx context.Context, f *contextFlags, stdin io.Reader, out io.Writer) error {
	switch {
	case f.internal:
		if f.asJSON {
			return writeJSONOut(map[string]any{"data": externalJSONRows(ctx, f.table, f.search)})
		}
		var b strings.Builder
		b.WriteString(renderHeader + "\n")
		if n := printExternalRows(ctx, &b, f.table, f.search); n == 0 {
			b.WriteString("(nothing internal or personal loads in this repo)\n")
		}
		return writeOut(out, b.String())
	case f.hook != "":
		// 6.9: a hook never blocks the tool (2 s timeout); every error
		// prints nothing and goes to ~/.lore/logs/hooks.log.
		hctx, cancel := context.WithTimeout(ctx, hookTimeout)
		defer cancel()
		if err := runContextHook(hctx, f, stdin, out); err != nil {
			logHookError(err)
		}
		return nil
	case f.topics:
		topics, err := loadTopicIndex(ctx)
		if err != nil {
			return err
		}
		if f.asJSON {
			return writeJSONOut(map[string]any{"data": topics})
		}
		var b strings.Builder
		for _, t := range topics {
			fmt.Fprintf(&b, "%-24s %s\n", t.Name, countsText(t.Counts))
		}
		if len(topics) == 0 {
			b.WriteString("(no rows yet)\n")
		}
		return writeOut(out, b.String())
	}
	var res contextResult
	var err error
	switch {
	case f.forFile != "":
		res, err = contextForFile(ctx, f.forFile)
	case f.forTask != "":
		res, err = contextForTask(ctx, f.forTask)
	case f.topic != "":
		res, err = contextForTopic(ctx, f.topic)
	case f.forPrompt != "":
		res, err = contextForPrompt(ctx, f.forPrompt)
	default:
		return errcodes.New(errcodes.InvalidInput, errContextNeedsMode.Error())
	}
	if err != nil {
		return err
	}
	if f.asJSON {
		return writeJSONOut(map[string]any{"data": res.picks})
	}
	maxB := f.maxBytes
	if maxB <= 0 {
		maxB = contextDefaultMax
	}
	return writeOut(out, res.text(maxB))
}

// writeOut writes s to out.
func writeOut(out io.Writer, s string) error {
	if _, err := io.WriteString(out, s); err != nil {
		return errcodes.New(errcodes.Internal, "write output").WithCause(err)
	}
	return nil
}

// contextPick is one row lore context returns, with why it was picked.
type contextPick struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Why   string `json:"why"`
	Bytes int    `json:"bytes"`
	full  string
	line  string
	group string
}

// contextResult is a titled list of picks and the command for the rest.
type contextResult struct {
	title string
	picks []contextPick
	more  string
}

// text fits the picks under maxBytes, grouped under section headers.
func (r contextResult) text(maxBytes int) string {
	s, _ := r.fit(maxBytes)
	return s
}

// fit is text plus the ids it printed (full or line), which the hook
// records in the session's seen set (6.9).
func (r contextResult) fit(maxBytes int) (string, []string) {
	head := fmt.Sprintf("# lore context for %s\n\n", r.title)
	var pieces []contextfit.Piece
	group := ""
	for _, p := range r.picks {
		if p.group != group {
			group = p.group
			h := "## " + group + "\n\n"
			pieces = append(pieces, contextfit.Piece{Full: h, Line: h})
		}
		pieces = append(pieces, contextfit.Piece{ID: p.ID, Full: p.full, Line: p.line})
	}
	if len(pieces) == 0 {
		return head + "(nothing in the store applies here; try `lore search \"<keywords>\"`)\n", nil
	}
	got := contextfit.Pick(pieces, maxBytes-len(head), contextMoreReserve)
	s := head + got.Text
	if got.More || r.more != "" {
		s += "\nmore: " + r.more + "\n"
	}
	return s, got.IDs
}

// without drops the picks whose id is in seen (the hook's session set).
func (r contextResult) without(seen map[string]bool) contextResult {
	if len(seen) == 0 {
		return r
	}
	keep := r.picks[:0:0]
	for _, p := range r.picks {
		if !seen[p.ID] {
			keep = append(keep, p)
		}
	}
	r.picks = keep
	return r
}

// Groups (section titles) of picks.
const (
	groupRules    = "Rules that apply here"
	groupHotfixes = "Hotfixes"
	groupNotes    = "Related notes"
)

// knowledgeRow is one live row of a context table with its kind.
type knowledgeRow struct {
	kind string
	r    renderRow
}

// contextKinds are the tables lore context reads, in output order.
var contextKinds = []struct {
	table row.Table
	kind  string
	group string
}{
	{schema.TableRules, "rule", groupRules},
	{schema.TableHotfixes, "hotfix", groupHotfixes},
	{schema.TableMemories, "memory", groupNotes},
	{schema.TableDecisions, "decision", groupNotes},
	// CONTEXT_BUDGET 6.2.1 / CQ12, OV53-NOTES: patterns and architecture notes
	// carry --scope too, so their folder scope ranks them like memories.
	{schema.TablePatterns, "pattern", groupNotes},
	{schema.TableArchitectureNotes, "architecture-note", groupNotes},
}

// loadKnowledge lists the live, not superseded rows of every context
// table across the committed-render sources.
func loadKnowledge(ctx context.Context) ([]knowledgeRow, error) {
	var out []knowledgeRow
	for _, k := range contextKinds {
		rows, err := renderLiveRows(ctx, k.table)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if renderField(r.Row, renderFieldSuperseded) != "" {
				continue
			}
			out = append(out, knowledgeRow{kind: k.kind, r: r})
		}
	}
	return out, nil
}

// groupOf is the section a kind prints under.
func groupOf(kind string) string {
	for _, k := range contextKinds {
		if k.kind == kind {
			return k.group
		}
	}
	return groupNotes
}

// pickOf renders one row as a pick (its full tag block and its line).
func pickOf(k knowledgeRow, why string) contextPick {
	id := k.r.ID.String()
	sev := renderField(k.r.Row, renderFieldSeverity)
	title := renderField(k.r.Row, renderFieldTitle)
	body := renderField(k.r.Row, renderFieldBody)
	attrs := fmt.Sprintf("id=%q", id)
	if sev != "" {
		attrs += fmt.Sprintf(" severity=%q", sev)
	}
	attrs += fmt.Sprintf(" why=%q", why)
	full := "<" + k.kind + " " + attrs + ">\n"
	if title != "" {
		full += "**" + title + "**\n\n"
	}
	full += body + "\n</" + k.kind + ">\n\n"
	line := contextfit.LineForm(id, contextfit.ShortForm(id, renderField(k.r.Row, schema.FieldSummary), title, body))
	return contextPick{ID: id, Kind: k.kind, Why: why, Bytes: len(full), full: full, line: line, group: groupOf(k.kind)}
}

// sortPicks orders picks by group (rules, hotfixes, notes) keeping the
// given order inside a group.
func sortPicks(ps []contextPick) {
	rank := map[string]int{groupRules: 0, groupHotfixes: 1, groupNotes: 2}
	sort.SliceStable(ps, func(i, j int) bool { return rank[ps[i].group] < rank[ps[j].group] })
}

// contextForFile is the 6.8 --for-file pick for the detected tool.
func contextForFile(ctx context.Context, p string) (contextResult, error) {
	return contextForFileTool(ctx, p, "")
}

// contextForFileTool is --for-file for tool ("" = detect; 6.2.8).
func contextForFileTool(ctx context.Context, p, tool string) (contextResult, error) {
	rows, err := loadKnowledge(ctx)
	if err != nil {
		return contextResult{}, err
	}
	rel := filepath.ToSlash(filepath.Clean(p))
	dir := path.Dir(rel)
	words := contextfit.Words(rel)
	var picks, notes []contextPick
	seen := map[string]bool{}
	add := func(k knowledgeRow, why string) {
		if !seen[k.r.ID.String()] {
			seen[k.r.ID.String()] = true
			picks = append(picks, pickOf(k, why))
		}
	}
	// 1. globs match the path.
	for _, k := range rows {
		if k.kind != "rule" && k.kind != "hotfix" {
			continue
		}
		for _, g := range contextfit.SplitGlobs(renderField(k.r.Row, renderFieldGlobs)) {
			if contextfit.MatchGlob(g, rel) {
				add(k, "glob "+g)
				break
			}
		}
	}
	// 2. the folder scope chain, innermost first (6.2.2, 6.2.8).
	fs := newFileScope(ctx, rel, tool)
	scopeChainPicks(fs, rows, add, func(cp contextPick) {
		if !seen[cp.ID] {
			seen[cp.ID] = true
			picks = append(picks, cp)
		}
	})
	// 3. the body names the path or its folder.
	for _, k := range rows {
		if k.kind != "rule" && k.kind != "hotfix" {
			continue
		}
		body := renderField(k.r.Row, renderFieldBody)
		switch {
		case strings.Contains(body, rel):
			add(k, "mentions "+rel)
		case dir != "." && strings.Contains(body, dir+"/"):
			add(k, "mentions "+dir+"/")
		}
	}
	// 4. rows tagged with a topic whose glob set covers the path (6.10).
	topicGlobPicks(ctx, rel, rows, add)
	// 6. notes on the path's words, best first, up to 5; notes scoped to
	// the path's folder chain rank first (CQ12), innermost first.
	type scored struct {
		k knowledgeRow
		n int
	}
	var cand []scored
	for _, k := range rows {
		if k.kind != "memory" && k.kind != "decision" {
			continue
		}
		n := contextfit.Score(renderField(k.r.Row, renderFieldTitle)+" "+renderField(k.r.Row, renderFieldBody), words)
		if at := fs.inChain(fs.folderOf(k)); at >= 0 {
			n += scopeNoteBoost * (len(fs.chain) - at)
		}
		if n > 0 {
			cand = append(cand, scored{k, n})
		}
	}
	sort.SliceStable(cand, func(i, j int) bool { return cand[i].n > cand[j].n })
	for i, c := range cand {
		if i >= contextNotesMax {
			break
		}
		why := "words " + strings.Join(words, " ")
		if d := fs.folderOf(c.k); fs.inChain(d) >= 0 {
			why = "scope dir:" + d
		}
		notes = append(notes, pickOf(c.k, why))
	}
	picks = applyOverrides(append(picks, notes...), rows)
	sortPicks(picks)
	more := ""
	if len(cand) > contextNotesMax || len(words) > 0 {
		more = "lore search \"" + strings.Join(words, " ") + "\""
	}
	return contextResult{title: rel, picks: picks, more: more}, nil
}

// contextForTopic returns every row tagged name.
func contextForTopic(ctx context.Context, name string) (contextResult, error) {
	tags, err := renderLiveRows(ctx, schema.TableTags)
	if err != nil {
		return contextResult{}, err
	}
	want := map[string]bool{}
	for _, t := range tags {
		if strings.EqualFold(renderField(t.Row, "name"), name) {
			want[t.ID.String()] = true
		}
	}
	if len(want) == 0 {
		return contextResult{}, errcodes.New(errcodes.NotFound, "no topic (tag) named "+name).
			WithHint("list them with `lore context --topics`")
	}
	rows, err := loadKnowledge(ctx)
	if err != nil {
		return contextResult{}, err
	}
	var picks []contextPick
	for _, k := range rows {
		for _, id := range renderTagIDs(k.r.Row) {
			if want[id] {
				picks = append(picks, pickOf(k, "topic "+name))
				break
			}
		}
	}
	sortPicks(picks)
	return contextResult{title: "topic " + name, picks: picks}, nil
}

// contextForPrompt returns rows matching the prompt's words, best first.
func contextForPrompt(ctx context.Context, text string) (contextResult, error) {
	words := contextfit.Words(text)
	rows, err := loadKnowledge(ctx)
	if err != nil {
		return contextResult{}, err
	}
	type scored struct {
		p contextPick
		n int
	}
	var cand []scored
	for _, k := range rows {
		n := contextfit.Score(renderField(k.r.Row, renderFieldTitle)+" "+renderField(k.r.Row, renderFieldBody), words)
		if n > 0 {
			cand = append(cand, scored{pickOf(k, "words"), n})
		}
	}
	sort.SliceStable(cand, func(i, j int) bool { return cand[i].n > cand[j].n })
	picks := make([]contextPick, 0, len(cand))
	for _, c := range cand {
		picks = append(picks, c.p)
	}
	sortPicks(picks)
	more := ""
	if len(words) > 0 {
		more = "lore search \"" + strings.Join(words, " ") + "\""
	}
	return contextResult{title: "the prompt", picks: picks, more: more}, nil
}

// hookInput is the part of a tool's hook JSON lore reads: Claude Code and
// Codex send tool_input.file_path on PreToolUse and prompt on
// UserPromptSubmit (CONTEXT_BUDGET 6.9).
type hookInput struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	Prompt        string `json:"prompt"`
	Cwd           string `json:"cwd"`
	ToolInput     struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
}

// hookOutput is the answer shape (hookSpecificOutput.additionalContext).
type hookOutput struct {
	HookSpecificOutput hookSpecific `json:"hookSpecificOutput"`
}

type hookSpecific struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// hookTools are the tools whose hook format lore answers (6.9 table).
var hookTools = map[string]bool{"claude_code": true, "codex": true}

// hookTimeout is the 6.9 hook deadline.
const hookTimeout = 2 * time.Second

// maxHookInput bounds the hook stdin read.
const maxHookInput = 1 << 20

func runContextHook(ctx context.Context, f *contextFlags, stdin io.Reader, out io.Writer) error {
	if !hookTools[f.hook] {
		return fmt.Errorf("no hook format for tool %q", f.hook)
	}
	if !f.hookStdin {
		return errors.New("--hook needs --hook-stdin")
	}
	data, err := io.ReadAll(io.LimitReader(stdin, maxHookInput))
	if err != nil {
		return err
	}
	var in hookInput
	if err = json.Unmarshal(data, &in); err != nil {
		return err
	}
	var res contextResult
	startEvent := f.sessionStart || in.HookEventName == hookEventSessionStart
	switch {
	case startEvent:
		res, err = contextSessionStart(ctx)
	case in.ToolInput.FilePath != "":
		p := in.ToolInput.FilePath
		if filepath.IsAbs(p) {
			base := in.Cwd
			if base == "" {
				base, _ = os.Getwd()
			}
			if r, rerr := filepath.Rel(base, p); rerr == nil && !strings.HasPrefix(r, "..") {
				p = r
			}
		}
		res, err = contextForFileTool(ctx, p, f.hook)
	case in.Prompt != "":
		res, err = contextForPrompt(ctx, in.Prompt)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	// 6.9: skip ids this session was already sent (machine-local seen set,
	// keyed by the tool's session id; no id, no memory).
	seen := loadHookSeen(f.hook, in.SessionID)
	if startEvent {
		seen = hookSeen{} // the start lines never mute the per-file hook
	}
	res = res.without(seen.ids)
	if len(res.picks) == 0 {
		return nil // nothing new to say: print nothing
	}
	capB := f.maxBytes
	switch {
	case capB > 0:
	case startEvent:
		capB = contextSessionStartCap
	default:
		capB = contextHookCapFall
		if set, ok := policySet(ctx); ok {
			if n, found := set.Limit(policy.KeyContextHookCap); found {
				capB = int(n)
			}
		}
	}
	event := in.HookEventName
	switch {
	case startEvent:
		event = hookEventSessionStart
	case event == "":
		event = "PreToolUse"
	}
	text, sent := res.fit(capB)
	if err = json.NewEncoder(out).Encode(hookOutput{HookSpecificOutput: hookSpecific{HookEventName: event, AdditionalContext: text}}); err != nil {
		return err
	}
	return seen.add(sent)
}

// logHookError appends err to ~/.lore/logs/hooks.log (6.9); best effort.
func logHookError(err error) {
	home, herr := os.UserHomeDir()
	if herr != nil {
		return
	}
	dir := filepath.Join(home, ".lore", "logs")
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	fh, ferr := os.OpenFile(filepath.Clean(filepath.Join(dir, "hooks.log")), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if ferr != nil {
		return
	}
	_, _ = fmt.Fprintf(fh, "%s lore context --hook: %v\n", cliClock().Now().Format(time.RFC3339), err)
	_ = fh.Close() // best effort: a hook log failure must not reach the tool
}
