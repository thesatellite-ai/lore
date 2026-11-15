package wire

// branchlayer.go — branch layers for the repo's project folder in a store
// (BRANCH_LAYERS_SPEC 7, 8): a backend wrapper in front of the folder's git
// store clone that, on an isolated code branch, reads the folder's shared
// notes with the layer's rows on top and writes every row onto the layer.
//
//	open ─▶ attached store at a project folder (policy.LevelFolder), a
//	        git clone, the repo in a git checkout ─▶ layerBackend
//	Read  ─▶ shared branch: the clone's rows; isolated: the clone's rows
//	         with the layer's changed rows in their place (cursor
//	         "layer:<tip>|<clone cursor>")
//	Get   ─▶ the layer's row when it changed it, else the clone's
//	Write ─▶ shared: the clone (as today); isolated: git.LayerWrite, one
//	         commit on refs/lore/layer/…, never the store branch, never a
//	         push; detached HEAD: E_DETACHED
//	Sync  ─▶ the clone's sync, then the folder's layers pushed (merged
//	         with a teammate's copy first)
//
// Which branch is isolated is logic/branchlayer.Classify over the
// branches.* policy keys, read once per command through the engine (the
// read runs with the wrapper switched off, so it cannot ask itself).

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// EnvLayer turns branch layers off for one command: LORE_LAYER=off writes
// to the shared notes and reads them alone, like --no-layer.
const EnvLayer = "LORE_LAYER"

// envLayerOff is EnvLayer's value that turns layers off.
const envLayerOff = "off"

// layerCursorPrefix starts a cursor the layer wrapper issued.
const (
	layerCursorPrefix = "layer:"
	layerCursorSep    = "|"
)

// codeRepo is what a layer needs to know about the code checkout.
type codeRepo struct {
	Top      string
	Branch   string // "" on a detached HEAD
	Detached bool
	Default  string // the default branch
	Remote   string // normalized origin url ("" when none)
	Split    string // merge-base of HEAD and origin/<default> ("" when unknown)
	Head     string // HEAD commit
}

