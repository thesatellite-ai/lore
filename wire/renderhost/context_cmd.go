// context_cmd.go — `lore context`, the agent's on-demand fetch command
// (CONTEXT_BUDGET_DESIGN 6.8, 6.9) plus `--internal` (§1 Commands, F20).
//
//	lore context --for-file <path>   rules/hotfixes whose globs match (why "glob …"),
//	                                 then rules/hotfixes naming the path or its folder,
//	                                 then semantic rules on the path's words,
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
//	  --meta                         each returned row's metadata (context_meta.go, 6.13)
//	lore context --internal          internal, global and personal rows (unchanged)
//
// Output never splits an entry (contextfit.Pick): a piece is full, or its
// line, or the output ends with "more: <command>". Committed-render rules
// apply: only the repo's rows and shareable stores (renderSources); the
// internal rows print only with --internal.

package renderhost

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

	"github.com/thesatellite-ai/lore/model/errs"

	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/row"
	contextop "github.com/thesatellite-ai/lore/ops/context"
	"github.com/thesatellite-ai/lore/schema"
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
	internal, topics, hookStdin     bool
	sessionStart, withMeta          bool
	table, search                   string
	forFile, topic, forPrompt, hook string
	forTask                         string
	maxBytes                        int
}

// Fetch implements contextop.Host: `lore context` is the operation
// context.fetch (ops/context), answered by the machinery below (PASS3
// ONE-PATH, P3-MISC-6).
func (h *Host) Fetch(ctx context.Context, in contextop.FetchIn, hookIn io.Reader) (a contextop.Answer, err error) {
	defer func() { err = h.hostError(err) }()
	f := &contextFlags{internal: in.Internal, topics: in.Topics, hookStdin: in.HookStdin, sessionStart: in.SessionStart,
		withMeta: in.Meta, table: in.Table, search: in.Search, forFile: in.ForFile, topic: in.Topic,
		forPrompt: in.ForPrompt, hook: in.Hook, forTask: in.ForTask, maxBytes: in.MaxBytes}
	return h.contextAnswer(ctx, f, hookIn)
}

// contextAnswer is one fetch: the text and the --json data. The mode
// order is v0.1.17's (internal, hook, topics, then the row modes).
func (h *Host) contextAnswer(ctx context.Context, f *contextFlags, stdin io.Reader) (contextop.Answer, error) {
	switch {
	case f.internal:
		if h.opts.External == nil {
			return contextop.Answer{}, errs.New(errs.Unsupported, "context --internal: not available in this build").
				WithHint("the internal and personal rows load through the release binary for now")
		}
		var b strings.Builder
		b.WriteString(renderHeader + "\n")
		n, data, err := h.opts.External(ctx, &b, f.table, f.search)
		if err != nil {
			return contextop.Answer{}, err
		}
		if n == 0 {
			b.WriteString("(nothing internal or personal loads in this repo)\n")
		}
		return contextop.Answer{Text: b.String(), Data: data}, nil
	case f.hook != "":
		// 6.9: a hook never blocks the tool (2 s timeout); every error
		// prints nothing and goes to ~/.lore/logs/hooks.log.
		hctx, cancel := context.WithTimeout(ctx, hookDeadline())
		defer cancel()
		var b strings.Builder
		if err := h.runContextHook(hctx, f, stdin, &b); err != nil {
			h.logHookError(err)
			return contextop.Answer{}, nil
		}
		return contextop.Answer{Text: b.String()}, nil
	case f.topics:
		topics, err := h.loadTopicIndex(ctx)
		if err != nil {
			return contextop.Answer{}, err
		}
		var b strings.Builder
		for _, t := range topics {
			fmt.Fprintf(&b, "%-24s %s\n", t.Name, countsText(t.Counts))
		}
		if len(topics) == 0 {
			b.WriteString("(no rows yet)\n")
		}
		return answerOf(b.String(), topics)
	}
	var res contextResult
	var err error
	switch {
	case f.forFile != "":
		res, err = h.contextForFile(ctx, f.forFile)
	case f.forTask != "":
		res, err = h.contextForTask(ctx, f.forTask)
	case f.topic != "":
		res, err = h.contextForTopic(ctx, f.topic)
	case f.forPrompt != "":
		res, err = h.contextForPrompt(ctx, f.forPrompt)
	default:
		return contextop.Answer{}, errs.New(errs.InvalidInput, errContextNeedsMode.Error())
	}
	if err != nil {
		return contextop.Answer{}, err
	}
	// 6.13: metadata only on request.
	if f.withMeta {
		if res, err = h.withMeta(ctx, res); err != nil {
			return contextop.Answer{}, err
		}
	}
	maxB := f.maxBytes
	if maxB <= 0 {
		maxB = contextDefaultMax
	}
	text, printed := res.fit(maxB)
	// 6.12 "not used in 60 days" (context_usage.go). P3-MISC-6: one fetch
	// answers both forms, so the usage count takes the rows the text
	// printed (a --json caller gets every pick, as before).
	h.recordUsage(ctx, printed)
	return answerOf(text, res.picks)
}

