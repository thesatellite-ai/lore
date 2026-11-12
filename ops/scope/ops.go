package scope

import (
	"context"
	"fmt"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/logic/dirscope"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"

	"github.com/thesatellite-ai/lore/engine"
)

// Operation names (`lore scope show|list|move`, 6.2.3).
const (
	Noun   = "scope"
	OpShow = "scope.show"
	OpList = "scope.list"
	OpMove = "scope.move"
)

// DirFile is the generated per-folder file (6.2.5), relative to the folder.
const DirFile = "LORE.md"

// Pinned severities: only these reach a folder file (6.3 tiers 1-2).
const (
	sevMust     = "must"
	sevCritical = "critical"
	sevHigh     = "high"
)

// Row fields read here.
const (
	fieldProject    = "project_id"
	fieldSeverity   = "severity"
	fieldArchived   = "archived_at"
	fieldActivation = "activation"
	fieldGlobs      = "globs"
	fieldSuperseded = "superseded_by_id"
)

var readErrors = []errs.Code{errs.InvalidInput, errs.NotFound, errs.Unsupported, errs.StoreUnreachable, errs.Internal}

var writeErrors = []errs.Code{
	errs.InvalidInput, errs.NotFound, errs.Unsupported, errs.PolicyDenied, errs.Stale,
	errs.StoreReadOnly, errs.StoreLocked, errs.StoreUnreachable, errs.OldLore, errs.Internal,
}

// Ops returns the scope operations bound to env.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show the scope chain for a path: which rules apply and which file serves each level",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors,
			Examples: []op.Example{{Title: "chain for a file", CLI: "lore scope show src/booking/refund.go", Want: "scope chain"}},
		}, o.show),
		op.Define(op.Spec{
			Name: OpList, Summary: "List every folder scope of this repo with its entries and file",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors,
			Examples: []op.Example{{Title: "every folder scope as JSON", CLI: "lore scope list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpMove, Summary: "Relabel a folder scope after a folder rename (ids unchanged, no row rewritten)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
			Examples: []op.Example{{Title: "after git mv src/booking src/bookings", CLI: "lore scope move src/booking src/bookings", Want: "src/bookings"}},
		}, o.move),
	}
}

type ops struct{ env Env }

// ShowIn is the input of scope.show.
type ShowIn struct {
	Path string `json:"path" arg:"0" desc:"a file or folder, relative to the repo root"`
}

// Level is one line of a scope chain.
type Level struct {
	Scope    string `json:"scope"`
	Name     string `json:"name"`
	Rules    int    `json:"rules"`
	Hotfixes int    `json:"hotfixes"`
	File     string `json:"file"`
	ID       string `json:"id,omitempty"`
}

// ShowOut is the chain, general first.
type ShowOut struct {
	Path   string  `json:"path"`
	Levels []Level `json:"levels"`
	Globs  []Level `json:"globs,omitempty"`
}

// Text prints the 6.2.3 chain.
func (o ShowOut) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "scope chain for %s (general first; later wins on conflict)\n", o.Path)
	for _, l := range append(slices.Clone(o.Levels), o.Globs...) {
		fmt.Fprintf(&b, "  %-8s %-28s %s  %s\n", l.Scope, l.Name, counts(l.Rules, l.Hotfixes), l.File)
	}
	if len(o.Globs) == 0 {
		b.WriteString("  glob     (none match this path)\n")
	}
	return b.String()
}

// counts is "3 rules, 1 hotfix".
func counts(rules, hotfixes int) string {
	s := plural(rules, "rule")
	if hotfixes > 0 {
		s += ", " + plural(hotfixes, "hotfix")
	}
	return s
}

func plural(n int, w string) string {
	if n == 1 {
		return "1 " + w
	}
	if strings.HasSuffix(w, "x") {
		return fmt.Sprintf("%d %ses", n, w)
	}
	return fmt.Sprintf("%d %ss", n, w)
}

// pinned is one live rule or hotfix of the repo store with its place.
type pinned struct {
	table row.Table
	r     row.Row
	place dirscope.Place
}

