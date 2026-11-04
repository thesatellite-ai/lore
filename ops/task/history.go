package task

// Activity history (TASK 17, TSQ10): every core write of a task records one
// entry per tracked field it changes, on the row itself, capped at
// tasks.history_max with the oldest entries folded into one summary.
//
//	op (edit, done, start, …) ─► o.hist().Update ─► addHistory
//	    addHistory: Get current row ─► diffHistory (tracked fields only)
//	                ─► append ─► foldHistory(tasks.history_max) ─► patch["history"]
//	    ─► engine Update (history and the change in ONE atomic write, 17.2)

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

const fieldHistory = schema.FieldTaskHistory

// History bounds (TSQ10): default 200, a policy may raise it to 1000.
const (
	defaultHistoryMax = 200
	ceilingHistoryMax = 1000
)

// histSummaryField marks the one entry a fold writes (17.2).
const histSummaryField = "_summary"

// histTimeLayout is an entry's at (RFC 3339, UTC, seconds).
const histTimeLayout = time.RFC3339

// historyTag types history entry ids (the_…); entries are not rows, the id
// is the list's merge key (StrategyListByID).
type historyTag struct{}

// Prefix implements ids.Tag.
func (historyTag) Prefix() ids.Prefix { return ids.PrefixHistoryEntry }

// HistoryEntry is one change of one tracked field (17.2). From/To are the
// text forms; "" is "none". Counts is set on a summary entry only: the
// folded entries per field, summed again when a summary is re-folded.
type HistoryEntry struct {
	ID     string         `json:"id"`
	At     string         `json:"at"`
	By     string         `json:"by,omitempty"`
	Field  string         `json:"field"`
	From   string         `json:"from,omitempty"`
	To     string         `json:"to,omitempty"`
	Note   string         `json:"note,omitempty"`
	Counts map[string]int `json:"counts,omitempty"`
}

// trackedFields are the fields history records (17.2); title and body are
// not tracked (git history keeps them; text would bloat rows).
func trackedFields() []string {
	return []string{
		work.FieldStatus, schema.FieldTaskParent, fieldAssigned, schema.FieldTaskAssignees, fieldPriority, fieldDue,
		fieldEstimate, work.FieldMission, fieldCommitment, schema.FieldTaskCloseReason, "gate", fieldRecur,
	}
}

// History decodes a task row's history, oldest first; a bad value reads as
// none (a read never fails on it).
func History(r row.Row) []HistoryEntry {
	v := r.Fields[fieldHistory]
	if v == nil || v.Kind() == value.KindNull {
		return nil
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil
	}
	var out []HistoryEntry
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	sortHistory(out)
	return out
}

// sortHistory orders by at, then id (17.3: deterministic on every machine).
func sortHistory(xs []HistoryEntry) {
	slices.SortStableFunc(xs, func(a, b HistoryEntry) int { return cmp.Or(cmp.Compare(a.At, b.At), cmp.Compare(a.ID, b.ID)) })
}

// histText is a field value as history text: lists sorted and joined,
// recur by its rule, everything else its text form; null is "".
func histText(field string, v value.Value) string {
	if v == nil || v.Kind() == value.KindNull {
		return ""
	}
	switch field {
	case schema.FieldTaskAssignees:
		items := slices.Clone(value.ItemStrings(v))
		slices.Sort(items)
		return strings.Join(items, ",")
	case fieldRecur:
		raw, err := v.MarshalJSON()
		if err != nil {
			return ""
		}
		var rc struct {
			Rule    string `json:"rule"`
			Stopped bool   `json:"stopped"`
		}
		if json.Unmarshal(raw, &rc) != nil {
			return ""
		}
		if rc.Stopped {
			return rc.Rule + " (stopped)"
		}
		return rc.Rule
	}
	return v.String()
}

// diffHistory is the entries of patch against cur: one per tracked field
// whose text changes. note goes on the status entry (open_items_at_close).
func diffHistory(cur row.Row, patch map[string]value.Value, by string, now time.Time, note string) ([]HistoryEntry, error) {
	var out []HistoryEntry
	at := now.UTC().Format(histTimeLayout)
	for _, f := range trackedFields() {
		nv, ok := patch[f]
		if !ok {
			continue
		}
		from, to := histText(f, cur.Fields[f]), histText(f, nv)
		if from == to {
			continue
		}
		id, err := ids.New[historyTag]()
		if err != nil {
			return nil, err
		}
		e := HistoryEntry{ID: id.String(), At: at, By: by, Field: f, From: from, To: to}
		if f == work.FieldStatus {
			e.Note = note
		}
		out = append(out, e)
	}
	return out, nil
}

