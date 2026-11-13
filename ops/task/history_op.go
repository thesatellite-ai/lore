package task

// lore task history <id> [--git] (TASK 17.2): one time-ordered list from
// the row's history, its claim_log and (with --git) older row versions.
//
//	taskHistory ─► Get row ─► "created" line (created_at, parent)
//	            ├─ History(row)       field changes + summary folds
//	            ├─ claim_log          claim / release / reassign events
//	            └─ --git ─► RowHistorian (optional Env upgrade) ─► diff of
//	               consecutive versions on the tracked fields + title
//	            ─► sort by at, then id ─► human lines "at  who  what"

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
)

// OpHistory is `lore task history`.
const OpHistory = "task.history"

// History line kinds.
const (
	histKindCreated = "created"
	histKindField   = "field"
	histKindSummary = "summary"
	histKindClaim   = "claim"
	histKindGit     = "git"
)

// fieldClaimLog is the coordination log on a task row (LORE_TEAM_SPEC 7).
const fieldClaimLog = "claim_log"

// HistoryIn is the input of task.history.
type HistoryIn struct {
	ID  string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	Git bool   `json:"git,omitempty" flag:"git" desc:"also read older row versions from git history (full detail of folded entries)"`
}

// HistoryLine is one line of task history.
type HistoryLine struct {
	At     string `json:"at"`
	By     string `json:"by,omitempty"`
	Kind   string `json:"kind"`
	Field  string `json:"field,omitempty"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Note   string `json:"note,omitempty"`
	Commit string `json:"commit,omitempty"`
	id     string
}

// HistoryOut is the output of task.history.
type HistoryOut struct {
	ID    string        `json:"id"`
	Lines []HistoryLine `json:"lines"`
	Human string        `json:"-"`
}

// Text implements op.Texter.
func (o HistoryOut) Text() string { return o.Human }

// RowVersion is one committed version of a row file (newest last).
type RowVersion struct {
	Commit string
	At     time.Time
	By     string
	Row    row.Row
}

// RowHistorian is the optional Env upgrade --git needs: the committed
// versions of one row, oldest first. Without it --git is E_UNSUPPORTED
// (never an empty list that reads as "no older changes").
type RowHistorian interface {
	RowVersions(ctx context.Context, ref engine.Ref) ([]RowVersion, error)
}

// claimEntry is the part of a claim_log entry history shows.
type claimEntry struct {
	At          time.Time `json:"at"`
	Event       string    `json:"event"`
	ActorID     string    `json:"actor_id"`
	PrevActorID string    `json:"prev_actor_id,omitempty"`
}

func historyOps(o ops) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpHistory, Summary: "Show a task's activity history (field changes, claims; --git for older versions)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: append(slices.Clone(work.ReadErrors), errs.Unsupported),
			Examples: []op.Example{{Title: "history as JSON", CLI: "lore task history --id tsk_<id> --json", Want: `"lines"`}},
		}, o.taskHistory),
	}
}

func (o ops) taskHistory(ctx context.Context, in HistoryIn) (HistoryOut, error) {
	id, err := taskID(in.ID)
	if err != nil {
		return HistoryOut{}, err
	}
	src, r, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return HistoryOut{}, err
	}
	created := HistoryLine{At: r.CreatedAt.UTC().Format(histTimeLayout), By: work.Field(r, work.FieldCreatedBy), Kind: histKindCreated}
	if p := work.Field(r, schema.FieldTaskParent); p != "" {
		created.Note = "under " + p
	}
	lines := []HistoryLine{created}
	for _, e := range History(r) {
		l := HistoryLine{At: e.At, By: e.By, Kind: histKindField, Field: e.Field, From: e.From, To: e.To, Note: e.Note, id: e.ID}
		if e.Field == histSummaryField {
			l.Kind, l.Field, l.From, l.To = histKindSummary, "", "", ""
		}
		lines = append(lines, l)
	}
	if v := r.Fields[fieldClaimLog]; v != nil {
		if raw, merr := v.MarshalJSON(); merr == nil {
			var log []claimEntry
			if json.Unmarshal(raw, &log) == nil {
				for _, c := range log {
					l := HistoryLine{At: c.At.UTC().Format(histTimeLayout), By: c.ActorID, Kind: histKindClaim, Field: c.Event}
					if c.PrevActorID != "" {
						l.Note = "from " + c.PrevActorID
					}
					lines = append(lines, l)
				}
			}
		}
	}
	if in.Git {
		h, ok := upgrade.As[RowHistorian](o.env)
		if !ok {
			return HistoryOut{}, errs.New(errs.Unsupported, "task history --git: this surface cannot read git history of a row").
				WithHint("run it from the lore CLI inside the repo")
		}
		vers, verr := h.RowVersions(ctx, engine.Ref{Store: src, Table: table, ID: id})
		if verr != nil {
			return HistoryOut{}, verr
		}
		lines = append(lines, dedupGit(lines, gitLines(vers))...)
	}
	slices.SortStableFunc(lines, func(a, b HistoryLine) int { return cmp.Or(cmp.Compare(a.At, b.At), cmp.Compare(a.id, b.id)) })
	out := HistoryOut{ID: id.String(), Lines: lines}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(o.historyHuman(ctx, l))
	}
	out.Human = b.String()
	return out, nil
}

// gitLines diffs consecutive row versions on the tracked fields and title.
func gitLines(vers []RowVersion) []HistoryLine {
	var out []HistoryLine
	fields := append(trackedFields(), work.FieldTitle)
	for i := 1; i < len(vers); i++ {
		prev, cur := vers[i-1].Row, vers[i].Row
		for _, f := range fields {
			from, to := histText(f, prev.Fields[f]), histText(f, cur.Fields[f])
			if from == to {
				continue
			}
			out = append(out, HistoryLine{At: vers[i].At.UTC().Format(histTimeLayout), By: vers[i].By, Kind: histKindGit, Field: f, From: from, To: to,
				Commit: shortSHA(vers[i].Commit)})
		}
	}
	return out
}

// dedupGit drops git lines the row's own history already shows (same
// field, from and to; counted, so a value set twice keeps its second git
// line when history holds one): TSQ10 folds old entries into a summary,
// and --git is there to restore that detail, not to print a change twice.
func dedupGit(have, git []HistoryLine) []HistoryLine {
	type change struct{ field, from, to string }
	seen := map[change]int{}
	for _, l := range have {
		if l.Kind == histKindField {
			seen[change{l.Field, l.From, l.To}]++
		}
	}
	out := git[:0:0]
	for _, l := range git {
		k := change{l.Field, l.From, l.To}
		if seen[k] > 0 {
			seen[k]--
			continue
		}
		out = append(out, l)
	}
	return out
}

// historyHuman is one human line: "2026-11-05 09:12  mira  status todo → in_progress".
func (o ops) historyHuman(ctx context.Context, l HistoryLine) string {
	at := l.At
	if t, err := time.Parse(histTimeLayout, l.At); err == nil {
		at = t.UTC().Format(noteTimeLayout)
	}
	who := l.By
	if strings.HasPrefix(who, "act_") {
		who = o.actorName(ctx, who)
	}
	var what string
	switch l.Kind {
	case histKindCreated:
		what = "created"
	case histKindSummary:
		what = "summary"
	case histKindClaim:
		what = "claim " + l.Field
	default:
		what = l.Field + " " + orNone(l.From) + " → " + orNone(l.To)
		if l.Commit != "" {
			what += " (git " + l.Commit + ")"
		}
	}
	if l.Note != "" {
		what += " (" + l.Note + ")"
	}
	return at + "  " + padRight(who, 18) + "  " + what + "\n"
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