// loadPinned reads the live, not archived must rules and critical/high
// hotfixes of the repo store with their places; rules carries every rule
// (any severity) for the glob level.
func (o ops) loadPinned(ctx context.Context, paths map[string]string) ([]pinned, error) {
	var out []pinned
	for _, t := range []row.Table{schema.TableRules, schema.TableHotfixes} {
		rows, err := o.env.List(ctx, store.SourceRepo, t)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.Deleted || text(r, fieldArchived) != "" || text(r, fieldSuperseded) != "" {
				continue
			}
			sev := text(r, fieldSeverity)
			if (t == schema.TableRules && sev != sevMust) || (t == schema.TableHotfixes && sev != sevCritical && sev != sevHigh) {
				continue
			}
			p := dirscope.Place{Repo: text(r, fieldRepo)}
			if sid := text(r, schema.FieldScope); sid != "" {
				p.Path = paths[sid]
				if p.Path == "" {
					p.Path = sid
				}
			}
			out = append(out, pinned{table: t, r: r, place: p})
		}
	}
	return out, nil
}

// scopePaths maps the repo's scp_ ids to their paths.
func scopePaths(rows []row.Row) map[string]string {
	m := map[string]string{}
	for _, r := range rows {
		m[r.ID.String()] = text(r, fieldPath)
	}
	return m
}

// show builds the chain for a path (6.2.2): project, repo, the folder
// scopes that cover it outermost first, then glob rules that match it.
// Rows of other stores render privately (CFG6) and are not counted here.
func (o ops) show(ctx context.Context, in ShowIn) (ShowOut, error) {
	p, err := dirscope.Clean(in.Path)
	if err != nil {
		return ShowOut{}, err
	}
	rid, err := ownRepo(ctx, o.env)
	if err != nil {
		return ShowOut{}, err
	}
	srows, err := Rows(ctx, o.env, rid)
	if err != nil {
		return ShowOut{}, err
	}
	paths := scopePaths(srows)
	all, err := o.loadPinned(ctx, paths)
	if err != nil {
		return ShowOut{}, err
	}
	out := ShowOut{Path: p}
	prj := Level{Scope: string(dirscope.KindProject), Name: "this project", File: ".lore/LORE.md"}
	repo := Level{Scope: string(dirscope.KindRepo), Name: "this repo", File: ".lore/LORE.md"}
	dirs := map[string]*Level{}
	var dirNames []string
	for id, d := range paths {
		dirNames = append(dirNames, d)
		dirs[d] = &Level{Scope: string(dirscope.KindDir), Name: d, ID: id, File: o.fileOf(ctx, d)}
	}
	for _, x := range all {
		var l *Level
		switch {
		case x.place.Repo == "":
			l = &prj
		case x.place.Repo != rid:
			continue
		case x.place.Path == "":
			l = &repo
		default:
			l = dirs[x.place.Path]
		}
		if l == nil {
			continue
		}
		if x.table == schema.TableRules {
			l.Rules++
		} else {
			l.Hotfixes++
		}
	}
	out.Levels = append(out.Levels, prj, repo)
	for _, d := range dirscope.Chain(dirNames, p) {
		out.Levels = append(out.Levels, *dirs[d])
	}
	globs, err := o.globLevels(ctx, rid, p)
	if err != nil {
		return ShowOut{}, err
	}
	out.Globs = globs
	return out, nil
}

// globLevels lists the live glob rules (any severity) whose patterns match p.
func (o ops) globLevels(ctx context.Context, rid, p string) ([]Level, error) {
	rows, err := o.env.List(ctx, store.SourceRepo, schema.TableRules)
	if err != nil {
		return nil, err
	}
	var out []Level
	for _, r := range rows {
		if r.Deleted || text(r, fieldArchived) != "" || text(r, fieldActivation) != schema.RuleActivationGlob {
			continue
		}
		if repo := text(r, fieldRepo); repo != "" && repo != rid {
			continue
		}
		for _, g := range contextfit.SplitGlobs(text(r, fieldGlobs)) {
			if contextfit.MatchGlob(g, p) {
				out = append(out, Level{Scope: "glob", Name: g, Rules: 1, ID: r.ID.String(), File: "lore context --for-file"})
				break
			}
		}
	}
	return out, nil
}

