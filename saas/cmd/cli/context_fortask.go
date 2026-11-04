// context_fortask.go — `lore context --for-task <id>` (CONTEXT_BUDGET 6.8):
// the task, its plan and links (linked resources included, RES-OQ3b),
// then what applies to the files it names.
//
//	--for-task tsk_… ─▶ task.show op (title, body, tags, plan, mission, --ref pointers)
//	    group "Task": the task, its plan and mission, each ref target (res_ too)
//	        a row outside the committed-render sources prints as one id line
//	        (internal: `lore show <id>`), never its text (CFG6)
//	    rules/hotfixes: globs matching a path the title/body names, then
//	        bodies naming that path (the --for-file picks per path)
//	    rows tagged with the task's tags ("topic <name>")
//	    notes (memories, decisions) on the title/body words, up to 5
//
// Output fits like every other mode (contextResult.text: never splits an
// entry; "more: lore search …").
package main

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strings"

	"saas/pkg/aicoder/errcodes"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// groupTask is the section of the task and the rows it points at.
const groupTask = "Task"

// Kinds of the task-side picks.
const (
	ctxKindTask    = "task"
	ctxKindLinked  = "linked"
	ctxKindPlan    = "plan"
	ctxKindMission = "mission"
)

// taskPathRe finds the file paths a task names: a token with a folder
// ("services/billing/invoice.go", "web/src/") or a file name with an
// extension ("invoice.go"). URLs are dropped by the caller.
var taskPathRe = regexp.MustCompile(`[A-Za-z0-9_.\-]+(?:/[A-Za-z0-9_.\-]+)+/?|[A-Za-z0-9_\-]+\.[A-Za-z][A-Za-z0-9]{0,5}\b`)

// contextForTask is the 6.8 --for-task pick.
func contextForTask(ctx context.Context, raw string) (contextResult, error) {
	t, err := loadTaskForContext(ctx, raw)
	if err != nil {
		return contextResult{}, err
	}
	srcs, err := renderSources(ctx)
	if err != nil {
		return contextResult{}, err
	}
	var picks []contextPick
	seen := map[string]bool{}
	// The task and what it points at (plan, mission, --ref targets).
	picks = append(picks, linkedPick(ctx, srcs, ctxKindTask, t.ID, "the task"))
	seen[t.ID] = true
	for _, l := range []struct{ kind, id string }{{ctxKindPlan, t.PlanID}, {ctxKindMission, t.MissionID}} {
		if l.id != "" && !seen[l.id] {
			seen[l.id] = true
			picks = append(picks, linkedPick(ctx, srcs, l.kind, l.id, l.kind+" of the task"))
		}
	}
	for _, r := range t.Refs {
		if r.Unavailable || seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		picks = append(picks, linkedPick(ctx, srcs, ctxKindLinked, r.ID, "ref of the task ("+r.Relation+")"))
	}

	rows, err := loadKnowledge(ctx)
	if err != nil {
		return contextResult{}, err
	}
	add := func(k knowledgeRow, why string) {
		if id := k.r.ID.String(); !seen[id] {
			seen[id] = true
			picks = append(picks, pickOf(k, why))
		}
	}
	// What applies to the files the task names (the --for-file picks).
	for _, p := range taskPaths(t.Title + "\n" + t.Body) {
		fr, ferr := contextForFile(ctx, p)
		if ferr != nil {
			return contextResult{}, ferr
		}
		for _, fp := range fr.picks {
			if fp.group != groupNotes && !seen[fp.ID] {
				seen[fp.ID] = true
				fp.Why = "names " + p + ": " + fp.Why
				picks = append(picks, fp)
			}
		}
	}
	// Rows sharing a tag with the task.
	if len(t.TagIDs) > 0 {
		names := tagNames(ctx)
		for _, k := range rows {
			for _, tid := range renderTagIDs(k.r.Row) {
				if slices.Contains(t.TagIDs, tid) {
					add(k, "topic "+names[tid])
					break
				}
			}
		}
	}
	// Notes on the task's words, best first.
	words := contextfit.Words(t.Title + " " + t.Body)
	type scored struct {
		k knowledgeRow
		n int
	}
	var cand []scored
	for _, k := range rows {
		if (k.kind != "memory" && k.kind != "decision") || seen[k.r.ID.String()] {
			continue
		}
		if n := contextfit.Score(renderField(k.r.Row, renderFieldTitle)+" "+renderField(k.r.Row, renderFieldBody), words); n > 0 {
			cand = append(cand, scored{k, n})
		}
	}
	sort.SliceStable(cand, func(i, j int) bool { return cand[i].n > cand[j].n })
	for i, c := range cand {
		if i >= contextNotesMax {
			break
		}
		add(c.k, "words")
	}
	sortTaskPicks(picks)
	more := ""
	if len(words) > 0 {
		more = "lore search \"" + strings.Join(words, " ") + "\""
	}
	return contextResult{title: "task " + t.ID, picks: picks, more: more}, nil
}