// answerOf is text with data encoded for --json. data is a list (the
// picks or the topic index): an empty one encodes as [], never null, so a
// --json caller that found nothing still reads a list (`.data | length`).
func answerOf[T any](text string, data []T) (contextop.Answer, error) {
	if data == nil {
		data = []T{}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return contextop.Answer{}, errs.Wrap(errs.Internal, "encode json", err)
	}
	return contextop.Answer{Text: text, Data: b}, nil
}

// contextPick is one row lore context returns, with why it was picked.
type contextPick struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Why   string `json:"why"`
	Bytes int    `json:"bytes"`
	// Meta is the row's metadata, only with --meta (context_meta.go).
	Meta  map[string]string `json:"meta,omitempty"`
	metas []pickMeta
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

// fit fits the picks under maxBytes, grouped under section headers, and
// returns the ids it printed (full or line), which the hook records in the
// session's seen set (6.9).
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
		return head + "(nothing in the store applies here; try `lore search --query \"<keywords>\"`)\n", nil
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
func (h *Host) loadKnowledge(ctx context.Context) ([]knowledgeRow, error) {
	var out []knowledgeRow
	for _, k := range contextKinds {
		rows, err := h.renderLiveRows(ctx, k.table)
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
	return contextPick{ID: id, Kind: k.kind, Why: why, Bytes: len(full), full: full, line: line, group: groupOf(k.kind), metas: metasOf(k.r.Row)}
}

// sortPicks orders picks by group (rules, hotfixes, notes) keeping the
// given order inside a group.
func sortPicks(ps []contextPick) {
	rank := map[string]int{groupRules: 0, groupHotfixes: 1, groupNotes: 2}
	sort.SliceStable(ps, func(i, j int) bool { return rank[ps[i].group] < rank[ps[j].group] })
}

// contextForFile is the 6.8 --for-file pick for the detected tool.
func (h *Host) contextForFile(ctx context.Context, p string) (contextResult, error) {
	return h.contextForFileTool(ctx, p, "")
}

// contextForFileTool is --for-file for tool ("" = detect; 6.2.8).
func (h *Host) contextForFileTool(ctx context.Context, p, tool string) (contextResult, error) {
	rows, err := h.loadKnowledge(ctx)
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
	fs := h.newFileScope(ctx, rel, tool)
	// 1. globs match the path; a glob rule the tool loads from its own
	// path rule file is not repeated (6.9).
	for _, k := range rows {
		if k.kind != "rule" && k.kind != "hotfix" || fs.pathRuleLoaded(k.r.ID.String()) {
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
	h.topicGlobPicks(ctx, rel, rows, add)
	// 5. semantic rules on the path's words (context_semantic.go).
	semanticRulePicks(rows, words, add)
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
		more = "lore search --query \"" + strings.Join(words, " ") + "\""
	}
	return contextResult{title: rel, picks: picks, more: more}, nil
}

// contextForTopic returns every row tagged name.
func (h *Host) contextForTopic(ctx context.Context, name string) (contextResult, error) {
	tags, err := h.renderLiveRows(ctx, schema.TableTags)
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
		return contextResult{}, errs.New(errs.NotFound, "no topic (tag) named "+name).WithHint("list them with `lore context --topics`")
	}
	rows, err := h.loadKnowledge(ctx)
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
func (h *Host) contextForPrompt(ctx context.Context, text string) (contextResult, error) {
	words := contextfit.Words(text)
	rows, err := h.loadKnowledge(ctx)
	if err != nil {
		return contextResult{}, err
	}
	type scored struct {
		p contextPick
		n int
	}
	var cand []scored
	// 6.8 step 5: semantic rules first, on their applies_to_description.
	semSeen := map[string]bool{}
	semanticRulePicks(rows, words, func(k knowledgeRow, why string) {
		semSeen[k.r.ID.String()] = true
		cand = append(cand, scored{pickOf(k, why), len(words) + 1})
	})
	for _, k := range rows {
		if semSeen[k.r.ID.String()] {
			continue
		}
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
		more = "lore search --query \"" + strings.Join(words, " ") + "\""
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
		// Command is Codex's apply_patch text (FA-CTX-5); its file
		// headers name the paths the patch touches.
		Command string `json:"command"`
	} `json:"tool_input"`
}

// patchFileHeaders are the apply_patch lines that name a file.
var patchFileHeaders = []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "}

// hookFilePath is the file a hook input is about: tool_input.file_path
// (Claude Code, Gemini CLI), else the first file header of a Codex
// apply_patch; "" when none.
func (in hookInput) hookFilePath() string {
	if in.ToolInput.FilePath != "" {
		return in.ToolInput.FilePath
	}
	for _, l := range strings.Split(in.ToolInput.Command, "\n") {
		for _, h := range patchFileHeaders {
			if p, ok := strings.CutPrefix(strings.TrimSpace(l), h); ok && strings.TrimSpace(p) != "" {
				return strings.TrimSpace(p)
			}
		}
	}
	return ""
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
var hookTools = map[string]bool{toolprofile.NameClaudeCode: true, toolprofile.NameCodex: true, toolprofile.NameGeminiCLI: true}

// hookDefaultEvent is the event a tool's file hook answers when the input
// names none (Gemini CLI's file hook is AfterTool, FA-CTX-5).
func hookDefaultEvent(tool string) string {
	if tool == toolprofile.NameGeminiCLI {
		return "AfterTool"
	}
	return "PreToolUse"
}

// hookTimeout is the 6.9 hook deadline.
const hookTimeout = 2 * time.Second

// hookDeadline is hookTimeout, or a test build's LORE_HOOK_TIMEOUT.
func hookDeadline() time.Duration {
	if d, ok := hookTimeoutOverride(); ok {
		return d
	}
	return hookTimeout
}

// maxHookInput bounds the hook stdin read.
const maxHookInput = 1 << 20

func (h *Host) runContextHook(ctx context.Context, f *contextFlags, stdin io.Reader, out io.Writer) error {
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
		res, err = h.contextSessionStart(ctx)
	case in.hookFilePath() != "":
		p := in.hookFilePath()
		if filepath.IsAbs(p) {
			base := in.Cwd
			if base == "" {
				base, _ = os.Getwd()
			}
			if r, rerr := filepath.Rel(base, p); rerr == nil && !strings.HasPrefix(r, "..") {
				p = r
			}
		}
		res, err = h.contextForFileTool(ctx, p, f.hook)
	case in.Prompt != "":
		res, err = h.contextForPrompt(ctx, in.Prompt)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	// 6.9: skip ids this session was already sent (machine-local seen set,
	// keyed by the tool's session id; no id, no memory).
	seen := loadHookSeen(h.loreHome(), f.hook, in.SessionID)
	seen.now = h.now
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
		if set, ok := h.policySet(ctx); ok {
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
		event = hookDefaultEvent(f.hook)
	}
	text, sent := res.fit(capB)
	h.recordUsage(ctx, sent)
	if err = json.NewEncoder(out).Encode(hookOutput{HookSpecificOutput: hookSpecific{HookEventName: event, AdditionalContext: text}}); err != nil {
		return err
	}
	return seen.add(sent)
}

// logHookError appends err to <lore home>/logs/hooks.log (6.9); best
// effort. The log is capped (hookslog.go): past hooksLogMax it moves to
// hooks.log.1 and a new one starts, so it never grows without bound.
func (h *Host) logHookError(err error) {
	appendHooksLog(h.loreHome(), h.now(), "lore context --hook: "+err.Error())
}