// foldHistory keeps the newest max-1 entries plus one summary of the rest
// when the list is longer than max (TSQ10). The summary's id derives from
// the task and the newest folded entry, so two machines folding the same
// entries write the same summary and the union merge converges.
func foldHistory(task string, list []HistoryEntry, maxN int) ([]HistoryEntry, error) {
	if maxN < 2 || len(list) <= maxN {
		return list, nil
	}
	sortHistory(list)
	n := len(list) - maxN + 1
	old, keep := list[:n], list[n:]
	counts := map[string]int{}
	total := 0
	from := old[0].At
	for _, e := range old {
		if e.Field == histSummaryField {
			for k, c := range e.Counts {
				counts[k] += c
				total += c
			}
			if e.From != "" && e.From < from {
				from = e.From
			}
			continue
		}
		counts[e.Field]++
		total++
	}
	last := old[len(old)-1]
	sid, err := ids.Natural(ids.PrefixHistoryEntry, task, histSummaryField, last.ID)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "history: summary id", err)
	}
	sum := HistoryEntry{ID: sid.String(), At: last.At, By: "lore", Field: histSummaryField, From: from, To: last.At, Counts: counts,
		Note: "folded " + strconv.Itoa(total) + " entries: " + countLine(counts) + "; full detail: lore task history " + task + " --git"}
	return append([]HistoryEntry{sum}, keep...), nil
}

// countLine is "status 96, priority 18" (largest first, ties by name).
func countLine(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + " " + strconv.Itoa(counts[k])
	}
	return strings.Join(parts, ", ")
}

// putHistory writes the history patch value.
func putHistory(patch map[string]value.Value, list []HistoryEntry) error {
	sortHistory(list)
	raw, err := json.Marshal(list)
	if err != nil {
		return errs.Wrap(errs.Internal, "history: encode", err)
	}
	v, err := value.JSONOf(raw)
	if err != nil {
		return errs.Wrap(errs.Internal, "history", err)
	}
	patch[fieldHistory] = v
	return nil
}

// historyMax is the effective tasks.history_max for target (TSQ10).
func (o ops) historyMax(ctx context.Context, target store.Source) (int, error) {
	p, ok := upgrade.As[bulk.Policier](o.env)
	if !ok {
		return defaultHistoryMax, nil
	}
	set, err := p.Policy(ctx, target)
	if err != nil {
		return 0, err
	}
	n, isSet := set.Limit(policy.KeyTasksHistoryMax)
	if !isSet || n <= 0 {
		return defaultHistoryMax, nil
	}
	return int(min(n, ceilingHistoryMax)), nil
}

// histNoteKey carries a note for the status entry of the next write
// (TSQ14: open_items_at_close of a forced done).
type histNoteKey struct{}

// withHistoryNote returns ctx whose task writes note on their status entry.
func withHistoryNote(ctx context.Context, note string) context.Context {
	return context.WithValue(ctx, histNoteKey{}, note)
}

func historyNote(ctx context.Context) string {
	s, _ := ctx.Value(histNoteKey{}).(string)
	return s
}

// historyBy is the current actor id ("" when the Env cannot tell).
func (o ops) historyBy(ctx context.Context) string {
	a, ok := o.env.(Actorer)
	if !ok {
		return ""
	}
	actor, err := a.Actor(ctx)
	if err != nil || actor.IsZero() {
		return ""
	}
	return actor.String()
}

// addHistory appends patch's tracked changes to the row's history in patch.
func (o ops) addHistory(ctx context.Context, ref engine.Ref, patch map[string]value.Value) error {
	src, cur, err := o.env.Get(ctx, ref)
	if err != nil {
		return err
	}
	entries, err := diffHistory(cur, patch, o.historyBy(ctx), opclock.Now(o.env), historyNote(ctx))
	if err != nil || len(entries) == 0 {
		return err
	}
	maxN, err := o.historyMax(ctx, src)
	if err != nil {
		return err
	}
	list, err := foldHistory(cur.ID.String(), append(History(cur), entries...), maxN)
	if err != nil {
		return err
	}
	return putHistory(patch, list)
}

// histEnv is the Env every task write of this package goes through: an
// Update of a task row records history in the same write. Only Update is
// changed; work.Patch uses nothing else, so optional upgrades stay on o.env.
type histEnv struct {
	work.Env
	o ops
}

// Update implements work.Env.
func (h histEnv) Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error) {
	if ref.Table == table {
		if err := h.o.addHistory(ctx, ref, patch); err != nil {
			return engine.Written{}, err
		}
	}
	return h.Env.Update(ctx, ref, patch)
}

// hist is o.env with history recording (histEnv).
func (o ops) hist() histEnv { return histEnv{Env: o.env, o: o} }
