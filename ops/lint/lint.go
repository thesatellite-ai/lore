package lint

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (`lore lint run`, mounted as `lore lint`).
const (
	Noun  = "lint"
	OpRun = "lint.run"
)

// Code names one kind of finding (a closed set: Codes).
type Code string

// The finding codes.
const (
	UnknownScheme       Code = "unknown_scheme"
	DanglingRef         Code = "dangling_ref"
	VarOldName          Code = "var_old_name"
	VarShadowed         Code = "var_shadowed"
	VarShareableDiffers Code = "var_shareable_differs"
	ParentCycle         Code = "parent_cycle"
	LongSpan            Code = "long_span"
	TimeEntriesMany     Code = "time_entries_many"
	DuplicateImport     Code = "duplicate_import"
	MetaUnknownKey      Code = "meta_unknown_key"
)

// Codes lists every finding code.
func Codes() []Code {
	return []Code{UnknownScheme, DanglingRef, VarOldName, VarShadowed, VarShareableDiffers,
		ParentCycle, LongSpan, TimeEntriesMany, DuplicateImport, MetaUnknownKey, SecretFound}
}

// ManyTimeEntries is where lint warns that a task should be split (TQ4,
// TIME 11.7: every entry is kept; folding is decided later from bench
// numbers).
const ManyTimeEntries = 300

// Fields read here (schema.Builtin).
const (
	fVarName      = "name"
	fVarValue     = "value"
	fVarShareable = "shareable"
	fVarAliases   = "aliases"
	// rowRefSep ends the id part of a ${row:<id>.<field>} body.
	rowRefSep = "."
)

// Env is what lint reads; *wire.App implements it.
type Env = work.Env

// RunIn is the input of lint.run.
type RunIn struct {
	Code []string `json:"code,omitempty" flag:"code" desc:"only these finding codes (repeatable)"`
	// Context runs only the LORE.md clean-up checks (CONTEXT_BUDGET 6.12).
	Context bool `json:"context,omitempty" flag:"context" desc:"only the context checks: superseded but pinned, no summary, too long, code path without globs, untagged, near duplicates"`
}

// Finding is one problem lint found.
type Finding struct {
	Code    Code   `json:"code"`
	ID      string `json:"id,omitempty"`
	Store   string `json:"store,omitempty"`
	Message string `json:"message"`
	// Fix is the command that fixes it ("" when a person must decide).
	Fix string `json:"fix,omitempty"`
}

// RunOut is the output of lint.run.
type RunOut struct {
	Findings []Finding `json:"findings"`
	Count    int       `json:"count"`
}

// Text is the human form.
func (o RunOut) Text() string {
	if o.Count == 0 {
		return "lint: no findings\n"
	}
	var b strings.Builder
	for _, f := range o.Findings {
		b.WriteString(string(f.Code))
		if f.ID != "" {
			b.WriteString(" " + f.ID)
		}
		if f.Store != "" {
			b.WriteString(" (" + f.Store + ")")
		}
		b.WriteString(": " + f.Message + "\n")
		if f.Fix != "" {
			b.WriteString("  fix: " + f.Fix + "\n")
		}
	}
	b.WriteString(strconv.Itoa(o.Count) + " finding(s)\n")
	return b.String()
}

// Ops returns the lint operations bound to env.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{Name: OpRun, Summary: "Report problems in the attached stores (dangling references, shadowed variables, task cycles, long time spans, duplicate imports, unknown metadata keys, stored secrets)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: []errs.Code{errs.InvalidInput, errs.Internal},
			Examples: []op.Example{{Title: "every finding as JSON", CLI: "lore lint --json", Want: `"findings"`},
				{Title: "LORE.md clean-up suggestions", CLI: "lore lint --context", Want: "finding"}}}, o.run),
	}
}

type ops struct{ env Env }

// sourced is one live row and its store.
type sourced struct {
	src store.Source
	r   row.Row
}

