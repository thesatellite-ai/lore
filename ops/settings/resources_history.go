package settings

// Resource history, report and quarantine (RESOURCES 3.15, 3.20/3.22).
//
//	write (add, edit, bulk edit, import, repoint, merge, lint --fix, retype)
//	  ─► withHistory: diff old fields vs patch fields ─► one entry per changed
//	     field {id, at, by, batch, field, from, to} (sensitive/internal masked)
//	     ─► capped at resourceHistoryMax ─► patch["history"] ─► Env.Create|Update
//	resource history <name> [--field F] [--git] ─► decode row history
//	     (+ --git: field diffs of the row's committed versions, OV62,
//	     unknown fields masked) ─► newest first
//	resource report [--kind K] [--days N] ─► count per kind, stale, unchecked,
//	     lint findings (dangling, quarantined, …), open links, secret_ref names
//	quarantineOf(kind, stored) ─► fields whose stored value does not fit their
//	     type or allowed values (computed on read: same rows, same answer, R10)

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/resquar"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// Operation names.
const (
	OpResHistory = "resource.history"
	OpResReport  = "resource.report"
)

// rHistory is the resources row's capped field-change list (schema
// FieldResourceHistory).
const rHistory = schema.FieldResourceHistory

// resourceHistoryMax caps a row's history: the oldest entries drop off so a
// resource stays small (RQ4); git keeps the full record.
const resourceHistoryMax = 100

// lintQuarantined is the lint check of a value that does not fit its field
// (R10 3.15).
const lintQuarantined = "quarantined"

// resHistTag types history entry ids; entries are not rows, the id is the
// list's merge key (MergeListByID), the same prefix as task history.
type resHistTag struct{}

// Prefix implements ids.Tag.
func (resHistTag) Prefix() ids.Prefix { return ids.PrefixHistoryEntry }