// loadTaskForContext runs task.show (the same read and refs as `lore task
// show`) and returns its data.
func loadTaskForContext(ctx context.Context, raw string) (opstask.Full, error) {
	if coreApp == nil {
		return opstask.Full{}, errcodes.New(errcodes.Internal, "the core is not mounted")
	}
	e, ok := coreApp.Registry().Lookup(opstask.OpShow)
	if !ok {
		return opstask.Full{}, errcodes.New(errcodes.Internal, opstask.OpShow+" is not registered")
	}
	in, err := json.Marshal(opstask.ShowIn{ID: strings.TrimSpace(raw)})
	if err != nil {
		return opstask.Full{}, errcodes.New(errcodes.Internal, "encode the task id").WithCause(err)
	}
	res, err := coreApp.Run(ctx, e, op.JSONDecoder{Body: in}, op.Caller{})
	if err != nil {
		return opstask.Full{}, coreErr(err)
	}
	b, err := res.JSON()
	if err != nil {
		return opstask.Full{}, coreErr(err)
	}
	var out opstask.ShowOut
	if err := json.Unmarshal(b, &out); err != nil {
		return opstask.Full{}, errcodes.New(errcodes.Internal, "decode task.show").WithCause(err)
	}
	return out.Data, nil
}

// linkedPick is one row the task points at. A row outside the committed
// render sources (an internal or personal store) prints as its id only
// (CFG6: internal text only through --internal or the private files).
func linkedPick(ctx context.Context, srcs []store.Source, kind, rawID, why string) contextPick {
	pick := contextPick{ID: rawID, Kind: kind, Why: why, group: groupTask}
	unseen := func(note string) contextPick {
		pick.line = contextfit.LineForm(rawID, note)
		pick.full = pick.line
		pick.Bytes = len(pick.full)
		return pick
	}
	id, err := ids.ParseAny(rawID)
	if err != nil {
		return unseen("(not a row id)")
	}
	src, r, err := coreApp.Get(ctx, engine.Ref{ID: id})
	if err != nil || r.Deleted {
		return unseen("(not available here)")
	}
	if !slices.Contains(srcs, src) {
		return unseen("(in an internal store; `lore show " + rawID + "`)")
	}
	if kind == ctxKindLinked {
		pick.Kind = string(r.Table)
	}
	title := renderField(r, renderFieldTitle)
	if title == "" {
		title = renderField(r, privateFieldName)
	}
	body := renderField(r, renderFieldBody)
	if body == "" {
		body = renderField(r, privateFieldDescription)
	}
	full := "<" + pick.Kind + " id=\"" + rawID + "\" why=\"" + why + "\">\n"
	if title != "" {
		full += "**" + title + "**\n\n"
	}
	if st := renderField(r, privateFieldStatus); st != "" {
		full += "status: " + st + "\n\n"
	}
	if body != "" {
		full += body + "\n"
	}
	full += "</" + pick.Kind + ">\n\n"
	pick.full, pick.Bytes = full, len(full)
	pick.line = contextfit.LineForm(rawID, contextfit.ShortForm(rawID, renderField(r, schema.FieldSummary), title, body))
	return pick
}

// taskPaths are the file paths a task's text names, in order, URLs out.
func taskPaths(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		if strings.Contains(f, "://") {
			continue // a URL, not a path
		}
		for _, m := range taskPathRe.FindAllString(f, -1) {
			m = strings.TrimRight(m, ".")
			if m != "" && !slices.Contains(out, m) {
				out = append(out, m)
			}
		}
	}
	return out
}

// tagNames maps tag ids to names across the render sources.
func tagNames(ctx context.Context) map[string]string {
	out := map[string]string{}
	tags, err := renderLiveRows(ctx, schema.TableTags)
	if err != nil {
		return out
	}
	for _, t := range tags {
		out[t.ID.String()] = renderField(t.Row, privateFieldName)
	}
	return out
}

// sortTaskPicks: the task group first, then rules, hotfixes, notes.
func sortTaskPicks(ps []contextPick) {
	rank := map[string]int{groupTask: 0, groupRules: 1, groupHotfixes: 2, groupNotes: 3}
	sort.SliceStable(ps, func(i, j int) bool { return rank[ps[i].group] < rank[ps[j].group] })
}
