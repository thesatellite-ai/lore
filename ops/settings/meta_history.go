package settings

// `lore meta history <id> [<key>]` (METADATA 3.19; OV62): dated value
// changes of one row's metadata, read from the row's committed versions.
//
//	metaHistory ─► rowID ─► Env.Get (which store holds it)
//	  ─► RowHistorian.RowVersions (wire; E_UNSUPPORTED without it or on a
//	     folder store) ─► per consecutive pair: per key id, set / changed /
//	     unset ─► mask a key that is sensitive in ANY version or in its key
//	     row today (OQ2, OV49-H2: an old value is never shown because the
//	     flag was added later) ─► oldest first, "at  who  [key ]change"
//
// Not built (report): the "conflict settled" line (needs merge-commit
// reading) and the note on live history before a compaction.

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// OpMetaHistory is `lore meta history`.
const OpMetaHistory = "meta.history"

// Change words of a metadata history line.
const (
	metaHistSet     = "set"
	metaHistChanged = "changed"
	metaHistUnset   = "unset"
)

// metaHistTime is the human time of a history line.
const metaHistTime = "2006-01-02 15:04"

// MetaHistoryIn is the input of meta.history.
type MetaHistoryIn struct {
	ID  string `json:"id" arg:"0" desc:"the row (any id)"`
	Key string `json:"key,omitempty" arg:"1" desc:"only this key (name or mky_ id)"`
}

