package wire

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

// The App is the store operations' Manager (ops/stores): it edits
// .lore/stores.yaml and ~/.lore/config.yaml and makes store markers.
var _ opsstores.Manager = (*App)(nil)

// Names wire picks for stores it creates.
const (
	// personalStoresDir holds the default personal store
	// (<home>/stores/me, owner note O7).
	personalStoresDir = "stores"
	// markerDateLayout is the `created:` date of a new marker.
	markerDateLayout = "2006-01-02"
	// defaultOrgFolder is the company-wide folder a new marker names (CFG1).
	defaultOrgFolder = "org"
	// storesFileMode: stores.yaml is committed, so it is world-readable
	// like any code file.
	storesFileMode = 0o644
	// yaml keys of a stores.yaml entry.
	keyStores   = "stores"
	keyName     = "name"
	keyURL      = "url"
	keyFolder   = "folder"
	keyReadOnly = "read_only"
	keyBranch   = "branch"
	// initSubject is the commit subject of a git store's marker commit.
	initSubject = "lore: store init"
)

// InitStore implements opsstores.Manager.
func (a *App) InitStore(ctx context.Context, in opsstores.InitIn) (opsstores.InitOut, error) {
	raw := in.URL
	if raw == "" {
		dir := filepath.Join(a.opts.Home, personalStoresDir, opsstores.PersonalName)
		raw = (&url.URL{Scheme: folder.Scheme, Path: filepath.ToSlash(dir)}).String()
	}
	raw, err := cleanFileURL(raw)
	if err != nil {
		return opsstores.InitOut{}, err
	}
	if in.Name != opsstores.PersonalName {
		if err = a.checkUnused(ctx, in.Name); err != nil {
			return opsstores.InitOut{}, err
		}
	}
	canon, err := canonical(attach.Store{URL: raw})
	if err != nil {
		return opsstores.InitOut{}, err
	}
	vis := rowfs.Visibility(in.Visibility)
	if vis == "" {
		vis = rowfs.VisibilityInternal
	}
	id, err := ids.New[ids.Store]()
	if err != nil {
		return opsstores.InitOut{}, errs.Wrap(errs.Internal, "store init: new store id", err)
	}
	m := rowfs.Marker{ID: id, Visibility: vis, Created: a.opts.Wall().UTC().Format(markerDateLayout), OrgFolder: defaultOrgFolder}
	got, already, err := a.makeStore(ctx, canon, m)
	if err != nil {
		return opsstores.InitOut{}, err
	}
	out := opsstores.InitOut{Name: in.Name, URL: raw, Already: already}
	if !got.ID.IsZero() {
		out.ID = got.ID.String()
	}
	if in.Name == opsstores.PersonalName {
		out.Personal = true
		return out, setPersonalStore(a.opts.Home, raw)
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		return opsstores.InitOut{}, err
	}
	return out, addStoresEntry(top, attach.Store{Name: store.Source(in.Name), URL: raw})
}

// makeStore writes m as the marker of the store at canon unless it already
// has one; it returns the marker in force and whether it was already there.
func (a *App) makeStore(ctx context.Context, canon string, m rowfs.Marker) (rowfs.Marker, bool, error) {
	u, err := url.Parse(canon)
	if err != nil {
		return rowfs.Marker{}, false, errs.New(errs.StoreScheme, "store init: the url does not parse")
	}
	if u.Scheme == folder.Scheme {
		dir := filepath.FromSlash(u.Path)
		if old, rerr := rowfs.ReadMarker(dir); rerr == nil {
			return old, true, nil
		} else if !errs.Has(rerr, errs.NotAStore) {
			if !errors.Is(rerr, fs.ErrNotExist) {
				return rowfs.Marker{}, false, rerr
			}
		}
		return m, false, folder.Init(dir, m)
	}
	b, err := a.openStore(ctx, canon)
	if err != nil {
		return rowfs.Marker{}, false, err
	}
	defer func() { _ = b.Close() }() // read-mostly handle; the commit below already landed
	gb, ok := b.(*git.Backend)
	if !ok {
		return rowfs.Marker{}, false, errs.New(errs.StoreScheme, "store init: not a git store")
	}
	if old := gb.Marker(); old.Version > 0 {
		return old, true, nil
	}
	if err = rowfs.WriteMarker(gb.Dir(), m); err != nil {
		return rowfs.Marker{}, false, err
	}
	if err = gitCommitMarker(ctx, gb.Dir(), a.opts.Wall()); err != nil {
		return rowfs.Marker{}, false, err
	}
	rep, err := gb.Sync(ctx)
	if err != nil {
		return rowfs.Marker{}, false, err
	}
	if len(rep.Refused) > 0 {
		return rowfs.Marker{}, false, errs.New(errs.PolicyDenied, "store init: the remote refused the marker commit: "+rep.Refused[0].Reason)
	}
	return m, false, nil
}