// fileOf is the folder file of d when it exists on disk, else "(no file yet: lore render)".
func (o ops) fileOf(ctx context.Context, d string) string {
	f := d + "/" + DirFile
	if top, ok := topOf(ctx, o.env); ok {
		if _, err := os.Stat(filepath.Join(top, filepath.FromSlash(f))); err != nil {
			return "(no file: lore render writes it when the folder has enough entries)"
		}
	}
	return f
}

// ListIn is the input of scope.list (no flags).
type ListIn struct {
	// --created-by / --updated-by: the shared writer filters of every
	// noun list (INTEG-AUDIT-1; engine.List applies them).
	metalist.Audit
}

// ListRow is one folder scope.
type ListRow struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Rules    int    `json:"rules"`
	Hotfixes int    `json:"hotfixes"`
	Notes    int    `json:"notes"`
	File     string `json:"file"`
	Bytes    int    `json:"bytes"`
	// Missing is set when the folder no longer exists (8.9: run lore scope move).
	Missing bool `json:"missing,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// ListOut is every folder scope, by path.
type ListOut struct {
	Data []ListRow `json:"data"`
}

// Text prints the 6.2.3 table.
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no folder scopes: lore rule add … --scope dir:<path>)\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-32s %5s %8s %5s  %-36s %6s\n", "scope", "rules", "hotfixes", "notes", "file", "bytes")
	for _, r := range o.Data {
		file := r.File
		if r.Missing {
			file = "(folder missing: lore scope move)"
		}
		fmt.Fprintf(&b, "%-32s %5d %8d %5d  %-36s %6d\n", "dir:"+r.Path, r.Rules, r.Hotfixes, r.Notes, file, r.Bytes)
	}
	return b.String()
}

// list counts every folder scope's pinned rows and notes (memories and
// decisions, CQ12: ranking only) and the folder file's size.
func (o ops) list(ctx context.Context, _ ListIn) (ListOut, error) {
	rid, err := ownRepo(ctx, o.env)
	if err != nil {
		return ListOut{}, err
	}
	srows, err := Rows(ctx, o.env, rid)
	if err != nil {
		return ListOut{}, err
	}
	paths := scopePaths(srows)
	all, err := o.loadPinned(ctx, paths)
	if err != nil {
		return ListOut{}, err
	}
	byID := map[string]*ListRow{}
	out := ListOut{Data: []ListRow{}}
	top, haveTop := topOf(ctx, o.env)
	for _, r := range srows {
		lr := ListRow{ID: r.ID.String(), Path: text(r, fieldPath), CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy}
		lr.File = lr.Path + "/" + DirFile
		if haveTop {
			if fi, err := os.Stat(filepath.Join(top, filepath.FromSlash(lr.Path))); err != nil || !fi.IsDir() {
				lr.Missing = true
			}
			if fi, err := os.Stat(filepath.Join(top, filepath.FromSlash(lr.File))); err == nil {
				lr.Bytes = int(fi.Size())
			} else {
				lr.File = "(none)"
			}
		}
		out.Data = append(out.Data, lr)
	}
	for i := range out.Data {
		byID[out.Data[i].ID] = &out.Data[i]
	}
	for _, x := range all {
		lr := byID[text(x.r, schema.FieldScope)]
		if lr == nil {
			continue
		}
		if x.table == schema.TableRules {
			lr.Rules++
		} else {
			lr.Hotfixes++
		}
	}
	for _, t := range []row.Table{schema.TableMemories, schema.TableDecisions} {
		rows, err := o.env.List(ctx, store.SourceRepo, t)
		if err != nil {
			return ListOut{}, err
		}
		for _, r := range rows {
			if lr := byID[text(r, schema.FieldScope)]; lr != nil && !r.Deleted {
				lr.Notes++
			}
		}
	}
	slices.SortFunc(out.Data, func(a, b ListRow) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// MoveIn is the input of scope.move.
type MoveIn struct {
	Old string `json:"old" arg:"0" desc:"the folder's old path, relative to the repo root"`
	New string `json:"new" arg:"1" desc:"its new path"`
}

// Moved is one relabelled scope.
type Moved struct {
	ID  string `json:"id"`
	Old string `json:"old"`
	New string `json:"new"`
}

// MoveOut lists the relabelled scopes.
type MoveOut struct {
	Data     []Moved  `json:"data"`
	Warnings []string `json:"warnings,omitempty"`
}

// Text prints one line per scope, then the next step.
func (o MoveOut) Text() string {
	var b strings.Builder
	for _, m := range o.Data {
		fmt.Fprintf(&b, "✓ %s path %s -> %s (ids unchanged)\n", m.ID, m.Old, m.New)
	}
	for _, w := range o.Warnings {
		b.WriteString("WARN: " + w + "\n")
	}
	b.WriteString("next: lore render\n")
	return b.String()
}

// Move is scope.move for a caller outside the registry: lore fix relabels
// a folder scope git saw renamed (B3-5) with exactly what `lore scope
// move` does, so the two can never drift.
func Move(ctx context.Context, env Env, in MoveIn) (MoveOut, error) {
	return ops{env: env}.move(ctx, in)
}