// readCodeRepo asks git about the checkout at top. A folder that is no git
// checkout, or has no remote, gives ok false: no layer applies.
func readCodeRepo(ctx context.Context, top string) (codeRepo, bool) {
	c := codeRepo{Top: top}
	run := func(args ...string) string {
		out, err := liveGit(ctx, top, args...)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	raw := run("config", "--get", "remote.origin.url")
	if raw == "" {
		return c, false
	}
	norm, err := repourl.Normalize(raw)
	if err != nil {
		return c, false
	}
	c.Remote = norm
	c.Head = run("rev-parse", "--verify", "--quiet", "HEAD")
	c.Branch = run("symbolic-ref", "--short", "--quiet", "HEAD")
	c.Detached = c.Branch == "" && c.Head != ""
	c.Default = strings.TrimPrefix(run("symbolic-ref", "--short", "--quiet", "refs/remotes/origin/HEAD"), "origin/")
	if c.Default == "" {
		c.Default = run("config", "--get", "init.defaultBranch")
	}
	if c.Default == "" {
		c.Default = defaultCodeBranch
	}
	if c.Head != "" {
		c.Split = run("merge-base", "HEAD", "refs/remotes/origin/"+c.Default)
	}
	return c, true
}

// defaultCodeBranch is the default branch of a clone that names none.
const defaultCodeBranch = "main"

// layerGuardKey marks a context on which every layer wrapper passes reads
// and writes straight to the clone (the policy read of the decision).
type layerGuardKey struct{}

// withoutLayers returns ctx with the layer wrappers switched off.
func withoutLayers(ctx context.Context) context.Context {
	return context.WithValue(ctx, layerGuardKey{}, true)
}

// layersOff reports whether ctx switched the layer wrappers off.
func layersOff(ctx context.Context) bool {
	v, _ := ctx.Value(layerGuardKey{}).(bool)
	return v
}

// layerSettings reads the branches.* policy for the folder a layer
// wrapper serves.
type layerSettings func(ctx context.Context) (branchlayer.Settings, error)

// layerBackend is the wrapper (see the file comment).
type layerBackend struct {
	inner store.Backend
	gb    *git.Backend
	name  store.Source
	code  codeRepo
	key   branchlayer.Key
	// settings reads the policy once (decide).
	settings layerSettings

	decideOnce sync.Once
	decision   branchlayer.Decision
	decideErr  error

	mu      sync.Mutex
	tip     string                  // the tip rows was read at
	rows    map[store.Key]store.Row // the layer's changed rows at tip
	rowsErr error
}

var (
	_ store.Backend = (*layerBackend)(nil)
	_ Unwrapper     = (*layerBackend)(nil)
)

// newLayerBackend wraps b when it is a git store clone serving a project
// folder of a repo in a git checkout with an origin remote; else b.
func newLayerBackend(ctx context.Context, name store.Source, folder string, b store.Backend, code codeRepo, settings layerSettings) store.Backend {
	gb, ok := concrete(b).(*git.Backend)
	if !ok || gb.Branch() == "" || folder == "" || folder == "." {
		return b
	}
	key := branchlayer.Key{StoreBranch: gb.Branch(), Folder: folder, RepoKey: branchlayer.RepoKey(code.Remote), Branch: code.Branch}
	if code.Branch == "" {
		key.Branch = "HEAD" // detached: never written (E_DETACHED), only named
	}
	if key.Check() != nil {
		return b
	}
	return &layerBackend{inner: b, gb: gb, name: name, code: code, key: key, settings: settings}
}

// Unwrap implements Unwrapper.
func (l *layerBackend) Unwrap() store.Backend { return l.inner }

// LayerCacheDir is the cache sub-folder of this wrapper's code branch
// (BRANCH_LAYERS 6.3): "" on the default branch and a detached HEAD, which
// read the shared notes only and keep the folder's own cache file.
func (l *layerBackend) LayerCacheDir() string {
	if l.code.Branch == "" || l.code.Branch == l.code.Default || os.Getenv(EnvLayer) == envLayerOff {
		return ""
	}
	return "_layers/" + l.key.RepoKey + "/" + branchlayer.BranchKey(l.code.Branch)
}

// decide classifies the code branch once per command.
func (l *layerBackend) decide(ctx context.Context) (branchlayer.Decision, error) {
	l.decideOnce.Do(func() {
		s := branchlayer.DefaultSettings()
		if l.settings != nil {
			got, err := l.settings(withoutLayers(ctx))
			if err != nil {
				l.decideErr = err
				return
			}
			s = got
		}
		l.decision = branchlayer.Classify(l.code.Branch, l.code.Default, s)
	})
	return l.decision, l.decideErr
}

// isolated reports whether this command reads and writes the layer.
func (l *layerBackend) isolated(ctx context.Context) (bool, error) {
	if layersOff(ctx) || os.Getenv(EnvLayer) == envLayerOff || l.code.Detached || l.code.Branch == "" {
		return false, nil
	}
	d, err := l.decide(ctx)
	if err != nil {
		return false, err
	}
	return d.Mode == branchlayer.Isolated, nil
}

// layerRows are the layer's changed rows, read once per tip.
func (l *layerBackend) layerRows(ctx context.Context) (string, map[store.Key]store.Row, error) {
	ly, err := l.gb.LayerOpen(ctx, l.key)
	if err != nil {
		return "", nil, err
	}
	// a stacked layer reads its parent's notes under its own (BRANCH_LAYERS 12)
	var parent git.Layer
	if ly.Exists() && ly.Facts.Parent != "" {
		pk := l.key
		pk.Branch = ly.Facts.Parent
		if parent, err = l.gb.LayerOpen(ctx, pk); err != nil {
			return "", nil, err
		}
	}
	tip := ly.Tip
	if parent.Exists() {
		tip += "+" + parent.Tip
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if tip == l.tip && l.rows != nil {
		return l.tip, l.rows, l.rowsErr
	}
	m := map[store.Key]store.Row{}
	var rerr error
	if parent.Exists() {
		prows, perr := l.gb.LayerRows(ctx, parent)
		for _, r := range prows {
			m[r.Key()] = r
		}
		rerr = perr
	}
	rows, err := l.gb.LayerRows(ctx, ly)
	for _, r := range rows {
		m[r.Key()] = r
	}
	l.tip, l.rows, l.rowsErr = tip, m, errors.Join(rerr, err)
	return l.tip, m, l.rowsErr
}

// Read implements store.Backend.
func (l *layerBackend) Read(ctx context.Context, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	iso, err := l.isolated(ctx)
	if err != nil {
		return nil, "", err
	}
	tip, inner := parseLayerCursor(cursor)
	if !iso {
		if tip != "" {
			inner = "" // the cache holds layer rows: read everything again
		}
		return l.inner.Read(ctx, inner)
	}
	nowTip, layer, err := l.layerRows(ctx)
	if err != nil {
		return nil, "", err
	}
	if tip != nowTip || !strings.HasPrefix(string(cursor), layerCursorPrefix) {
		inner = "" // the layer moved (or this cache never saw it): a full read
	}
	rows, next, err := l.inner.Read(ctx, inner)
	if err != nil {
		return nil, "", err
	}
	out := make([]store.Row, 0, len(rows)+len(layer))
	for _, r := range rows {
		if lr, ok := layer[r.Key()]; ok {
			out = append(out, lr)
			continue
		}
		out = append(out, r)
	}
	if inner == "" {
		seen := make(map[store.Key]bool, len(rows))
		for _, r := range rows {
			seen[r.Key()] = true
		}
		for k, lr := range layer {
			if !seen[k] {
				out = append(out, lr)
			}
		}
	}
	return out, store.Cursor(layerCursorPrefix + nowTip + layerCursorSep + string(next)), nil
}

// parseLayerCursor splits a layer cursor into the layer tip and the
// clone's cursor; any other cursor is the clone's own.
func parseLayerCursor(c store.Cursor) (tip string, inner store.Cursor) {
	rest, ok := strings.CutPrefix(string(c), layerCursorPrefix)
	if !ok {
		return "", c
	}
	t, in, _ := strings.Cut(rest, layerCursorSep)
	return t, store.Cursor(in)
}

// Get implements store.Backend.
func (l *layerBackend) Get(ctx context.Context, table row.Table, id ids.AnyID) (store.Row, error) {
	iso, err := l.isolated(ctx)
	if err != nil {
		return store.Row{}, err
	}
	if iso {
		_, layer, err := l.layerRows(ctx)
		if err != nil {
			return store.Row{}, err
		}
		if r, ok := layer[store.Key{Table: table, ID: id}]; ok {
			return r, nil
		}
	}
	return l.inner.Get(ctx, table, id)
}

// Write implements store.Backend.
func (l *layerBackend) Write(ctx context.Context, changes []store.Change) error {
	if store.NoLayer(ctx) || layersOff(ctx) || os.Getenv(EnvLayer) == envLayerOff {
		return l.inner.Write(ctx, changes)
	}
	if l.code.Detached {
		d, err := l.decide(ctx)
		if err != nil {
			return err
		}
		if d.Step != branchlayer.StepOff {
			return errs.New(errs.Detached, "the code repo "+l.code.Top+" has a detached HEAD, so this write has no branch to keep its notes with").
				WithHint("check out a branch (git switch <branch>), or write to the shared notes with --no-layer (LORE_LAYER=off)")
		}
		return l.inner.Write(ctx, changes)
	}
	iso, err := l.isolated(ctx)
	if err != nil {
		return err
	}
	if !iso {
		return l.inner.Write(ctx, changes)
	}
	if err := l.refuseStale(ctx); err != nil {
		return err
	}
	res, err := l.gb.LayerWrite(ctx, git.LayerWriteRequest{
		Key:     l.key,
		Changes: changes,
		Op:      store.CommitOp(ctx),
		Facts: branchlayer.Facts{Repo: l.code.Remote, BaseBranch: l.code.Default, Split: l.code.Split,
			CodeHead: l.code.Head, By: actorOfChanges(changes), Parent: l.parentBranch(ctx)},
	})
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.rows = nil // the next read sees the new tip
	l.mu.Unlock()
	for _, c := range changes {
		NoteLayerWrite(ctx, c.Row.Table, c.Row.ID.String(), l.code.Branch, l.key.Folder)
	}
	if res.Declared != "" {
		NoteLayerDeclared(ctx, l.key.Folder)
	}
	return nil
}

// parentBranch is the code branch whose open layer this branch is stacked
// on (BRANCH_LAYERS 12), "" when none: an open layer of this repo whose
// code branch tip is in HEAD and holds commits of its own that the
// default branch does not have; the nearest one when several are. Only
// asked when the branch has no layer yet (a layer keeps its parent).
func (l *layerBackend) parentBranch(ctx context.Context) string {
	if ly, err := l.gb.LayerOpen(ctx, l.key); err != nil || ly.Exists() {
		return ""
	}
	entries, err := l.gb.LayerList(ctx, l.key.RepoKey, false)
	if err != nil {
		return ""
	}
	top, main := l.code.Top, "refs/remotes/origin/"+l.code.Default
	if !liveRefExists(ctx, top, main) {
		main = "refs/heads/" + l.code.Default
	}
	best, bestTip := "", ""
	for _, e := range entries {
		b := e.Layer.Key.Branch
		if b == l.code.Branch {
			continue
		}
		tip := ""
		for _, ref := range []string{"refs/heads/" + b, "refs/remotes/origin/" + b} {
			if out, gerr := liveGit(ctx, top, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); gerr == nil {
				tip = strings.TrimSpace(string(out))
				break
			}
		}
		if tip == "" {
			continue
		}
		if _, gerr := liveGit(ctx, top, "merge-base", "--is-ancestor", tip, "HEAD"); gerr != nil {
			continue // not in this branch
		}
		if _, gerr := liveGit(ctx, top, "merge-base", "--is-ancestor", tip, main); gerr == nil {
			continue // nothing of its own beyond the default branch
		}
		if bestTip != "" {
			if _, gerr := liveGit(ctx, top, "merge-base", "--is-ancestor", tip, bestTip); gerr == nil {
				continue // an older ancestor of the best so far
			}
		}
		best, bestTip = b, tip
	}
	return best
}

// refuseStale refuses a write on a reused branch name: an open layer whose
// split commit is not in this branch belongs to an older branch.
func (l *layerBackend) refuseStale(ctx context.Context) error {
	ly, err := l.gb.LayerOpen(ctx, l.key)
	if err != nil || !ly.Exists() || ly.Facts.Split == "" || l.code.Head == "" {
		return err
	}
	if _, err := liveGit(ctx, l.code.Top, "merge-base", "--is-ancestor", ly.Facts.Split, "HEAD"); err == nil {
		return nil
	}
	if _, err := liveGit(ctx, l.code.Top, "cat-file", "-e", ly.Facts.Split+"^{commit}"); err != nil {
		return nil // the split commit is not in this clone: cannot tell, keep writing
	}
	return errs.New(errs.LayerStale, "the open notes of "+l.code.Branch+" in "+l.key.Folder+" belong to an older branch of the same name (it left "+l.code.Default+" at "+short7(ly.Facts.Split)+", which this branch does not contain)").
		WithHint("land or drop the old notes first: lore branch land --branch " + l.code.Branch + " or lore branch drop --branch " + l.code.Branch)
}

// short7 is the first 7 characters of a commit id.
func short7(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

// actorOfChanges is the updated_by of the first change ("" when none can
// be read): the actor who started a layer.
func actorOfChanges(changes []store.Change) string {
	for _, c := range changes {
		if r, err := c.Row.Decode(); err == nil && !r.UpdatedBy.IsZero() {
			return r.UpdatedBy.String()
		}
	}
	return ""
}

// Sync implements store.Backend: the clone's sync, which also sends the
// clone's branch layers (git syncClone, BRANCH_LAYERS 16).
func (l *layerBackend) Sync(ctx context.Context) (store.SyncReport, error) { return l.inner.Sync(ctx) }

// Capabilities implements store.Backend.
func (l *layerBackend) Capabilities() store.Capabilities { return l.inner.Capabilities() }

// Close implements store.Backend.
func (l *layerBackend) Close() error { return l.inner.Close() }

// layerCacheDirOf is the layer cache sub-folder of b's outermost layer
// wrapper ("" when b has none).
func layerCacheDirOf(b store.Backend) string {
	for {
		if lb, ok := b.(*layerBackend); ok {
			return lb.LayerCacheDir()
		}
		u, ok := b.(Unwrapper)
		if !ok {
			return ""
		}
		b = u.Unwrap()
	}
}

// layerOf is b's layer wrapper, nil when it has none.
func layerOf(b store.Backend) *layerBackend {
	for {
		if lb, ok := b.(*layerBackend); ok {
			return lb
		}
		u, ok := b.(Unwrapper)
		if !ok {
			return nil
		}
		b = u.Unwrap()
	}
}

// policySettings reads the branches.* keys of the policy a write to name
// sees into branchlayer.Settings.
func policySettings(st *state, name store.Source) layerSettings {
	return func(ctx context.Context) (branchlayer.Settings, error) {
		s := branchlayer.DefaultSettings()
		if st.eng == nil {
			return s, nil
		}
		set, err := st.eng.Policy(ctx, name)
		if err != nil {
			return s, err
		}
		if v, ok := set.Flag(policy.KeyBranchesLayers); ok {
			s.Layers = v
		}
		if v, ok := set.Text(policy.KeyBranchesDefaultMode); ok {
			s.DefaultMode = branchlayer.ModeOfPolicy(v)
		}
		s.Isolate = set.Strings(policy.KeyBranchesIsolate)
		s.MainLike = set.Strings(policy.KeyBranchesMainLike)
		s.IsolatedSet = set.Strings(policy.KeyBranchesIsolated)
		s.SharedSet = set.Strings(policy.KeyBranchesShared)
		return s, nil
	}
}

// wrapLayers puts a layer wrapper in front of every attached project
// folder of a git store clone (policy.LevelFolder) when the repo is a git
// checkout with an origin remote.
func wrapLayers(ctx context.Context, st *state, attached []attach.Opened) {
	var code codeRepo
	asked, ok := false, false
	for i := range attached {
		if levelOf(attached[i]) != policy.LevelFolder {
			continue
		}
		if _, isGit := concrete(attached[i].Backend).(*git.Backend); !isGit {
			continue
		}
		if !asked {
			code, ok = readCodeRepo(ctx, st.repoTop)
			asked = true
		}
		if !ok {
			return
		}
		attached[i].Backend = newLayerBackend(ctx, attached[i].Name, attached[i].Folder, attached[i].Backend, code, policySettings(st, attached[i].Name))
	}
}

// The optional interfaces of the wrapped backend (the engine asserts them
// on the outermost backend, so a wrapper that dropped them would refuse
// purges and lose the audit journal): passed through, with a write on an
// isolated branch going to the layer.
var (
	_ store.AuditJournal      = (*layerBackend)(nil)
	_ store.PurgeAuditJournal = (*layerBackend)(nil)
	_ engine.Purger           = (*layerBackend)(nil)
)

// WriteAudited implements store.AuditJournal: on an isolated branch the
// rows go to the layer (the engine's audit sink records the events after
// the write as for any backend without a journal); otherwise the wrapped
// backend journals them with its commit.
func (l *layerBackend) WriteAudited(ctx context.Context, changes []store.Change, recs []store.AuditRecord) error {
	j, ok := l.inner.(store.AuditJournal)
	if !ok || l.writesToLayer(ctx) {
		return l.Write(ctx, changes)
	}
	return j.WriteAudited(ctx, changes, recs)
}

// PendingAudit implements store.AuditJournal.
func (l *layerBackend) PendingAudit(ctx context.Context) ([]store.AuditRecord, error) {
	if j, ok := l.inner.(store.AuditJournal); ok {
		return j.PendingAudit(ctx)
	}
	return nil, nil
}

// AckAudit implements store.AuditJournal.
func (l *layerBackend) AckAudit(ctx context.Context, recs []store.AuditRecord) error {
	if j, ok := l.inner.(store.AuditJournal); ok {
		return j.AckAudit(ctx, recs)
	}
	return nil
}

// WritePurge implements engine.Purger: a purge acts on the shared notes
// (it is the explicit, confirmed removal of a row for good), never on a
// layer.
func (l *layerBackend) WritePurge(ctx context.Context, changes []store.Change, purged []ids.AnyID) error {
	p, ok := l.inner.(engine.Purger)
	if !ok {
		return errs.New(errs.Unsupported, "store "+string(l.name)+" cannot record purged ids")
	}
	return p.WritePurge(ctx, changes, purged)
}

// Purged implements engine.Purger.
func (l *layerBackend) Purged(ctx context.Context) (map[string]bool, error) {
	p, ok := l.inner.(engine.Purger)
	if !ok {
		return nil, errs.New(errs.Unsupported, "store "+string(l.name)+" cannot record purged ids")
	}
	return p.Purged(ctx)
}

// WritePurgeAudited implements store.PurgeAuditJournal.
func (l *layerBackend) WritePurgeAudited(ctx context.Context, changes []store.Change, purged []ids.AnyID, recs []store.AuditRecord) error {
	j, ok := l.inner.(store.PurgeAuditJournal)
	if !ok {
		return l.WritePurge(ctx, changes, purged)
	}
	return j.WritePurgeAudited(ctx, changes, purged, recs)
}

// writesToLayer reports whether a write with ctx goes to the layer (the
// test Write itself makes; a detached HEAD's refusal is Write's to give).
func (l *layerBackend) writesToLayer(ctx context.Context) bool {
	if store.NoLayer(ctx) || layersOff(ctx) || os.Getenv(EnvLayer) == envLayerOff {
		return false
	}
	if l.code.Detached {
		d, err := l.decide(ctx)
		return err != nil || d.Step != branchlayer.StepOff
	}
	iso, err := l.isolated(ctx)
	return err != nil || iso
}