// run is lint.run: read every store once, then run each check.
func (o ops) run(ctx context.Context, in RunIn) (RunOut, error) {
	want := map[Code]bool{}
	for _, c := range in.Code {
		if !slices.Contains(Codes(), Code(c)) && !slices.Contains(ContextCodes(), Code(c)) {
			return RunOut{}, errs.New(errs.InvalidInput, "lint: unknown code "+strconv.Quote(c))
		}
		want[Code(c)] = true
	}
	all, err := o.rows(ctx)
	if err != nil {
		return RunOut{}, err
	}
	if in.Context {
		// 6.12: the context checks alone, read-only, each with its command.
		out := append(contextFindings(all), o.contextScopeFindings(ctx, all)...)
		if len(want) > 0 {
			out = slices.DeleteFunc(out, func(f Finding) bool { return !want[f.Code] })
		}
		if out == nil {
			out = []Finding{}
		}
		return RunOut{Findings: out, Count: len(out)}, nil
	}
	var out []Finding
	out = append(out, refFindings(all)...)
	out = append(out, varFindings(all[schema.TableVars])...)
	out = append(out, taskFindings(all[schema.TableTasks])...)
	out = append(out, metaFindings(all)...)
	secrets, err := o.secretScan(ctx, all) // SQ12: re-scan with today's team rules
	if err != nil {
		return RunOut{}, err
	}
	out = append(out, secrets...)
	dups, err := opstask.FindDuplicateImports(ctx, o.env)
	if err != nil {
		return RunOut{}, err
	}
	for _, d := range dups {
		// PROV9: reported with the command, never merged on its own.
		out = append(out, Finding{Code: DuplicateImport, ID: d.IDs[0], Message: d.Source + " " + d.ExtID + " was imported more than once: " + strings.Join(d.IDs, ", "),
			Fix: "keep the oldest: " + strings.Join(d.FixCommand(), "; ")})
	}
	if len(want) > 0 {
		out = slices.DeleteFunc(out, func(f Finding) bool { return !want[f.Code] })
	}
	if out == nil {
		out = []Finding{}
	}
	return RunOut{Findings: out, Count: len(out)}, nil
}

// rows lists every live row of every built-in table in every store.
func (o ops) rows(ctx context.Context) (map[row.Table][]sourced, error) {
	out := map[row.Table][]sourced{}
	for _, def := range schema.Builtin() {
		for _, src := range o.env.Stores() {
			rs, err := o.env.List(ctx, src, def.Name)
			if err != nil {
				return nil, err
			}
			for _, r := range rs {
				if !r.Deleted {
					out[def.Name] = append(out[def.Name], sourced{src: src, r: r})
				}
			}
		}
	}
	return out, nil
}

// code spans and fenced blocks: unknown schemes there are code, not
// references (SEC 6: no noise from shell snippets).
var (
	fenced     = regexp.MustCompile("(?s)```.*?```")
	inlineCode = regexp.MustCompile("`[^`\n]*`")
)

// stripCode removes fenced blocks and inline code spans.
func stripCode(s string) string {
	return inlineCode.ReplaceAllString(fenced.ReplaceAllString(s, ""), "")
}

// refFindings checks every ${…} reference in every text field.
func refFindings(all map[row.Table][]sourced) []Finding {
	live := map[string]bool{}
	for _, rs := range all {
		for _, s := range rs {
			live[s.r.ID.String()] = true
		}
	}
	vars, aliases := map[string]bool{}, map[string]string{}
	for _, s := range all[schema.TableVars] {
		name := text(s.r, fVarName)
		vars[name] = true
		for _, a := range value.ItemStrings(s.r.Fields[fVarAliases]) {
			aliases[a] = name
		}
	}
	var out []Finding
	for _, t := range slices.Sorted(maps.Keys(all)) {
		for _, s := range all[t] {
			for _, f := range slices.Sorted(maps.Keys(s.r.Fields)) {
				v, ok := s.r.Fields[f].(value.Text)
				if !ok {
					continue
				}
				out = append(out, checkText(s, f, v.String(), live, vars, aliases)...)
			}
		}
	}
	return out
}

// checkText is refFindings for one field.
func checkText(s sourced, field, txt string, live, vars map[string]bool, aliases map[string]string) []Finding {
	var out []Finding
	id := s.r.ID.String()
	at := func(c Code, msg, fix string) {
		out = append(out, Finding{Code: c, ID: id, Store: string(s.src), Message: field + ": " + msg, Fix: fix})
	}
	for _, r := range refs.References(stripCode(txt)) {
		if !r.Scheme.Known() {
			at(UnknownScheme, r.String()+" names no known scheme; it stays literal text", "write $${…} to keep it literal, or fix the scheme")
		}
	}
	for _, r := range refs.References(txt) {
		switch r.Scheme {
		case refs.SchemeRow:
			target, _, _ := strings.Cut(r.Body, rowRefSep)
			if _, err := ids.ParseAny(target); err == nil && !live[target] {
				at(DanglingRef, r.String()+" points at no live row", "lore edit the row, or restore "+target+" (lore trash restore "+target+")")
			}
		case refs.SchemeVar:
			name, _, _ := strings.Cut(r.Body, ":-")
			switch {
			case vars[name]:
			case aliases[name] != "":
				at(VarOldName, r.String()+" uses an old name of ${var:"+aliases[name]+"}", "replace it with ${var:"+aliases[name]+"}")
			default:
				at(DanglingRef, r.String()+" names no variable", "lore vars set "+name+" <value>")
			}
		case refs.SchemeEnv, refs.SchemeMeta, refs.SchemeRes, refs.SchemeSecret:
			// env is the reader's machine; meta, res and secret targets
			// are the resolver's (ov8) and the secrets index's to check.
		}
	}
	return out
}

