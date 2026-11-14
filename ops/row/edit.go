package row

// edit.go — the row commands of the plain-files round (D2, D3):
//
//	lore row path <id>      (row.path, mounted as `lore path` / `lore open`)
//	    └► Locator.RowLocate (wire: Engine.Get ─► the backend's RowFile) ─► the absolute file path
//	lore row markdown <id>  (row.markdown)
//	    └► Env.Get + logic/rowmd.Render ─► the Markdown view `lore edit` opens
//	lore row edit <id> --file <view.md>  (row.edit; `lore edit --id <id>` runs the editor around it)
//	    └► logic/rowmd.Parse (file:line refusals) ─► Updater.Update: the one
//	       write path (schema, references, secret scan, policy, new HLC,
//	       updated_by/at, commit and push as any write in that setup)
//
// The row file stays JSON (D2); the view is a way to edit it, never a
// second copy: nothing is written when the view changes nothing.

import (
	"context"
	"os"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/rowmd"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/fromfile"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/resmask"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names of the edit commands.
const (
	OpPath     = "row.path"
	OpMarkdown = "row.markdown"
	OpEdit     = "row.edit"
	// PathVerb, EditVerb: the words the host mounts at the top level
	// (`lore path`, `lore open`, `lore edit`).
	PathVerb  = "path"
	OpenAlias = "open"
	EditVerb  = "edit"
	// markdownLabel names a view given inline (REST "markdown") in a refusal.
	markdownLabel = "markdown"
)

// Location is where a row's file is (wire.App.RowLocate fills it).
type Location struct {
	Table  row.Table
	Store  string
	Folder string
	// Path is the absolute path of the row file ("" when it has none).
	Path    string
	Exists  bool
	Deleted bool
	// LiveRef / LivePath: the newest version is on this git ref at this
	// path, not at Path (a live row of a code repo); NotPulled: the ref is
	// the remote default branch (landed, not pulled yet).
	LiveRef   string
	LivePath  string
	NotPulled bool
}

// Locator is the Env upgrade row.path and the views need.
type Locator interface {
	RowLocate(ctx context.Context, id ids.AnyID) (Location, error)
}

// Updater is the Env upgrade row.edit writes through (wire.App.Update).
type Updater interface {
	Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error)
}

// PathIn is the input of row.path.
type PathIn struct {
	ID string `json:"id" arg:"0" desc:"any row id (tsk_…, mem_…, rul_…)"`
}

// PathOut is the output of row.path (D-j).
type PathOut struct {
	ID     string `json:"id"`
	Table  string `json:"table"`
	Path   string `json:"path"`
	Store  string `json:"store"`
	Folder string `json:"folder"`
	// Deleted: the row is in the trash (its file holds the tombstone).
	Deleted bool `json:"deleted,omitempty"`
}

// Text is the path alone, so `$EDITOR "$(lore path <id>)"` works.
func (o PathOut) Text() string { return o.Path + "\n" }

// MarkdownIn is the input of row.markdown.
type MarkdownIn struct {
	ID string `json:"id" arg:"0" desc:"any row id"`
}

// MarkdownOut is the output of row.markdown.
type MarkdownOut struct {
	ID     string `json:"id"`
	Table  string `json:"table"`
	Store  string `json:"store"`
	Folder string `json:"folder"`
	// Path is the row file ("" for a live row with no file yet).
	Path string `json:"path,omitempty"`
	// Live names the git ref copy the row is read from, when it is live.
	Live string `json:"live,omitempty"`
	// BodyField is the field written as the Markdown body ("" for none).
	BodyField string `json:"body_field,omitempty"`
	Markdown  string `json:"markdown"`
}

// Text is the view itself.
func (o MarkdownOut) Text() string { return o.Markdown }