// move relabels the scope at old and every scope under it (6.2.3, D12):
// only the scopes rows change, so no knowledge row is rewritten. A scope
// already at a target path refuses the whole move before anything is
// written (merging two scopes is not a relabel).
func (o ops) move(ctx context.Context, in MoveIn) (MoveOut, error) {
	oldDir, err := dirscope.Clean(in.Old)
	if err != nil {
		return MoveOut{}, err
	}
	newDir, err := dirscope.Clean(in.New)
	if err != nil {
		return MoveOut{}, err
	}
	if dirscope.Under(newDir, oldDir) {
		return MoveOut{}, errs.New(errs.InvalidInput, "scope move: "+newDir+" is inside "+oldDir)
	}
	rid, err := ownRepo(ctx, o.env)
	if err != nil {
		return MoveOut{}, err
	}
	srows, err := Rows(ctx, o.env, rid)
	if err != nil {
		return MoveOut{}, err
	}
	taken := map[string]bool{}
	var plan []Moved
	var refs []row.Row
	for _, r := range srows {
		p := text(r, fieldPath)
		if np, ok := dirscope.Moved(p, oldDir, newDir); ok {
			plan = append(plan, Moved{ID: r.ID.String(), Old: p, New: np})
			refs = append(refs, r)
		} else {
			taken[p] = true
		}
	}
	if len(plan) == 0 {
		return MoveOut{}, errs.New(errs.NotFound, "scope move: no folder scope at "+oldDir).WithHint("run `lore scope list`")
	}
	for _, m := range plan {
		if taken[m.New] {
			return MoveOut{}, errs.New(errs.InvalidInput, "scope move: a scope already exists at "+m.New).
				WithHint("move its rows with each row's edit command (`lore <noun> edit <id> --scope dir:" + m.New + "`) instead")
		}
	}
	out := MoveOut{Data: plan}
	if w, err := checkDir(ctx, o.env, newDir); err != nil {
		return MoveOut{}, err
	} else if w != "" {
		out.Warnings = append(out.Warnings, w)
	}
	for i, m := range plan {
		p, err := value.TextOf(m.New)
		if err != nil {
			return MoveOut{}, errs.Wrap(errs.InvalidInput, "scope move: path", err)
		}
		if _, err := o.env.Update(ctx, engine.Ref{Store: store.SourceRepo, Table: schema.TableScopes, ID: refs[i].ID}, map[string]value.Value{fieldPath: p}); err != nil {
			return MoveOut{}, err
		}
	}
	return out, nil
}
