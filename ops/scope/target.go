package scope

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/dirscope"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Env is the part of the engine row API the scope work needs. It is a
// subset of every knowledge noun's Env (ops/memory.Env), declared here so
// ops/memory can import this package without a cycle.
type Env interface {
	Create(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error)
	Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error)
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
}

// RepoIdentifier is the optional Env upgrade that names this checkout's
// repos row (PROV1: _meta.json repo_id is the checkout's own rep_ row).
// Without it, repo and dir scopes are refused (fail closed).
type RepoIdentifier interface {
	RepoIdentity(ctx context.Context) (repoID string, ok bool)
}

// RepoTopper is the optional Env upgrade that gives the code repository's
// top folder, so --scope dir:<path> can check the folder exists (6.2.3)
// and `lore scope show|list` can name the files. Without it the existence
// check is skipped with a warning.
type RepoTopper interface {
	RepoTop(ctx context.Context) (string, error)
}

// Target is what a --scope value means for one write.
type Target struct {
	Spec dirscope.Spec
	// To is the store the row must go to ("me" for global; "" = leave).
	To string
	// Repo is the rep_ id to bind; Master clears repo_id (project scope).
	Repo   string
	Master bool
	// ScopeID is the scp_ id to set; ClearScope writes null scope_id
	// (project and repo scopes on edit).
	ScopeID    string
	ClearScope bool
	// Warnings are lines for the write's output (path not checked).
	Warnings []string
}

// Given reports whether a --scope value was given.
func (t Target) Given() bool { return t.Spec.Kind != dirscope.KindDefault }

// Personal store target of the global scope (6.2.1: a row of the
// personal store).
const toPersonal = "me"

// ForWrite resolves raw (--scope) for a write to store to (--to) with
// --repo repo. A dir scope makes (or finds) the folder's scp_ row in the
// repo store first, so the ref the new row carries exists (logic/validate
// refuses dangling refs).
//
// Decisions: dir scopes live only in the repo store (committed folder
// files hold the repo's own rows, CFG6), so --to another store is refused;
// org scope is refused with a hint to name the store with --to (the
// org target spelling belongs to the stores layer, OV25-1); --repo with a
// repo or dir scope is refused (two answers to one question).
func ForWrite(ctx context.Context, env Env, raw, to, repo string, edit bool) (Target, error) {
	spec, err := dirscope.Parse(raw)
	if err != nil {
		return Target{}, err
	}
	t := Target{Spec: spec}
	switch spec.Kind {
	case dirscope.KindDefault:
		return t, nil
	case dirscope.KindGlobal:
		if edit {
			return Target{}, errs.New(errs.InvalidInput, "--scope global moves a row to your personal store").WithHint("use `lore move --id <id> --to me`")
		}
		if to != "" && to != toPersonal {
			return Target{}, errs.New(errs.InvalidInput, "--scope global writes to your personal store; it cannot go with --to "+to)
		}
		t.To = toPersonal
		return t, nil
	case dirscope.KindOrg:
		return Target{}, errs.New(errs.InvalidInput, "--scope "+spec.Label()+": org rows are written with --to <org store>").
			WithHint("run `lore store list` and give --to <store>")
	case dirscope.KindProject:
		t.Master, t.ClearScope = true, true
		return t, refuseRepoFlag(repo, spec)
	case dirscope.KindRepo, dirscope.KindDir:
	}
	if err = refuseRepoFlag(repo, spec); err != nil {
		return Target{}, err
	}
	rid, err := ownRepo(ctx, env)
	if err != nil {
		return Target{}, err
	}
	t.Repo = rid
	if spec.Kind == dirscope.KindRepo {
		t.ClearScope = true
		return t, nil
	}
	if to != "" && to != string(store.SourceRepo) {
		return Target{}, errs.New(errs.InvalidInput, "--scope "+spec.Label()+": folder scopes are rows of the repo store; it cannot go with --to "+to)
	}
	w, err := checkDir(ctx, env, spec.Path)
	if err != nil {
		return Target{}, err
	}
	if w != "" {
		t.Warnings = append(t.Warnings, w)
	}
	id, err := Ensure(ctx, env, rid, spec.Path)
	if err != nil {
		return Target{}, err
	}
	t.ScopeID = id.String()
	return t, nil
}

// refuseRepoFlag refuses --repo next to a scope that decides the repo.
func refuseRepoFlag(repo string, spec dirscope.Spec) error {
	if repo == "" {
		return nil
	}
	return errs.New(errs.InvalidInput, "give --scope "+spec.Label()+" or --repo "+repo+", not both")
}

// ownRepo is this checkout's rep_ id.
func ownRepo(ctx context.Context, env Env) (string, error) {
	ri, ok := upgrade.As[RepoIdentifier](env)
	if !ok {
		return "", errs.New(errs.Unsupported, "repo and folder scopes need this checkout's repo identity, which this setup lacks")
	}
	rid, known := ri.RepoIdentity(ctx)
	if !known {
		return "", errs.New(errs.InvalidInput, "this checkout has no repo identity yet").WithHint("run `lore init` in the repo first")
	}
	return rid, nil
}