// EditIn is the input of row.edit.
type EditIn struct {
	ID string `json:"id" arg:"0" desc:"the row the view is of"`
	// File / Markdown: the edited view, from a file or inline (one of them).
	File     string `json:"file,omitempty" flag:"file" desc:"the edited Markdown view to write back (lore row markdown --id <id> prints one; lore edit --id <id> opens it in your editor)"`
	Markdown string `json:"markdown,omitempty" flag:"markdown" desc:"the edited Markdown view itself, instead of --file"`
}

// EditOut is the output of row.edit.
type EditOut struct {
	ID    string `json:"id"`
	Table string `json:"table"`
	Store string `json:"store"`
	// Changed lists the fields written; empty with NoChange.
	Changed  []string `json:"changed"`
	NoChange bool     `json:"no_change"`
	// Path is the row file ("" for a live row with no file yet).
	Path     string   `json:"path,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// Text is one line: what was written where, or that nothing was.
func (o EditOut) Text() string {
	if o.NoChange {
		return "no change: " + o.ID + " is as it was (nothing written)\n"
	}
	var b strings.Builder
	b.WriteString("✓ " + o.ID + " updated in store " + o.Store + ": " + strings.Join(o.Changed, ", ") + "\n")
	if o.Path != "" {
		b.WriteString("  file " + o.Path + "\n")
	}
	for _, w := range o.Warnings {
		b.WriteString("  warning: " + w + "\n")
	}
	return b.String()
}

// editErrors are the codes row.edit can return.
var editErrors = []errs.Code{
	errs.InvalidID, errs.InvalidInput, errs.NotFound, errs.AmbiguousID, errs.InTrash, errs.Purged, errs.Stale,
	errs.Unsupported, errs.ResourceInvalid, errs.EmptyBody, errs.SecretDetected, errs.NeedsConfirm, errs.Cancelled,
	errs.PolicyDenied, errs.LeakBlocked, errs.RefDangling, errs.NameTaken, errs.StoreReadOnly, errs.StoreLocked,
	errs.StoreUnreachable, errs.ReadOnly, errs.OldLore, errs.Internal,
}

// EditOps returns row.path, row.markdown and row.edit bound to env.
func EditOps(env Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{Name: OpPath, Summary: "Print the file path of any row by id (also lore path, lore open)",
			Permission: op.PermRead, Access: op.AccessRead,
			Errors: []errs.Code{errs.InvalidID, errs.NotFound, errs.AmbiguousID, errs.Unsupported, errs.StoreUnreachable, errs.Internal},
			Examples: []op.Example{
				{Title: "the file of a task", CLI: "lore path --id tsk_<id>", Want: ".json"},
				{Title: "open it in your editor", CLI: `$EDITOR "$(lore path --id tsk_<id>)"`},
				{Title: "store, folder and path as JSON", CLI: "lore path --id tsk_<id> --json", Want: `"path"`},
			}}, o.path),
		op.Define(op.Spec{Name: OpMarkdown, Summary: "Print any row as the Markdown view lore edit opens (fields as frontmatter, long text as body)",
			Permission: op.PermRead, Access: op.AccessRead,
			Errors: []errs.Code{errs.InvalidID, errs.NotFound, errs.AmbiguousID, errs.InTrash, errs.Purged, errs.Unsupported, errs.StoreUnreachable, errs.Internal},
			Examples: []op.Example{
				{Title: "a task as Markdown", CLI: "lore row markdown --id tsk_<id>", Want: "---"},
				{Title: "edit it with any tool, then write it back", CLI: "lore row markdown --id tsk_<id> > t.md && lore row edit --id tsk_<id> --file t.md"},
			}}, o.markdown),
		op.Define(op.Spec{Name: OpEdit, Summary: "Write back an edited Markdown view of a row (lore edit --id <id> opens your editor for it)",
			Permission: op.PermWrite, Access: op.AccessWrite, Confirm: op.ConfirmRule{MayAsk: true}, Errors: editErrors,
			Examples: []op.Example{
				{Title: "write back a view edited with any tool", CLI: "lore row edit --id tsk_<id> --file t.md", Want: "updated"},
				{Title: "open your editor instead", CLI: "lore edit --id tsk_<id>"},
			}}, o.edit),
	}
}

// parseID reads a row id argument.
func parseID(s string) (ids.AnyID, error) {
	s = strings.TrimSpace(s)
	id, err := ids.ParseAny(s)
	if err != nil {
		return ids.AnyID{}, lookup.BadID(s+" is not a row id", err).
			WithHint("a row id is a kind prefix and 32 hex characters (tsk_…, mem_…, rul_…); find one with lore search --query <words>")
	}
	return id, nil
}

// locator is env's Locator, or E_UNSUPPORTED.
func (o ops) locator() (Locator, error) {
	l, ok := upgrade.As[Locator](o.env)
	if !ok {
		return nil, errs.New(errs.Unsupported, "path: this build's store wiring cannot say where rows are kept")
	}
	return l, nil
}

// path is row.path: one absolute path (D-j). A row with no file of its
// own (a live row, a landed row not pulled yet) is refused with where it
// is instead, so a script never opens a stale or missing file.
func (o ops) path(ctx context.Context, in PathIn) (PathOut, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return PathOut{}, err
	}
	l, err := o.locator()
	if err != nil {
		return PathOut{}, err
	}
	loc, err := l.RowLocate(ctx, id)
	if err != nil {
		return PathOut{}, err
	}
	if err := noFile(id, loc); err != nil {
		return PathOut{}, err
	}
	return PathOut{ID: id.String(), Table: string(loc.Table), Path: loc.Path, Store: loc.Store, Folder: loc.Folder, Deleted: loc.Deleted}, nil
}

// noFile is the refusal for a row whose newest version is not a file in
// this checkout; nil when loc.Path is it.
func noFile(id ids.AnyID, loc Location) error {
	switch {
	case loc.NotPulled:
		return errs.New(errs.NotFound, id.String()+" has no file in this checkout yet: it landed on "+loc.LiveRef+" ("+loc.LivePath+") and this checkout has not pulled it").
			WithHint("git pull brings its file; lore show --id " + id.String() + " prints it now")
	case loc.LiveRef != "":
		return errs.New(errs.NotFound, id.String()+" has no file of its own yet: it is a live row, kept on the git ref "+loc.LiveRef+" ("+loc.LivePath+") so everyone sees it before any commit").
			WithHint("lore edit " + id.String() + " edits it there; lore show --id " + id.String() + " prints it; it gets a file at " + loc.Path + " when it lands on the default branch (lore land)")
	case !loc.Exists:
		return errs.New(errs.NotFound, id.String()+" is in store "+loc.Store+" but its file "+loc.Path+" is missing (deleted by hand?)").
			WithHint("lore doctor checks the store; lore show --id " + id.String() + " prints the copy lore still has")
	}
	return nil
}

// view is the current row, its table and the Markdown view's place.
type view struct {
	src  string
	cur  row.Row
	def  schema.TableDef
	loc  Location
	live string
}

// load reads the row a view is of, refusing a deleted row and a row whose
// sensitive values the view would write into a plain file.
func (o ops) load(ctx context.Context, id ids.AnyID) (view, error) {
	src, r, err := o.env.Get(ctx, engine.Ref{ID: id})
	if err != nil {
		return view{}, err
	}
	if r.Deleted {
		return view{}, o.gone(ctx, engine.Ref{Store: src, Table: r.Table, ID: id})
	}
	def, ok := o.env.TableSchema(ctx, r.Table)
	if !ok {
		return view{}, errs.New(errs.Unsupported, "edit: this lore version has no table "+string(r.Table)+" for "+id.String()).
			WithHint("lore upgrade, or edit the JSON file directly (lore path " + id.String() + ")")
	}
	for _, f := range def.Fields {
		if v, held := r.Fields[f.Name]; f.Sensitive && held && v.Kind() != value.KindNull {
			return view{}, sensitiveRefusal(id, f.Name)
		}
	}
	if _, names, ok := resmask.Fields(ctx, o.env, r, hidden); ok && len(names) > 0 {
		return view{}, sensitiveRefusal(id, strings.Join(names, ", "))
	}
	v := view{src: string(src), cur: r, def: def}
	if l, ok := upgrade.As[Locator](o.env); ok {
		loc, lerr := l.RowLocate(ctx, id)
		if lerr != nil {
			return view{}, lerr
		}
		v.loc = loc
		if loc.LiveRef != "" {
			v.live = loc.LiveRef + ": " + loc.LivePath
		}
	}
	return v, nil
}

// sensitiveRefusal: the view would put sensitive values in a plain file.
func sensitiveRefusal(id ids.AnyID, fields string) error {
	return errs.New(errs.Unsupported, id.String()+" holds sensitive values ("+fields+"); lore edit would write them into a plain temporary file, so it does not open this row").
		WithHint("change them with the row's own command (lore resource edit --id " + id.String() + " …)")
}

// markdown is row.markdown.
func (o ops) markdown(ctx context.Context, in MarkdownIn) (MarkdownOut, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return MarkdownOut{}, err
	}
	v, err := o.load(ctx, id)
	if err != nil {
		return MarkdownOut{}, err
	}
	path := v.loc.Path
	if v.live != "" {
		path = ""
	}
	text, err := rowmd.Render(v.def, v.cur, rowmd.Where{Store: v.src, Folder: v.loc.Folder, Path: path, Live: v.live})
	if err != nil {
		return MarkdownOut{}, err
	}
	return MarkdownOut{ID: id.String(), Table: string(v.cur.Table), Store: v.src, Folder: v.loc.Folder, Path: path, Live: v.live,
		BodyField: rowmd.BodyField(v.def), Markdown: string(text)}, nil
}

// edit is row.edit: parse the view against the row as it is now, then
// write the changed fields through the normal write path.
func (o ops) edit(ctx context.Context, in EditIn) (EditOut, error) {
	id, err := parseID(in.ID)
	if err != nil {
		return EditOut{}, err
	}
	// the row first: an unknown, trashed or purged id is answered as such
	// (the lookup contract every id command keeps), before the view
	v, err := o.load(ctx, id)
	if err != nil {
		return EditOut{}, err
	}
	text, label, err := editText(in)
	if err != nil {
		return EditOut{}, err
	}
	res, err := rowmd.Parse(v.def, v.cur, label, text)
	if err != nil {
		return EditOut{}, err
	}
	out := EditOut{ID: id.String(), Table: string(v.cur.Table), Store: v.src, Changed: res.Changed, NoChange: res.NoChange()}
	if v.live == "" {
		out.Path = v.loc.Path
	}
	if out.NoChange {
		out.Changed = []string{}
		return out, nil
	}
	up, ok := upgrade.As[Updater](o.env)
	if !ok {
		return EditOut{}, errs.New(errs.Unsupported, "edit: this build's store wiring cannot write rows")
	}
	w, err := up.Update(ctx, engine.Ref{Store: store.Source(v.src), Table: v.cur.Table, ID: id}, res.Patch)
	if err != nil {
		return EditOut{}, err
	}
	for _, wn := range w.Warnings {
		out.Warnings = append(out.Warnings, wn.Message)
	}
	return out, nil
}

// editText is the edited view and the name refusals give it.
func editText(in EditIn) ([]byte, string, error) {
	file, inline := strings.TrimSpace(in.File), in.Markdown
	switch {
	case file != "" && inline != "":
		return nil, "", errs.New(errs.InvalidInput, "edit: give --file or --markdown, not both")
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, "", fromfile.Unreadable("edit", file, err).
				WithHint("name the Markdown view file to write back (lore row markdown --id <id> > view.md makes one)")
		}
		return b, file, nil
	case inline != "":
		return []byte(inline), markdownLabel, nil
	}
	return nil, "", errs.New(errs.InvalidInput, "edit: no view to write back: give --file <view.md> (or --markdown)").
		WithHint("lore edit --id <id> opens the row in your editor and writes it back; lore row markdown --id <id> prints the view to edit with another tool")
}