// MetaHistoryLine is one value change; From/To are display text, masked
// for a sensitive key.
type MetaHistoryLine struct {
	At     string `json:"at"`
	By     string `json:"by,omitempty"`
	ByName string `json:"by_name,omitempty"` // the actor's display name, when known
	Key    string `json:"key"`
	KeyID  string `json:"key_id"`
	Change string `json:"change"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Commit string `json:"commit"`
}

// MetaHistoryOut is the output of meta.history, oldest first.
type MetaHistoryOut struct {
	ID    string            `json:"id"`
	Count int               `json:"count"`
	Lines []MetaHistoryLine `json:"lines"`
	one   bool              // one key asked: the key column is left out
}

// Text implements op.Texter (the 3.19 sample).
func (o MetaHistoryOut) Text() string {
	if len(o.Lines) == 0 {
		return "(no metadata changes in git history)\n"
	}
	var b strings.Builder
	for _, l := range o.Lines {
		what := l.Change + " " + l.To
		switch l.Change {
		case metaHistChanged:
			what = "set " + l.To + " (was " + l.From + ")"
		case metaHistUnset:
			what = "unset (was " + l.From + ")"
		}
		if !o.one {
			what = l.Key + "  " + what
		}
		at := l.At
		if t, err := time.Parse(time.RFC3339, l.At); err == nil {
			at = t.UTC().Format(metaHistTime)
		}
		b.WriteString(at + "  " + padTo(cmp.Or(l.ByName, l.By), 14) + "  " + what + "\n")
	}
	return b.String()
}

func (o ops) metaHistoryOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpMetaHistory, Summary: "Dated metadata value changes of one row, from git history (sensitive values masked)", Permission: op.PermRead, Access: op.AccessRead,
			Errors:   append(slices.Clone(readErrors), errs.Unsupported),
			Examples: []op.Example{{Title: "one key's history", CLI: "lore meta history tsk_… cost_hours", Want: "set"}}}, o.metaHistory),
	}
}

func (o ops) metaHistory(ctx context.Context, in MetaHistoryIn) (MetaHistoryOut, error) {
	id, err := rowID(in.ID)
	if err != nil {
		return MetaHistoryOut{}, err
	}
	src, cur, err := o.env.Get(ctx, engine.Ref{ID: id})
	if err != nil {
		return MetaHistoryOut{}, err
	}
	h, ok := upgrade.As[task.RowHistorian](o.env)
	if !ok {
		return MetaHistoryOut{}, errs.New(errs.Unsupported, "meta history: this surface cannot read git history of a row").
			WithHint("run it from the lore CLI inside the repo")
	}
	vers, err := h.RowVersions(ctx, engine.Ref{Store: src, Table: cur.Table, ID: id})
	if err != nil {
		return MetaHistoryOut{}, err
	}
	rows := make([]row.Row, 0, len(vers)+1)
	for _, v := range vers {
		rows = append(rows, v.Row)
	}
	hidden := o.sensitiveKeys(ctx, append(rows, cur))
	out := MetaHistoryOut{ID: id.String(), Lines: []MetaHistoryLine{}, one: in.Key != ""}
	prev := map[ids.ID[ids.MetaKey]]meta.Entry{}
	names := map[string]string{}
	for _, v := range vers {
		next := entriesOf(v.Row)
		for _, l := range metaDiff(prev, next, hidden) {
			if in.Key != "" && l.Key != in.Key && l.KeyID != in.Key {
				continue
			}
			l.At, l.By, l.Commit = v.At.UTC().Format(time.RFC3339), v.By, v.Commit
			l.ByName = o.actorDisplay(ctx, v.By, names)
			out.Lines = append(out.Lines, l)
		}
		prev = next
	}
	out.Count = len(out.Lines)
	return out, nil
}

// actorDisplay is the display name of an act_ id ("" when unknown or not
// an actor id); memo keeps one lookup per actor.
func (o ops) actorDisplay(ctx context.Context, by string, memo map[string]string) string {
	if n, ok := memo[by]; ok {
		return n
	}
	name := ""
	if aid, err := ids.ParseAny(by); err == nil {
		if _, r, gerr := o.env.Get(ctx, engine.Ref{Table: schema.TableActors, ID: aid}); gerr == nil && !r.Deleted {
			name = text(r, actorDisplayField)
		}
	}
	memo[by] = name
	return name
}

// actorDisplayField is the actors row's display name.
const actorDisplayField = "display_name"

// entriesOf decodes a row's metadata; an entry that does not decode is
// left out (never guessed).
func entriesOf(r row.Row) map[ids.ID[ids.MetaKey]]meta.Entry {
	out := make(map[ids.ID[ids.MetaKey]]meta.Entry, len(r.Meta))
	for kid, j := range r.Meta {
		if en, err := meta.DecodeEntry(j); err == nil {
			out[kid] = en
		}
	}
	return out
}

// sensitiveKeys is every key id marked sensitive on any of rows, or by
// its meta_keys row today.
func (o ops) sensitiveKeys(ctx context.Context, rows []row.Row) map[ids.ID[ids.MetaKey]]bool {
	out := map[ids.ID[ids.MetaKey]]bool{}
	seen := map[ids.ID[ids.MetaKey]]bool{}
	for _, r := range rows {
		for kid, en := range entriesOf(r) {
			if en.Sensitive {
				out[kid] = true
			}
			if seen[kid] {
				continue
			}
			seen[kid] = true
			if _, k, err := o.env.Get(ctx, engine.Ref{Table: schema.TableMetaKeys, ID: kid.Any()}); err == nil && flag(k, mSensitive) {
				out[kid] = true
			}
		}
	}
	return out
}

// metaDiff is the lines between two versions' entries, in key-name order.
func metaDiff(prev, next map[ids.ID[ids.MetaKey]]meta.Entry, hidden map[ids.ID[ids.MetaKey]]bool) []MetaHistoryLine {
	show := func(kid ids.ID[ids.MetaKey], en meta.Entry) string {
		if hidden[kid] {
			en.Sensitive = true
		}
		return meta.Show(en)
	}
	var out []MetaHistoryLine
	for kid, n := range next {
		p, had := prev[kid]
		switch {
		case !had:
			out = append(out, MetaHistoryLine{Key: n.Name, KeyID: kid.String(), Change: metaHistSet, To: show(kid, n)})
		case string(p.V) != string(n.V):
			out = append(out, MetaHistoryLine{Key: n.Name, KeyID: kid.String(), Change: metaHistChanged, From: show(kid, p), To: show(kid, n)})
		}
	}
	for kid, p := range prev {
		if _, has := next[kid]; !has {
			out = append(out, MetaHistoryLine{Key: p.Name, KeyID: kid.String(), Change: metaHistUnset, From: show(kid, p)})
		}
	}
	sortMetaLines(out)
	return out
}

// sortMetaLines orders lines by key name, then key id.
func sortMetaLines(ls []MetaHistoryLine) {
	slices.SortFunc(ls, func(a, b MetaHistoryLine) int {
		return cmp.Or(cmp.Compare(a.Key, b.Key), cmp.Compare(a.KeyID, b.KeyID))
	})
}

// padTo pads s with spaces to n characters.
func padTo(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