// gitCommitMarker commits lore-store.yaml in a lore-owned store worktree
// (the git adapter has no "commit a control file" call; this is the one
// non-row commit lore makes, so it lives here beside the marker logic).
func gitCommitMarker(ctx context.Context, dir string, now time.Time) error {
	return gitCommitFiles(ctx, dir, now, initSubject, rowfs.MarkerFile)
}

// gitCommitFiles commits paths (slash-separated, relative to dir) of a git
// store's worktree as lore's own author, hooks off, signing off.
func gitCommitFiles(ctx context.Context, dir string, now time.Time, subject string, paths ...string) error {
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME="+git.DefaultAuthor.Name, "GIT_AUTHOR_EMAIL="+git.DefaultAuthor.Email,
		"GIT_COMMITTER_NAME="+git.DefaultAuthor.Name, "GIT_COMMITTER_EMAIL="+git.DefaultAuthor.Email,
		"GIT_AUTHOR_DATE="+now.UTC().Format(time.RFC3339), "GIT_COMMITTER_DATE="+now.UTC().Format(time.RFC3339))
	for _, args := range [][]string{
		append([]string{"add", "--"}, paths...),
		append([]string{"-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "-m", subject, "--"}, paths...),
	} {
		if _, err := (gitRun{dir: dir, env: env, args: args}).output(ctx); err != nil {
			return errs.Wrap(errs.Internal, "wire: commit "+strings.Join(paths, " "), err)
		}
	}
	return nil
}

// openStore opens the store at a canonical url with this App's backends.
func (a *App) openStore(ctx context.Context, canon string) (store.Backend, error) {
	reg, err := builtinRegistry()
	if err != nil {
		return nil, err
	}
	o, err := newOpeners(reg)
	if err != nil {
		return nil, err
	}
	return o.Open(ctx, canon, store.OpenOptions{Home: a.opts.Home})
}

// AddStore implements opsstores.Manager.
func (a *App) AddStore(ctx context.Context, in opsstores.AddIn) (opsstores.Entry, error) {
	if err := a.checkUnused(ctx, in.Name); err != nil {
		return opsstores.Entry{}, err
	}
	raw, err := cleanFileURL(in.URL)
	if err != nil {
		return opsstores.Entry{}, err
	}
	in.URL = raw
	s := attach.Store{Name: store.Source(in.Name), URL: in.URL, Folder: in.Folder, ReadOnly: in.ReadOnly, Branch: in.Branch}
	canon, err := canonical(s)
	if err != nil {
		return opsstores.Entry{}, err
	}
	b, err := a.openStore(ctx, canon)
	if err != nil {
		return opsstores.Entry{}, err
	}
	m, _ := b.(markerOf)
	_ = b.Close() // only opened to check it is a store
	if m == nil || m.Marker().Version == 0 {
		return opsstores.Entry{}, errs.Wrap(errs.NotAStore, "store add: "+in.Name+" has no "+rowfs.MarkerFile, store.ErrNotAStore).
			WithHint("make it a store first: lore store init " + in.Name + " <url>")
	}
	if in.Folder != "" {
		ok, ferr := a.folderExists(ctx, s, in.Folder)
		if ferr != nil {
			return opsstores.Entry{}, ferr
		}
		if !ok {
			return opsstores.Entry{}, errs.New(errs.NotFound, "store add: the store has no folder "+in.Folder).
				WithHint("write to it once with --to <store>/" + in.Folder + " (lore asks first), then attach it")
		}
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		return opsstores.Entry{}, err
	}
	if err := addStoresEntry(top, s); err != nil {
		return opsstores.Entry{}, err
	}
	return opsstores.Entry{Name: in.Name, URL: in.URL, Folder: in.Folder, ReadOnly: in.ReadOnly, Branch: in.Branch}, nil
}

// RemoveStore implements opsstores.Manager.
func (a *App) RemoveStore(ctx context.Context, name string) (opsstores.Entry, error) {
	top, err := a.repoTop(ctx)
	if err != nil {
		return opsstores.Entry{}, err
	}
	var gone attach.Store
	err = editStoresFile(top, func(list *yaml.Node) error {
		for i, n := range list.Content {
			if v := mapGet(n, keyName); v != nil && v.Value == name {
				if derr := n.Decode(&gone); derr != nil {
					return errs.Wrap(errs.ConfigInvalid, "wire: "+attach.FileName+" entry "+name, derr)
				}
				list.Content = append(list.Content[:i], list.Content[i+1:]...)
				return nil
			}
		}
		return errs.New(errs.NotFound, "store remove: no store named "+name+" in "+attach.FileName).
			WithHint("run `lore store list`")
	})
	if err != nil {
		return opsstores.Entry{}, err
	}
	return opsstores.Entry{Name: name, URL: gone.URL, Folder: gone.Folder, ReadOnly: gone.ReadOnly, Branch: gone.Branch}, nil
}

// ListStores implements opsstores.Manager.
func (a *App) ListStores(ctx context.Context) ([]opsstores.Entry, error) {
	top, err := a.repoTop(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return nil, err
	}
	var out []opsstores.Entry
	for _, s := range cfg.Stores {
		out = append(out, opsstores.Entry{Name: string(s.Name), URL: s.URL, Folder: s.Folder, ReadOnly: s.ReadOnly, Branch: s.Branch})
	}
	mc, err := readMachineConfig(a.opts.Home)
	if err != nil {
		return nil, err
	}
	if p := mc.personalURL(); p != "" {
		out = append(out, opsstores.Entry{Name: opsstores.PersonalName, URL: p, Personal: true})
	}
	return out, nil
}

// checkUnused refuses a name another store of this repo already uses (CFG2).
func (a *App) checkUnused(ctx context.Context, name string) error {
	top, err := a.repoTop(ctx)
	if err != nil {
		return err
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return err
	}
	for _, s := range cfg.Stores {
		if string(s.Name) == name {
			return errs.New(errs.InvalidInput, "store: the name "+name+" is already used in "+attach.FileName).
				WithHint("pick another name, or `lore store remove " + name + "` first")
		}
	}
	return nil
}

// folderExists reports whether the store at raw has the data folder f.
func (a *App) folderExists(ctx context.Context, src attach.Store, f string) (bool, error) {
	dir, err := a.storeDir(ctx, src)
	if err != nil {
		return false, err
	}
	clean, err := rowfs.CleanFolder(f)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(filepath.Join(dir, filepath.FromSlash(rowfs.FolderDataPath(clean))))
	return err == nil, nil
}

// storeDir is the local folder holding a store's files: the path of a
// file:// store, the lore-owned worktree of a git store (of src's pinned
// branch, when it names one).
func (a *App) storeDir(ctx context.Context, src attach.Store) (string, error) {
	canon, err := canonical(attach.Store{URL: src.URL, Branch: src.Branch})
	if err != nil {
		return "", err
	}
	u, err := url.Parse(canon)
	if err != nil {
		return "", errs.New(errs.StoreScheme, "wire: the store url does not parse")
	}
	if u.Scheme == folder.Scheme {
		return filepath.FromSlash(u.Path), nil
	}
	b, err := a.openStore(ctx, canon)
	if err != nil {
		return "", err
	}
	defer func() { _ = b.Close() }() // only the path is needed
	d, ok := b.(interface{ Dir() string })
	if !ok {
		return "", errs.New(errs.StoreScheme, "wire: the store has no local folder")
	}
	return d.Dir(), nil
}

// readStoresFile parses <top>/.lore/stores.yaml (absent = no stores).
func readStoresFile(top string) (attach.Config, error) {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(top, filepath.FromSlash(attach.FileName))))
	if errors.Is(err, fs.ErrNotExist) {
		return attach.Config{}, nil
	}
	if err != nil {
		return attach.Config{}, errs.Wrap(errs.Internal, "wire: read "+attach.FileName, err)
	}
	return attach.Parse(b)
}