// checkDir refuses a folder that does not exist in the checkout (6.2.3);
// without a RepoTopper it returns a warning instead.
func checkDir(ctx context.Context, env Env, dir string) (string, error) {
	top, ok := topOf(ctx, env)
	if !ok {
		return "folder " + dir + " was not checked (no repo folder known)", nil
	}
	fi, err := os.Stat(filepath.Join(top, filepath.FromSlash(dir)))
	if err != nil || !fi.IsDir() {
		return "", errs.New(errs.InvalidInput, "dir scope "+dir+": no such folder in the repo").WithHint("paths are relative to the repo root: " + top)
	}
	return "", nil
}

// topOf is the repo's top folder, ok false when unknown.
func topOf(ctx context.Context, env Env) (string, bool) {
	rt, ok := upgrade.As[RepoTopper](env)
	if !ok {
		return "", false
	}
	top, err := rt.RepoTop(ctx)
	return top, err == nil && top != ""
}

// Scope row fields.
const (
	fieldRepo = "repo_id"
	fieldPath = schema.FieldScopePath
)

// Ensure returns the live scope row of repo rid at dir in the repo store,
// creating it when there is none.
func Ensure(ctx context.Context, env Env, rid, dir string) (ids.AnyID, error) {
	rows, err := Rows(ctx, env, rid)
	if err != nil {
		return ids.AnyID{}, err
	}
	for _, r := range rows {
		if text(r, fieldPath) == dir {
			return r.ID, nil
		}
	}
	repoID, err := ids.ParseAny(rid)
	if err != nil {
		return ids.AnyID{}, errs.Wrap(errs.Internal, "scope: repo identity", err)
	}
	ref, err := value.RefOf(repoID)
	if err != nil {
		return ids.AnyID{}, err
	}
	p, err := value.TextOf(dir)
	if err != nil {
		return ids.AnyID{}, errs.Wrap(errs.InvalidInput, "scope: path", err)
	}
	w, err := env.Create(ctx, store.SourceRepo, schema.TableScopes, map[string]value.Value{fieldRepo: ref, fieldPath: p})
	if err != nil {
		return ids.AnyID{}, err
	}
	return w.Row.ID, nil
}

// Rows lists the live scope rows of repo rid in the repo store.
func Rows(ctx context.Context, env Env, rid string) ([]row.Row, error) {
	all, err := env.List(ctx, store.SourceRepo, schema.TableScopes)
	if err != nil {
		return nil, err
	}
	var out []row.Row
	for _, r := range all {
		if !r.Deleted && text(r, fieldRepo) == rid {
			out = append(out, r)
		}
	}
	return out, nil
}

// text is a field's text form ("" when absent or null).
func text(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// PlaceOf is where row r attaches: its repo_id and its scope's path (the
// scp_ id itself when the scope row is gone, so it still ranks as a
// folder).
func PlaceOf(ctx context.Context, env Env, r row.Row) dirscope.Place {
	p := dirscope.Place{Repo: text(r, fieldRepo)}
	sid := text(r, schema.FieldScope)
	if sid == "" {
		return p
	}
	p.Path = sid
	id, err := ids.ParseAny(sid)
	if err != nil {
		return p
	}
	if _, s, err := env.Get(ctx, engine.Ref{Store: store.SourceRepo, Table: schema.TableScopes, ID: id}); err == nil && !s.Deleted {
		p.Path = text(s, fieldPath)
	}
	return p
}

// Place is where t puts a row; cur is the row's place before the write
// (an add passes the zero Place with repo set by --repo).
func (t Target) Place(cur dirscope.Place) dirscope.Place {
	switch {
	case t.Master:
		return dirscope.Place{}
	case t.Repo != "":
		return dirscope.Place{Repo: t.Repo, Path: t.Spec.Path}
	}
	return cur
}

// CheckOverrides parses raw (--overrides) as an id of table (prefix p),
// checks the row is live and attaches at a scope strictly wider than
// narrow that covers it (6.2.2), and returns the id. noun names it in
// messages.
func CheckOverrides(ctx context.Context, env Env, noun string, table row.Table, p ids.Prefix, raw string, narrow dirscope.Place, self string) (string, error) {
	raw = strings.TrimSpace(raw)
	id, err := ids.ParseAny(raw)
	if err != nil || id.Prefix() != p {
		return "", errs.New(errs.InvalidID, "--overrides "+raw+": not a "+noun+" id ("+string(p)+"_…)")
	}
	if id.String() == self {
		return "", errs.New(errs.InvalidInput, "--overrides: a "+noun+" cannot override itself")
	}
	_, r, err := env.Get(ctx, engine.Ref{Table: table, ID: id})
	if errs.Has(err, errs.NotFound) || (err == nil && r.Deleted) {
		return "", errs.New(errs.NotFound, "--overrides "+raw+": no such "+noun)
	}
	if err != nil {
		return "", err
	}
	wide := PlaceOf(ctx, env, r)
	if !dirscope.Wider(wide, narrow) {
		return "", errs.New(errs.InvalidInput, "--overrides "+raw+": that "+noun+" is at "+Label(wide)+", which is not wider than "+Label(narrow)).
			WithHint("a row may override only a row of a more general scope that covers it (project > repo > folder > subfolder)")
	}
	return id.String(), nil
}

// Label is a place as the user reads it.
func Label(p dirscope.Place) string {
	switch {
	case p.Repo == "":
		return string(dirscope.KindProject)
	case p.Path == "":
		return string(dirscope.KindRepo)
	}
	return "dir:" + p.Path
}