// ResHistoryEntry is one change of one field value. From/To are display
// text, "" for unset; a sensitive or internal field never stores its
// values, only maskedValue (3.8: history is read by everyone with the row).
type ResHistoryEntry struct {
	ID      string `json:"id"`
	At      string `json:"at"`
	By      string `json:"by,omitempty"`
	Batch   string `json:"batch,omitempty"`
	FieldID string `json:"field_id"`
	Field   string `json:"field"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	// Commit is set only on entries read from git (--git, OV62); entries
	// stored on the row never carry it.
	Commit string `json:"commit,omitempty"`
}

// actorer is the optional Env upgrade that names the caller (wire's actor).
type actorer interface {
	Actor(ctx context.Context) (ids.ID[ids.Actor], error)
}

// historyOf decodes a resource row's history, oldest first; a bad value
// reads as none (a read never fails on it).
func historyOf(r located) []ResHistoryEntry {
	b, ok := value.JSONForm(r.Row.Fields[rHistory])
	if !ok {
		return nil
	}
	var out []ResHistoryEntry
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	slices.SortStableFunc(out, func(a, b ResHistoryEntry) int { return cmp.Or(cmp.Compare(a.At, b.At), cmp.Compare(a.ID, b.ID)) })
	return out
}

// withHistory adds patch's field changes against old (nil on add) to
// patch["history"]. A patch without fields is left alone.
func (o ops) withHistory(ctx context.Context, d kindDef, old *located, patch map[string]value.Value) error {
	nv, ok := patch[rFields]
	if !ok {
		return nil
	}
	prev := resValues{vals: map[string]json.RawMessage{}}
	var list []ResHistoryEntry
	if old != nil {
		var err error
		if prev, err = decodeValues(*old); err != nil {
			return err
		}
		list = historyOf(*old)
	}
	next, err := decodeValues(located{Row: rowWith(rFields, nv)})
	if err != nil {
		return err
	}
	by := ""
	if a, isA := upgrade.As[actorer](o.env); isA {
		if id, aerr := a.Actor(ctx); aerr == nil && !id.IsZero() {
			by = id.String()
		}
	}
	batch := ""
	if b, isB := engine.BatchOf(ctx); isB {
		batch = b.ID.String()
	}
	at := opclock.Now(o.env).UTC().Format(time.RFC3339)
	changes := fieldChanges(d, prev, next, false)
	if len(changes) == 0 {
		return nil
	}
	for _, c := range changes {
		id, ierr := ids.New[resHistTag]()
		if ierr != nil {
			return ierr
		}
		c.ID, c.At, c.By, c.Batch = id.String(), at, by, batch
		list = append(list, c)
	}
	if n := len(list); n > resourceHistoryMax {
		list = list[n-resourceHistoryMax:]
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return errs.Wrap(errs.Internal, "resource history: encode", err)
	}
	if patch[rHistory], err = value.JSONOf(raw); err != nil {
		return errs.Wrap(errs.Internal, "resource history", err)
	}
	return nil
}

// fieldChanges is one entry (FieldID, Field, From, To) per field whose
// value differs between prev and next, in field-id order. 3.8 / OV49-H2: a
// sensitive, internal or secret_ref field's values never enter history
// (masked by the field's CURRENT definition); unknownHidden masks a field
// the kind no longer defines too (old git versions: its flags are unknown,
// so fail closed, OV62).
func fieldChanges(d kindDef, prev, next resValues, unknownHidden bool) []ResHistoryEntry {
	fids := make([]string, 0, len(prev.vals)+len(next.vals))
	for id := range prev.vals {
		fids = append(fids, id)
	}
	for id := range next.vals {
		if _, seen := prev.vals[id]; !seen {
			fids = append(fids, id)
		}
	}
	slices.Sort(fids)
	var out []ResHistoryEntry
	for _, fid := range fids {
		from, to := histValue(prev.vals, fid), histValue(next.vals, fid)
		if from == to {
			continue
		}
		name := cmp.Or(next.names[fid], prev.names[fid])
		f, known := d.byID[fid]
		if known {
			name = text(f.Row, kName)
		}
		if (known && (flag(f.Row, fSensitive) || flag(f.Row, fInternal) || text(f.Row, fType) == string(value.KindSecretRef))) || (!known && unknownHidden) {
			from, to = maskIf(from), maskIf(to)
		}
		out = append(out, ResHistoryEntry{FieldID: fid, Field: name, From: from, To: to})
	}
	return out
}

// rowWith is a scratch row holding one field (decodeValues reads Fields).
func rowWith(f string, v value.Value) row.Row {
	return row.Row{Table: schema.TableResources, Fields: map[string]value.Value{f: v}}
}

// histValue is one stored value as history text ("" when unset).
func histValue(vals map[string]json.RawMessage, fid string) string {
	raw, ok := vals[fid]
	if !ok {
		return ""
	}
	return display(fromJSONRaw(raw))
}

// maskIf masks a non-empty value.
func maskIf(s string) string {
	if s == "" {
		return ""
	}
	return maskedValue
}

// updateResource is Env.Update of a resource row with its history entry.
func (o ops) updateResource(ctx context.Context, d kindDef, r located, patch map[string]value.Value) error {
	if err := o.withHistory(ctx, d, &r, patch); err != nil {
		return err
	}
	_, err := o.env.Update(ctx, engine.Ref{Store: r.Src, Table: schema.TableResources, ID: r.Row.ID}, patch)
	return err
}

// Quarantined is one stored value that breaks its field's rule (R10 3.15):
// still visible and editable, named with the rule, never the value. The
// rule itself lives in logic/resquar (shared with the engine and cache).
type Quarantined = resquar.Finding

// quarantineOf lists d-fields of stored whose value does not fit the
// field's type or allowed values, and values of fields d does not know.
// Refs are not checked (a dangling ref is not a quarantine reason, 3.4).
func quarantineOf(d kindDef, stored resValues) []Quarantined {
	var out []Quarantined
	for fid, raw := range stored.vals {
		f, known := d.byID[fid]
		if !known {
			out = append(out, Quarantined{Field: cmp.Or(stored.names[fid], fid), Rule: resquar.RuleUnknownField})
			continue
		}
		if q, bad := resquar.Check(resquar.Field{Name: text(f.Row, kName), Type: text(f.Row, fType), Values: texts(f.Row, fValues)}, raw); bad {
			out = append(out, q)
		}
	}
	slices.SortFunc(out, func(a, b Quarantined) int { return cmp.Compare(a.Field, b.Field) })
	return out
}

// quarantineLine is the show/list mark of q ("" when none).
func quarantineLine(q []Quarantined) string {
	if len(q) == 0 {
		return ""
	}
	parts := make([]string, len(q))
	for i, x := range q {
		parts[i] = x.Field + " (" + x.Rule + ")"
	}
	return "quarantined: " + strings.Join(parts, "; ")
}

// ResHistoryIn is the input of resource.history.
type ResHistoryIn struct {
	Name  string `json:"id" arg:"0" desc:"the resource (name or res_ id)"`
	Field string `json:"field,omitempty" flag:"field" desc:"only changes of this field"`
	Git   bool   `json:"git,omitempty" flag:"git" desc:"also read older versions from git history (beyond the row's last 100 entries)"`
}

// ResHistoryOut is the output of resource.history, newest first.
type ResHistoryOut struct {
	ID      string            `json:"id"`
	Count   int               `json:"count"`
	Entries []ResHistoryEntry `json:"entries"`
}

// Text implements op.Texter: the 3.22 lines.
func (o ResHistoryOut) Text() string {
	if len(o.Entries) == 0 {
		return "(no field changes recorded)\n"
	}
	var b strings.Builder
	for _, e := range o.Entries {
		change := e.From + " → " + e.To
		switch {
		case e.From == "":
			change = "set " + e.To
		case e.To == "":
			change = "unset (was " + e.From + ")"
		}
		line := dayOf(e.At) + "  " + e.Field + "  " + change
		if e.By != "" {
			line += "  " + e.By
		}
		if e.Batch != "" {
			line += "  batch " + e.Batch
		}
		if e.Commit != "" {
			line += "  (git " + shortCommit(e.Commit) + ")"
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (o ops) resHistory(ctx context.Context, in ResHistoryIn) (ResHistoryOut, error) {
	r, err := o.resourceByName(ctx, in.Name, "")
	if err != nil {
		return ResHistoryOut{}, err
	}
	out := ResHistoryOut{ID: r.Row.ID.String(), Entries: []ResHistoryEntry{}}
	all := historyOf(r)
	if in.Git {
		git, gerr := o.resGitHistory(ctx, r)
		if gerr != nil {
			return ResHistoryOut{}, gerr
		}
		all = append(all, dedupResGit(all, git)...)
		slices.SortStableFunc(all, func(a, b ResHistoryEntry) int { return cmp.Compare(a.At, b.At) })
	}
	for i := len(all) - 1; i >= 0; i-- {
		if in.Field == "" || all[i].Field == in.Field {
			out.Entries = append(out.Entries, all[i])
		}
	}
	out.Count = len(out.Entries)
	return out, nil
}

// resGitHistory is the field changes between consecutive committed
// versions of r (OV49-H1: git is the full record past the capped list).
//
//	RowHistorian (wire, E_UNSUPPORTED without it) ─► versions oldest first
//	─► kindDef of the CURRENT kind (masking by today's flags, OV49-H2)
//	─► fieldChanges(prev, next, unknown fields masked) per version pair
func (o ops) resGitHistory(ctx context.Context, r located) ([]ResHistoryEntry, error) {
	h, ok := upgrade.As[task.RowHistorian](o.env)
	if !ok {
		return nil, errs.New(errs.Unsupported, "resource history --git: this surface cannot read git history of a row").
			WithHint("run it from the lore CLI inside the repo")
	}
	vers, err := h.RowVersions(ctx, engine.Ref{Store: r.Src, Table: schema.TableResources, ID: r.Row.ID})
	if err != nil {
		return nil, err
	}
	d, err := o.kindDef(ctx, text(r.Row, rKind))
	if err != nil {
		return nil, err
	}
	var out []ResHistoryEntry
	for i := 1; i < len(vers); i++ {
		prev, perr := decodeValues(located{Row: vers[i-1].Row})
		next, nerr := decodeValues(located{Row: vers[i].Row})
		if perr != nil || nerr != nil {
			continue // a version whose values do not decode: never guessed
		}
		for _, c := range fieldChanges(d, prev, next, true) {
			c.At, c.By, c.Commit = vers[i].At.UTC().Format(time.RFC3339), vers[i].By, vers[i].Commit
			out = append(out, c)
		}
	}
	return out, nil
}

// dedupResGit drops git entries the row's list already holds (same field,
// from and to; counted), so --git adds only what the cap dropped.
func dedupResGit(have, git []ResHistoryEntry) []ResHistoryEntry {
	type change struct{ fid, from, to string }
	seen := map[change]int{}
	for _, e := range have {
		seen[change{e.FieldID, e.From, e.To}]++
	}
	var out []ResHistoryEntry
	for _, e := range git {
		k := change{e.FieldID, e.From, e.To}
		if seen[k] > 0 {
			seen[k]--
			continue
		}
		out = append(out, e)
	}
	return out
}

// shortCommit is a commit id's first 7 characters.
func shortCommit(c string) string {
	const n = 7
	if len(c) > n {
		return c[:n]
	}
	return c
}

// ResReportIn is the input of resource.report.
type ResReportIn struct {
	Kind string `json:"kind,omitempty" flag:"kind" desc:"only this kind (name or knd_ id)"`
	Days int    `json:"days,omitempty" flag:"days" desc:"stale when last_checked is older than this many days (default 60)"`
}

// KindCount is one kind's live resource count.
type KindCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// ResReportOut is the output of resource.report.
type ResReportOut struct {
	Total     int           `json:"total"`
	Kinds     []KindCount   `json:"kinds"`
	Stale     []string      `json:"stale"`
	Unchecked []string      `json:"unchecked"`
	Findings  []LintFinding `json:"findings"`
	Links     []GraphLink   `json:"open_links"`
	// Rotate are the secret_ref values (references, never secrets) as a
	// rotation checklist (3.22).
	Rotate []string `json:"rotate"`
}

// Text implements op.Texter.
func (o ResReportOut) Text() string {
	var b strings.Builder
	parts := make([]string, len(o.Kinds))
	for i, k := range o.Kinds {
		parts[i] = k.Kind + " " + strconv.Itoa(k.Count)
	}
	fmt.Fprintf(&b, "resources   %d (%s)\n", o.Total, strings.Join(parts, ", "))
	fmt.Fprintf(&b, "stale       %d%s\n", len(o.Stale), listTail(o.Stale))
	fmt.Fprintf(&b, "unchecked   %d%s\n", len(o.Unchecked), listTail(o.Unchecked))
	byCheck := map[string]int{}
	for _, f := range o.Findings {
		byCheck[f.Check]++
	}
	fmt.Fprintf(&b, "dangling    %d\n", byCheck[lintDangling]+byCheck[lintBareName])
	fmt.Fprintf(&b, "quarantined %d\n", byCheck[lintQuarantined])
	if len(o.Findings) > 0 {
		b.WriteString("  details: lore resource lint\n")
	}
	links := make([]string, len(o.Links))
	for i, l := range o.Links {
		links[i] = strings.TrimSuffix(l.Table, "s") + " " + l.Row
	}
	fmt.Fprintf(&b, "open links  %d%s\n", len(links), listTail(links))
	fmt.Fprintf(&b, "rotate      %d%s\n", len(o.Rotate), listTail(o.Rotate))
	return b.String()
}

// listTail is ": a, b" for a non-empty list.
func listTail(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return ": " + strings.Join(xs, ", ")
}

// resReport: live resources of the view (one kind with --kind) ─► counts,
// stale/unchecked (resource stale's rule), lint findings of those rows,
// open links of those rows, secret_ref references (RES 3.20/3.22, OV49).
func (o ops) resReport(ctx context.Context, in ResReportIn) (ResReportOut, error) {
	rows, err := liveRows(ctx, o.env, o.resStores(), schema.TableResources)
	if err != nil {
		return ResReportOut{}, err
	}
	kindID := ""
	if in.Kind != "" {
		d, kerr := o.kindDef(ctx, in.Kind)
		if kerr != nil {
			return ResReportOut{}, kerr
		}
		kindID = d.kind.Row.ID.String()
	}
	days := in.Days
	if days <= 0 {
		days = defaultStaleDays
	}
	cutoff := opclock.Now(o.env).Add(-time.Duration(days) * hoursPerDay * time.Hour)
	out := ResReportOut{Kinds: []KindCount{}, Stale: []string{}, Unchecked: []string{}, Findings: []LintFinding{}, Links: []GraphLink{}, Rotate: []string{}}
	counts := map[string]int{}
	defs := map[string]kindDef{}
	var nodes []string
	for _, r := range rows {
		kid := text(r.Row, rKind)
		if text(r.Row, rArchivedAt) != "" || (kindID != "" && kid != kindID) {
			continue
		}
		d, seen := defs[kid]
		if !seen {
			var kerr error
			if d, kerr = o.kindDef(ctx, kid); kerr != nil {
				continue // a kind this view cannot see
			}
			defs[kid] = d
		}
		out.Total++
		counts[d.name()]++
		id, label := r.Row.ID.String(), d.name()+" "+text(r.Row, rName)
		nodes = append(nodes, id)
		if lc := text(r.Row, rLastChecked); lc == "" {
			out.Unchecked = append(out.Unchecked, label)
		} else if t, ok := parseWhen(lc); ok && t.Before(cutoff) {
			out.Stale = append(out.Stale, label)
		}
		stored, derr := decodeValues(r)
		if derr != nil {
			continue
		}
		for fid, raw := range stored.vals {
			if f, ok := d.byID[fid]; ok && text(f.Row, fType) == string(value.KindSecretRef) {
				if s := display(fromJSONRaw(raw)); s != "" && !slices.Contains(out.Rotate, s) {
					out.Rotate = append(out.Rotate, s)
				}
			}
		}
	}
	for k, n := range counts {
		out.Kinds = append(out.Kinds, KindCount{Kind: k, Count: n})
	}
	slices.SortFunc(out.Kinds, func(a, b KindCount) int { return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Kind, b.Kind)) })
	slices.Sort(out.Stale)
	slices.Sort(out.Unchecked)
	slices.Sort(out.Rotate)
	lint, err := o.resLint(ctx, ResLintIn{})
	if err != nil {
		return ResReportOut{}, err
	}
	for _, f := range lint.Findings {
		if slices.Contains(nodes, f.Row) {
			out.Findings = append(out.Findings, f)
		}
	}
	if len(nodes) > 0 {
		if out.Links, err = o.openLinks(ctx, nodes, false); err != nil {
			return ResReportOut{}, err
		}
		if out.Links == nil {
			out.Links = []GraphLink{}
		}
	}
	return out, nil
}

func (o ops) historyOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpResHistory, Summary: "Field changes of one resource, newest first, with actor, date and batch (sensitive values masked)", Permission: op.PermRead, Access: op.AccessRead,
			Errors:   append(slices.Clone(readErrors), errs.Unsupported),
			Examples: []op.Example{{Title: "one field's history", CLI: "lore resource history hub_prod --field os", Want: "os"}}}, o.resHistory),
		op.Define(op.Spec{Name: OpResReport, Summary: "Report on the resources in view: counts per kind, stale, unchecked, dangling, quarantined, open links, secret references to rotate", Permission: op.PermRead, Access: op.AccessRead,
			Errors: append(slices.Clone(readErrors), errs.KindUnknown)}, o.resReport),
	}
}