// varFindings: one name in several stores. The first store in precedence
// wins (SQ22); a nearer level shadowing a farther one is normal, so only
// a winner outside the repo is reported (DECISION OV17-1). Shareable
// variables of one name with different values are SQ20's finding.
func varFindings(vs []sourced) []Finding {
	byName := map[string][]sourced{}
	var names []string
	for _, s := range vs {
		n := text(s.r, fVarName)
		if _, seen := byName[n]; !seen {
			names = append(names, n)
		}
		byName[n] = append(byName[n], s)
	}
	var out []Finding
	for _, n := range names {
		group := byName[n]
		if len(group) < 2 {
			continue
		}
		win := group[0]
		if win.src != store.SourceRepo {
			for _, s := range group[1:] {
				out = append(out, Finding{Code: VarShadowed, ID: s.r.ID.String(), Store: string(s.src),
					Message: "variable " + n + " is shadowed by " + win.r.ID.String() + " in " + string(win.src) + " (.lore/stores.yaml order decides)",
					Fix:     "lore vars show " + n})
			}
		}
		vals := map[string]bool{}
		var shared []string
		for _, s := range group {
			if b, ok := s.r.Fields[fVarShareable].(value.Bool); ok && b.Bool() {
				vals[text(s.r, fVarValue)] = true
				shared = append(shared, s.r.ID.String()+" ("+string(s.src)+")")
			}
		}
		if len(vals) > 1 {
			out = append(out, Finding{Code: VarShareableDiffers, ID: win.r.ID.String(),
				Message: "shareable variable " + n + " has different values: " + strings.Join(shared, ", ")})
		}
	}
	return out
}

// taskFindings: parent cycles (TASK 6.13), long spans (TQ2) and tasks with
// ManyTimeEntries or more entries (TQ4).
func taskFindings(ts []sourced) []Finding {
	nodes := make([]tasktree.Node, 0, len(ts))
	var out []Finding
	for _, s := range ts {
		nodes = append(nodes, tasktree.Node{ID: s.r.ID.String(), Parent: text(s.r, schema.FieldTaskParent)})
		id, src := s.r.ID.String(), string(s.src)
		entries := opstask.TimeEntries(s.r)
		liveN := 0
		for _, e := range entries {
			if e.Deleted() {
				continue
			}
			liveN++
			if e.HasFlag(timeentry.FlagLongSpan) {
				out = append(out, Finding{Code: LongSpan, ID: id, Store: src,
					Message: "time entry " + e.ID.String() + " of " + e.ActorID.String() + " is " + timeentry.FormatSeconds(e.Seconds) + " long (counted in full)",
					Fix:     "lore task time edit " + id + " " + e.ID.String()})
			}
		}
		if liveN >= ManyTimeEntries {
			out = append(out, Finding{Code: TimeEntriesMany, ID: id, Store: src,
				Message: strconv.Itoa(liveN) + " time entries on one task", Fix: "split the task: lore task split " + id})
		}
	}
	for _, c := range tasktree.Build(nodes).Cycles() {
		out = append(out, Finding{Code: ParentCycle, ID: c[0],
			Message: "parent cycle " + strings.Join(c, " → ") + " → " + c[0] + "; " + c[0] + " is shown at top level",
			Fix:     "lore task edit " + c[0] + " --no-parent"})
	}
	return out
}

// metaFindings: a meta entry whose key id no attached registry defines.
func metaFindings(all map[row.Table][]sourced) []Finding {
	known := map[string]bool{}
	for _, s := range all[schema.TableMetaKeys] {
		known[s.r.ID.String()] = true
	}
	var out []Finding
	for _, t := range slices.Sorted(maps.Keys(all)) {
		for _, s := range all[t] {
			for _, k := range slices.SortedFunc(maps.Keys(s.r.Meta), func(a, b ids.ID[ids.MetaKey]) int { return strings.Compare(a.String(), b.String()) }) {
				if !known[k.String()] {
					out = append(out, Finding{Code: MetaUnknownKey, ID: s.r.ID.String(), Store: string(s.src),
						Message: "metadata key " + k.String() + " is defined in no attached store",
						Fix:     "attach the store that defines it, or lore meta unset " + s.r.ID.String() + " <name>"})
				}
			}
		}
	}
	return out
}

// listItems is a list field's items as text (nil when absent).
func listItems(r row.Row, f string) []string {
	l, ok := r.Fields[f].(value.List)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(l.Items()))
	for _, it := range l.Items() {
		out = append(out, it.String())
	}
	return out
}

// text is a text field of r ("" when absent or null).
func text(r row.Row, f string) string {
	if v, ok := r.Fields[f]; ok && v != nil && v.Kind() != value.KindNull {
		return v.String()
	}
	return ""
}