// addStoresEntry appends s to stores.yaml (keeping comments and order).
func addStoresEntry(top string, s attach.Store) error {
	return editStoresFile(top, func(list *yaml.Node) error {
		n := &yaml.Node{Kind: yaml.MappingNode}
		mapSet(n, keyName, scalar(string(s.Name)))
		mapSet(n, keyURL, scalar(s.URL))
		if s.Folder != "" {
			mapSet(n, keyFolder, scalar(s.Folder))
		}
		if s.Branch != "" {
			mapSet(n, keyBranch, scalar(s.Branch))
		}
		if s.ReadOnly {
			mapSet(n, keyReadOnly, &yaml.Node{Kind: yaml.ScalarNode, Value: "true", Tag: "!!bool"})
		}
		list.Content = append(list.Content, n)
		return nil
	})
}

// editStoresFile loads stores.yaml as nodes, lets edit change the `stores`
// list, re-validates the result with attach.Parse (a broken file is never
// written) and writes it back atomically.
func editStoresFile(top string, edit func(list *yaml.Node) error) error {
	path := filepath.Join(top, filepath.FromSlash(attach.FileName))
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	b, err := os.ReadFile(filepath.Clean(path))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return errs.Wrap(errs.Internal, "wire: read "+attach.FileName, err)
	default:
		if err = yaml.Unmarshal(b, doc); err != nil {
			return errs.Wrap(errs.ConfigInvalid, "wire: "+attach.FileName+" is not valid YAML", err)
		}
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return errs.New(errs.ConfigInvalid, "wire: "+attach.FileName+" is not a mapping")
	}
	list := mapGet(root, keyStores)
	if list == nil || list.Kind != yaml.SequenceNode {
		list = &yaml.Node{Kind: yaml.SequenceNode}
		mapSet(root, keyStores, list)
	}
	if err = edit(list); err != nil {
		return err
	}
	list.Style = 0 // block style: one entry per line, as people write it (an emptied list was flow `[]`)
	out, err := yaml.Marshal(doc)
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode "+attach.FileName, err)
	}
	if _, err := attach.Parse(out); err != nil {
		return err
	}
	return writeFileAtomic(path, out, storesFileMode)
}

// cleanFileURL writes a file:// url in its one canonical form (absolute,
// clean path) so the folder backend accepts it and two spellings of one
// folder name the same store; other urls pass unchanged.
func cleanFileURL(raw string) (string, error) {
	if !strings.HasPrefix(raw, folder.Scheme+"://") {
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return "", errs.New(errs.StoreScheme, "store: a file url needs an absolute path (file:///abs/path)")
	}
	p, err := filepath.Abs(filepath.FromSlash(u.Path))
	if err != nil {
		return "", errs.Wrap(errs.BadPath, "store: the file url's path", err)
	}
	u.Path = filepath.ToSlash(filepath.Clean(p))
	return u.String(), nil
}
