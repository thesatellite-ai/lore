package wire

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitcommit"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// The App is the store operations' Manager (ops/stores): it edits
// .lore/stores.yaml and ~/.lore/config.yaml and makes store markers.
var _ opsstores.Manager = (*App)(nil)

// Names wire picks for stores it creates.
const (
	// personalStoresDir holds the default personal store
	// (<home>/stores/_me, owner note O7 and item 17: personalFolderDir).
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
	if raw == "" && in.Name != opsstores.PersonalName {
		return opsstores.InitOut{}, opsstores.MissingInitURL(in.Name) // BATCH2 #9, also for callers past the op
	}
	if raw == "" {
		raw = fileuri.URL(personalFolderDir(a.opts.Home), "")
	}
	raw, err := cleanFileURL(raw)
	if err != nil {
		return opsstores.InitOut{}, err
	}
	if in.Name != opsstores.PersonalName { // brief 3.7: stores.yaml and the org entry are shared
		if raw, err = a.canonicalShared(ctx, raw); err != nil {
			return opsstores.InitOut{}, err
		}
	}
	// noRepo: a caller that may run outside a repo (the team step), and no
	// repo here or the repo attaches this very store under this name
	// already: the repo-only checks and the .lore/stores.yaml entry are
	// skipped, everything about the store itself still runs.
	noRepo := false
	if in.Name != opsstores.PersonalName {
		top, terr := a.repoTop(ctx)
		switch {
		case in.RepoOptional && errs.Has(terr, errs.NotAStore):
			noRepo = true
		case terr == nil:
			// The repo attaches this name already (a team re-made after its
			// setup was purged, purge never touching a repo; `lore store
			// init` run twice): the same store under the same name is
			// reused, a no-op attach; another store under that name is
			// refused with the command that drops the stale entry
			// (coordinator decision 2026-11-08).
			old, found, err := attachedAs(top, in.Name)
			if err != nil {
				return opsstores.InitOut{}, err
			}
			if found {
				if !samePlace(old, attach.Store{URL: raw}) {
					return opsstores.InitOut{}, errs.New(errs.InvalidInput, "store: this repo's "+attach.FileName+" already attaches "+in.Name+" to "+old.URL+", not "+raw).
						WithHint("drop the old entry with `lore store remove "+in.Name+"` (its rows stay in that store), then save again; or pick another team name").
						WithNext("lore", "store", "remove", in.Name)
				}
				noRepo = true
			}
		}
	}
	if in.Name != opsstores.PersonalName && !noRepo {
		if err = a.checkUnused(ctx, in.Name, attach.Store{URL: raw}); err != nil {
			return opsstores.InitOut{}, err
		}
	}
	canon, err := canonical(attach.Store{URL: raw})
	if err != nil {
		return opsstores.InitOut{}, err
	}
	if in.Name != opsstores.PersonalName && !noRepo { // after canonical: a credential in the url is refused first
		if err = a.checkNewPlace(ctx, attach.Store{URL: raw}); err != nil {
			return opsstores.InitOut{}, err
		}
	}
	vis := rowfs.Visibility(in.Visibility)
	if vis == "" {
		vis = rowfs.VisibilityInternal
	}
	id, err := ids.New[ids.Store]()
	if err != nil {
		return opsstores.InitOut{}, errs.Wrap(errs.Internal, "store init: new store id", err)
	}
	m := rowfs.Marker{ID: id, Visibility: vis, Created: a.opts.Wall().UTC().Format(markerDateLayout)}
	got, already, err := a.makeStore(ctx, canon, raw, m)
	if err != nil {
		return opsstores.InitOut{}, err
	}
	out := opsstores.InitOut{Name: in.Name, URL: raw, Already: already}
	if !got.ID.IsZero() {
		out.ID = got.ID.String()
	}
	if in.Name == opsstores.PersonalName {
		out.Personal = true
		out.Following, err = setPersonalStoreFollowed(a.opts.Home, raw)
		return out, err
	}
	if noRepo {
		return out, nil
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		return opsstores.InitOut{}, err
	}
	return out, addStoresEntry(top, attach.Store{Name: store.Source(in.Name), URL: raw})
}

// attachedAs is the entry of the repo at top's .lore/stores.yaml named
// name; found is false when there is none.
func attachedAs(top, name string) (attach.Store, bool, error) {
	cfg, err := readStoresFile(top)
	if err != nil {
		return attach.Store{}, false, err
	}
	for _, s := range cfg.Stores {
		if string(s.Name) == name {
			return s, true, nil
		}
	}
	return attach.Store{}, false, nil
}

// InitStoreMarker makes the store at raw a lore store (its marker, the
// same as store init) without naming it anywhere: no personal.store, no
// .lore/stores.yaml entry. A setup's own personal_store url uses it, since
// the setup entry is what points at that store (realmac finding 13: the
// page promised "Save will run lore store init" and nothing ran).
func (a *App) InitStoreMarker(ctx context.Context, raw string) (bool, error) {
	raw, err := cleanFileURL(raw)
	if err != nil {
		return false, err
	}
	canon, err := canonical(attach.Store{URL: raw})
	if err != nil {
		return false, err
	}
	id, err := ids.New[ids.Store]()
	if err != nil {
		return false, errs.Wrap(errs.Internal, "store init: new store id", err)
	}
	m := rowfs.Marker{ID: id, Visibility: rowfs.VisibilityInternal, Created: a.opts.Wall().UTC().Format(markerDateLayout)}
	_, already, err := a.makeStore(ctx, canon, raw, m)
	return already, err
}

// makeStore writes m as the marker of the store at canon unless it already
// has one; it returns the marker in force and whether it was already there.
func (a *App) makeStore(ctx context.Context, canon, raw string, m rowfs.Marker) (rowfs.Marker, bool, error) {
	u, err := url.Parse(canon)
	if err != nil {
		return rowfs.Marker{}, false, errs.New(errs.StoreScheme, "store init: the url does not parse")
	}
	if u.Scheme == folder.Scheme {
		dir := fileuri.OSPath(u.Path)
		if old, rerr := rowfs.ReadMarker(dir); rerr == nil {
			return old, true, nil
		} else if !errs.Has(rerr, errs.NotAStore) {
			if !errors.Is(rerr, fs.ErrNotExist) {
				return rowfs.Marker{}, false, rerr
			}
		}
		return m, false, folder.Init(dir, m, a.opts.rowfsOptions()...)
	}
	b, err := a.openStore(ctx, canon, raw)
	if err != nil {
		return rowfs.Marker{}, false, err
	}
	defer func() { _ = b.Close() }() // read-mostly handle; the commit below already landed
	gb, ok := b.(*git.Backend)
	if !ok {
		return rowfs.Marker{}, false, errs.New(errs.StoreScheme, "store init: not a git store")
	}
	if old := gb.Marker(); old.Version > 0 {
		// B3-29: the marker is the clone's; judge the remote's own content.
		// A remote deleted and made again (empty, or another history) is
		// not "already a lore store": this clone of the old one is stale.
		reset, rerr := gb.RemoteReset(ctx)
		if rerr != nil {
			return rowfs.Marker{}, false, rerr
		}
		if reset {
			shown := raw
			return rowfs.Marker{}, false, errs.New(errs.Refused, "store init: "+shown+" was deleted and made again, but this machine's clone "+gb.Dir()+" still holds the old store ("+old.ID.String()+"); nothing was changed").
				WithHint("start fresh: lore store forget " + shown + ", then run this command again; or put the old store back: lore store add <name> " + shown + ", then lore store push <name> --restore")
		}
		return old, true, nil
	}
	if err = rowfs.WriteMarker(gb.Dir(), m); err != nil {
		return rowfs.Marker{}, false, err
	}
	files := []string{rowfs.MarkerFile}
	if readme, rerr := readmeForCommit(ctx, gb.Dir()); rerr != nil {
		return rowfs.Marker{}, false, rerr
	} else if readme != "" {
		files = append(files, readme)
	}
	if err = gitCommitFilesAs(ctx, gb.Dir(), a.opts.Wall(), storeIdentityFunc(a.opts), initSubject, files...); err != nil {
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

// readmeForCommit writes lore's README.md into the git store clone dir
// when the store has none (not in its files, not in its last commit: a
// person who removed it from the files only keeps that change) and
// returns its path for the commit lore is about to make there; "" when
// nothing was written (D-f: once, with lore's next commit, never on a read).
// A README that is an earlier lore's text, byte for byte, is replaced
// (rowfs.ReadmeOutdated).
func readmeForCommit(ctx context.Context, dir string) (string, error) {
	if rowfs.HasReadme(dir) && !rowfs.ReadmeOutdated(dir) {
		return "", nil
	}
	if rowfs.ReadmeOutdated(dir) {
		if _, err := rowfs.EnsureReadme(dir); err != nil {
			return "", err
		}
		return rowfs.ReadmeFile, nil
	}
	if _, err := (gitRun{dir: dir, args: []string{"cat-file", "-e", "HEAD:" + rowfs.ReadmeFile}}).output(ctx); err == nil {
		return "", nil
	}
	wrote, err := rowfs.EnsureReadme(dir)
	if err != nil || !wrote {
		return "", err
	}
	return rowfs.ReadmeFile, nil
}

// gitCommitFilesAs commits paths (slash-separated, relative to dir) of a
// git store's worktree, hooks off, signing off. Q-R2-1: when the clone's
// origin is a store of an org with an identity, that org identity signs
// it, whatever git knows. Otherwise FLAG4: the person's git identity, when
// git has one, signs the commit; only without it is who's lore identity
// passed (as GIT_AUTHOR_* / GIT_COMMITTER_* on these git calls, never into
// git config).
func gitCommitFilesAs(ctx context.Context, dir string, now time.Time, who storeSigner, subject string, paths ...string) error {
	ident, err := who.envFor(ctx, dir)
	if err != nil {
		return err
	}
	env := []string{
		"GIT_AUTHOR_DATE=" + now.UTC().Format(time.RFC3339), "GIT_COMMITTER_DATE=" + now.UTC().Format(time.RFC3339)}
	env = append(env, ident...)
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

// OrgSigner is one configured org that has an identity (Options.OrgSigners):
// the urls of its stores, as ~/.lore/config.yaml spells them, and the
// signature every commit to them carries (Q-R2-1, LORE_TEAM_SPEC 6.5).
type OrgSigner struct {
	// StoreURLs are the org's store urls; compared after canonicalising, so
	// any spelling of the same remote matches.
	StoreURLs []string
	// Name and Email are the org identity's commit author.
	Name  string
	Email string
}

// OrgStoreURL is one store url of a configured org (Options.OrgStoreURLs),
// as ~/.lore/config.yaml spells it; Org is the entry's name.
type OrgStoreURL struct {
	Org string
	URL string
}

// storeSigner decides who signs a store commit lore makes: the org
// identity for an org store (Q-R2-1), else the FLAG4 rule with the lore
// identity as git's fallback.
type storeSigner struct{ opts Options }

// storeIdentityFunc is the signer of store commits for opts (storeSigner).
func storeIdentityFunc(opts Options) storeSigner { return storeSigner{opts: opts} }

// fallback answers the lore identity (`lore whoami`: display name + the
// identity key's e-mail) as a commit signature: always the PERSON's, the
// owner of a detected agent rather than "<person> via <tool>
// <agent.auto/…@lore.invalid>" (owner bug 12: that went into a store
// clone's .git/config and doctor then demanded it); the agent goes into
// the Lore-Agent trailer (agentTrailers). FLAG4: it is used only
// when git has no user.name/user.email for the clone, an existing git
// identity always wins, and git config is never changed (it is passed as
// GIT_AUTHOR_*/GIT_COMMITTER_* on that one call). It resolves lazily, so a
// machine where git knows the person never pays for the resolution; a
// failed resolution leaves git.DefaultAuthor rather than failing the write.
func (s storeSigner) fallback(ctx context.Context) git.Signature {
	who, err := resolveIdentity(ctx, s.opts)
	if err != nil {
		return git.Signature{}
	}
	name, email := who.Person().CommitSignature()
	return git.Signature{Name: name, Email: email}
}

// agentTrailers is the Lore-Agent trailer of lore's own commits when an
// agent writes for its person (none for the person, the CI actor, or an
// unresolvable identity). Resolved once, at the first commit.
func (s storeSigner) agentTrailers() func() []string {
	var once sync.Once
	var out []string
	return func() []string {
		once.Do(func() {
			who, err := resolveIdentity(context.Background(), s.opts)
			if err != nil || who.Agent == "" {
				return
			}
			out = []string{gitcommit.AgentTrailer(who.Label, who.Agent)}
		})
		return out
	}
}

// LoreSignature is the lore identity as a commit signature, the same
// FLAG4 fallback every core store commit uses (`lore whoami`: display name
// + the identity key's e-mail; storeSigner.fallback). The old binary's
// storeSignature calls it so both paths name the same signer (r3-sign).
// ok is false when the identity cannot be resolved.
func (a *App) LoreSignature(ctx context.Context) (name, email string, ok bool) {
	sig := storeIdentityFunc(a.opts).fallback(ctx)
	if sig.Name == "" && sig.Email == "" {
		return "", "", false
	}
	return sig.Name, sig.Email, true
}

// owner is the org identity that signs the store at raw (any spelling of
// its url, folder and branch ignored), when raw is a store of an org with
// an identity (Q-R2-1). A failing or absent Options.OrgSigners hook names
// no owner: the FLAG4 rule then signs, because refusing every store write
// over an unreadable config would be worse than the old behaviour.
func (s storeSigner) owner(ctx context.Context, raw string) (git.Signature, bool) {
	if s.opts.OrgSigners == nil {
		return git.Signature{}, false
	}
	key, ok := storeKey(raw)
	if !ok {
		return git.Signature{}, false
	}
	orgs, err := s.opts.OrgSigners(ctx)
	if err != nil {
		return git.Signature{}, false
	}
	for _, o := range orgs {
		for _, u := range o.StoreURLs {
			if k, ok := storeKey(u); ok && k == key {
				return git.Signature{Name: o.Name, Email: o.Email}, true
			}
		}
	}
	return git.Signature{}, false
}

// envFor is the identity environment of a commit lore makes in the store
// worktree dir, the backend's rule (adapters/backend/git identity.go): in
// an org store the org identity, unless dir's git identity uses the same
// email (git signs, as for a hand edit); else git.IdentityEnv's FLAG4.
func (s storeSigner) envFor(ctx context.Context, dir string) ([]string, error) {
	origin := ""
	if out, err := (gitRun{dir: dir, args: []string{"config", "--get", "remote.origin.url"}}).output(ctx); err == nil {
		origin = strings.TrimSpace(string(out))
		if sig, ok := s.owner(ctx, origin); ok {
			if env, ok := git.SignerEnvFor(ctx, dir, sig); ok {
				return env, nil
			}
		}
	}
	// a personal store falls back to its owning setup's identity (item 39)
	return git.IdentityEnv(ctx, dir, func(ctx context.Context) git.Signature { return s.identityFor(ctx, origin) })
}

// storeKey is the key every spelling of one store remote shares, its
// folder and branch query ignored: for a git url the remote's host (lower
// case) and repository path without slashes at either end or a .git
// suffix (git.Remote.Norm), so `git@host:org/notes.git`,
// `https://host/org/notes` and `https://host/org/notes/` are one store; for
// a file:// url its path without a trailing slash. canonical() keeps those
// differences (clone folders and locations are keyed on it), so two urls
// are compared as one store only through this key.
func storeKey(raw string) (string, bool) {
	c, err := canonical(attach.Store{URL: raw})
	if err != nil {
		return "", false
	}
	u, err := url.Parse(c)
	if err != nil {
		return "", false
	}
	u.RawQuery, u.Fragment = "", ""
	if u.Scheme == folder.Scheme {
		if p := strings.TrimRight(u.Path, "/"); p != "" {
			u.Path = p
		}
		u.RawPath = ""
		return u.String(), true
	}
	r, err := git.RemoteOf(u)
	if err != nil {
		return "", false
	}
	return r.Norm, true
}

// storeOpeners is newOpeners with this App's commit signing: every git
// clone falls back to the lore identity (FLAG4) and an org store is
// signed by its org identity (Q-R2-1).
func storeOpeners(reg *schema.Registry, origins map[string]string, opts Options) (*store.Openers, error) {
	who := storeIdentityFunc(opts)
	// OV6/OV3 (FQ7): the commits lore makes itself in a git store carry
	// Lore-Version and, in CI, Lore-CI-Run. Only store clones get here: the
	// repo store is the folder backend (no commits), and a person's commits
	// are never touched. An empty Version adds no trailer.
	trailers := git.WithTrailers(gitcommit.OwnCommitTrailers(opts.Version, os.Getenv)...)
	route := git.WithRoute(func(ctx context.Context, remote string) ([]string, []string) {
		return cloneRoute(ctx, opts.Home, optsProjectDir(opts), remote)
	})
	// a failed clone/fetch/push names the access rule it went through
	route = git.Join(route, git.WithAccessNote(accessNoteFor(opts.Home)))
	// Store commits are dated by the app clock (the wall clock; LORE_NOW in
	// a test build), so the age a compaction cuts at follows the same time
	// as every row stamp (store push modes, 2026-11-09).
	clock := git.Join()
	if opts.Wall != nil {
		clock = git.WithClock(opts.Wall)
	}
	return newOpeners(reg, origins, git.Join(git.WithIdentity(who.fallback), git.WithCloneIdentity(who.cloneIdentity), cloneIdentitySay(opts.Notice), trailers, git.WithTrailerFunc(who.agentTrailers()), route, clock, handGitOption(opts.Notice)), who.owner, who.identityFor, opts.Notice)
}

// openStore opens the store at a canonical url with this App's backends.
// raw is the url as the person wrote it (canonical's input): a git clone's
// origin keeps that spelling (originsOf).
func (a *App) openStore(ctx context.Context, canon, raw string) (store.Backend, error) {
	reg, err := builtinRegistry()
	if err != nil {
		return nil, err
	}
	o, err := storeOpeners(reg, originsOf(raw), a.opts)
	if err != nil {
		return nil, err
	}
	return o.Open(ctx, canon, a.opts.openOptions())
}

// AddStore implements opsstores.Manager.
func (a *App) AddStore(ctx context.Context, in opsstores.AddIn) (opsstores.Entry, error) {
	// The very same entry attached already (lore store add run twice): a
	// no-op, the entry as it is, instead of "the name is already used".
	if top, terr := a.repoTop(ctx); terr == nil {
		if old, found, err := attachedAs(top, in.Name); err == nil && found && old.ReadOnly == in.ReadOnly && samePlace(old, attach.Store{URL: in.URL, Folder: in.Folder, Branch: in.Branch}) {
			return opsstores.Entry{Name: string(old.Name), URL: old.URL, Folder: old.Folder, ReadOnly: old.ReadOnly, Branch: old.Branch}, nil
		}
	}
	if err := a.checkUnused(ctx, in.Name, attach.Store{URL: in.URL, Folder: in.Folder, Branch: in.Branch}); err != nil {
		return opsstores.Entry{}, err
	}
	raw, err := cleanFileURL(in.URL)
	if err != nil {
		return opsstores.Entry{}, err
	}
	if raw, err = a.canonicalShared(ctx, raw); err != nil { // brief 3.7: stores.yaml is shared
		return opsstores.Entry{}, err
	}
	in.URL = raw
	s := attach.Store{Name: store.Source(in.Name), URL: in.URL, Folder: in.Folder, ReadOnly: in.ReadOnly, Branch: in.Branch}
	canon, err := canonical(s)
	if err != nil {
		return opsstores.Entry{}, err
	}
	if err = a.checkNewPlace(ctx, s); err != nil { // after canonical: a credential in the url is refused first
		return opsstores.Entry{}, err
	}
	b, err := a.openStore(ctx, canon, s.URL)
	if err != nil {
		return opsstores.Entry{}, err
	}
	m, _ := b.(markerOf)
	_ = b.Close() // only opened to check it is a store
	if m == nil || m.Marker().Version == 0 {
		if in.Branch != "" && a.isStoreAt(ctx, attach.Store{Name: s.Name, URL: s.URL}) {
			// the default branch is a store: the pinned branch is what is
			// missing (D3), not the marker (G-CORE-5)
			return opsstores.Entry{}, errs.New(errs.NotFound, "store add: the store has no branch "+in.Branch+" (its default branch is a lore store)").
				WithHint("create the branch in the store repo from its default branch (git push origin <default>:" + in.Branch + "), then attach it")
		}
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

// isStoreAt reports whether s (its default branch) opens as a 0.1.18
// store; any error is false (the caller keeps its own refusal).
func (a *App) isStoreAt(ctx context.Context, s attach.Store) bool {
	canon, err := canonical(s)
	if err != nil {
		return false
	}
	b, err := a.openStore(ctx, canon, s.URL)
	if err != nil {
		return false
	}
	m, _ := b.(markerOf)
	_ = b.Close()
	return m != nil && m.Marker().Version != 0
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
// Outside any repo it lists the personal store only (stores.yaml is a
// repo's); store.list adds the org's stores (OrgStoreEntries).
func (a *App) ListStores(ctx context.Context) ([]opsstores.Entry, error) {
	top, err := a.storeRepoTop(ctx)
	if err != nil {
		return nil, err
	}
	var cfg attach.Config
	if top != "" {
		if cfg, err = readStoresFile(top); err != nil {
			return nil, err
		}
	}
	var out []opsstores.Entry
	for _, s := range cfg.Stores {
		out = append(out, opsstores.Entry{Name: string(s.Name), URL: s.URL, Folder: s.Folder, ReadOnly: s.ReadOnly, Branch: s.Branch, Source: opsstores.SourceStoresFile})
	}
	mc, err := readMachineConfig(a.opts.Home)
	if err != nil {
		return nil, err
	}
	if p := effectivePersonalURL(ctx, a.opts.Home, a.projectDir(), mc); p != "" { // ONBOARDING 3.4
		out = append(out, opsstores.Entry{Name: opsstores.PersonalName, URL: p, Personal: true, Source: opsstores.SourcePersonal})
	}
	return out, nil
}

var _ opsstores.OrgLister = (*App)(nil)

// OrgStoreEntries implements opsstores.OrgLister: the stores the repo's
// org brings (Options.OrgStores, with stores.yaml clashes renamed as the
// open does), real urls (G-CORE-8).
func (a *App) OrgStoreEntries(ctx context.Context) ([]opsstores.Entry, error) {
	top, err := a.storeRepoTop(ctx) // "" outside a repo: the setup's org stores as they are
	if err != nil {
		return nil, err
	}
	var out []opsstores.Entry
	for _, s := range orgStores(ctx, a.opts, top) {
		out = append(out, opsstores.Entry{Name: string(s.Name), URL: s.URL, Folder: s.Folder, ReadOnly: s.ReadOnly, Branch: s.Branch, Source: opsstores.SourceOrg})
	}
	taken := map[string]bool{}
	for _, e := range out {
		taken[e.Name] = true
	}
	if top != "" {
		if c, rerr := readStoresFile(top); rerr == nil {
			for _, s := range c.Stores {
				taken[string(s.Name)] = true
			}
		}
	}
	return append(out, a.otherOrgStores(out, taken)...), nil
}

// otherOrgStores are the stores of the org entries behind got (the org
// folder's store and the linked folder's) that got does not already hold:
// an org entry lists every store (`lore org refresh` takes them from
// lore-org.yaml), and store list and store folder list show each once.
// They attach no folder here, so each is named by its short name
// (storeshortname.go; owner report 2026-11-11: only the org's first store
// was listed), or by its url when the short name is a name taken here
// (a stores.yaml entry, an attached org name, or another store's).
func (a *App) otherOrgStores(got []opsstores.Entry, taken map[string]bool) []opsstores.Entry {
	orgs := map[string]bool{}
	seen := map[string]bool{}
	for _, e := range got {
		org, _, _ := strings.Cut(e.Name, targetSep)
		orgs[org] = true
		if k, ok := storeKey(e.URL); ok {
			seen[k] = true
		}
	}
	if len(orgs) == 0 {
		return nil
	}
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return nil // the attached ones are listed; config errors are doctor's
	}
	var out []opsstores.Entry
	for _, o := range cfg.Orgs {
		if !orgs[o.Name] {
			continue
		}
		for _, sc := range o.Stores {
			k, ok := storeKey(sc.URL)
			if !ok || seen[k] {
				continue
			}
			seen[k] = true
			name := sc.ShortName()
			if name == "" || taken[name] || slices.Contains(attach.Reserved(), name) {
				name = sc.URL
			}
			taken[name] = true
			out = append(out, opsstores.Entry{Name: name, URL: sc.URL, Branch: sc.Branch, Source: opsstores.SourceOrg})
		}
	}
	return out
}

// checkUnused refuses a name another store of this repo already uses (CFG2).
// place is the store the name is about to stand for: an org store of the
// same name at the same place (url, branch and folder) is not a clash, it is
// the org's own store being made or attached here by its org name, the way
// the team docs make it (`lore store init <org> <org store url>` in the
// org's own repo; renameOrgClashes leaves such an entry as is).
func (a *App) checkUnused(ctx context.Context, name string, place attach.Store) error {
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
			return errs.New(errs.InvalidInput, "store: the name "+name+" is already used in "+attach.FileName+" (for "+s.URL+")").
				WithHint("pick another name, or drop that entry with `lore store remove "+name+"` (its rows stay in that store)").
				WithNext("lore", "store", "remove", name)
		}
	}
	// CLI_REFERENCE 3.21 <name>: a name a store of the repo's org already
	// answers is taken too (G-CORE-8), else `--to <name>` would mean two
	// places.
	for _, s := range rawOrgStores(ctx, a.opts) {
		if string(s.Name) == name && !samePlace(s, place) {
			return errs.New(errs.InvalidInput, "store: the name "+name+" is already used by a store of this repo's org").
				WithHint("pick another name; `lore store list` shows every name in use")
		}
	}
	return nil
}

// samePlace reports whether two store entries name the same place: the
// same repo (canonical url, or one remote spelled two ways git accepts:
// https, ssh, scp, with or without .git, as repourl.Normalize reads them;
// the clone of both is the same folder), the same branch, and the same
// folder (the store root spelled "" or rowfs.FolderRoot).
func samePlace(a, b attach.Store) bool {
	if rootFolder(a.Folder) != rootFolder(b.Folder) {
		return false
	}
	if sameURL(a, b) {
		return true
	}
	na, ea := repourl.NormalizeStore(a.URL)
	nb, eb := repourl.NormalizeStore(b.URL)
	return ea == nil && eb == nil && na == nb && a.Branch == b.Branch
}

// rootFolder is a folder with its slashes trimmed, the store root as "".
func rootFolder(f string) string {
	f = strings.Trim(f, "/")
	if f == rowfs.FolderRoot {
		return ""
	}
	return f
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
	return rowfs.HasStoreFolder(dir, clean), nil
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
		return fileuri.OSPath(u.Path), nil
	}
	b, err := a.openStore(ctx, canon, src.URL)
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

// homeTilde is the shell's home shorthand, accepted at the start of a
// file:// url's path.
const homeTilde = "~"

// cleanFileURL writes a file:// url in its one canonical form (absolute,
// clean path) so the folder backend accepts it and two spellings of one
// folder name the same store. Every url a person adds passes here (store
// init, add, set-url), so it is also the add-time credential check: a url
// holding a password or a token-shaped user is refused (E_URL_USERINFO,
// attach.NormalizeURL; the url never appears in the error), because the
// value would be written into the committed stores.yaml; any other https
// user part is dropped (G-CORE-6). Other urls pass unchanged.
func cleanFileURL(raw string) (string, error) {
	if isPlainLocalPath(raw) {
		return localPathURL(raw) // a git repository here, or a folder (storelocalpath.go)
	}
	if !strings.HasPrefix(raw, folder.Scheme+"://") {
		if _, err := attach.NormalizeURL(raw); errs.Has(err, errs.URLUserinfo) {
			return "", err
		}
		// G-CORE-6: a user part that is not refused (an https user name,
		// which git hosts treat as a token) is dropped before the url is
		// written into the committed stores.yaml; read time strips it
		// anyway (attach.CleanURL), so the written url is the one used.
		// An ssh login user (git@) is kept, as Strip keeps it.
		if clean, _, serr := repourl.Strip(raw); serr == nil {
			return clean, nil
		}
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errs.New(errs.StoreScheme, "store: a file url needs an absolute path (file:///abs/path)")
	}
	// file://C:/x names the drive path (fileuri.FoldDriveHost); kept as a
	// host, the cleaned url came out as file://C:/C:/x.
	fileuri.FoldDriveHost(u)
	// `file://~/stores/x` (host "~") and `file:///~/stores/x` name a folder
	// under the user's home, as a shell would read it (O7, docs examples).
	if u.Host == homeTilde || u.Path == "/"+homeTilde || strings.HasPrefix(u.Path, "/"+homeTilde+"/") {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", errs.Wrap(errs.BadPath, "store: ~ in a file url needs a home folder", herr)
		}
		rest := u.Path
		if u.Host != homeTilde {
			rest = strings.TrimPrefix(rest, "/"+homeTilde)
		}
		u.Host, u.Path = "", filepath.ToSlash(home)+rest
	}
	if u.Path == "" {
		return "", errs.New(errs.StoreScheme, "store: a file url needs an absolute path (file:///abs/path)")
	}
	if fileuri.IsDriveURLPath(u.Path) && runtime.GOOS != windowsGOOS {
		// B3-28: file:///C:/… names a Windows drive; here it is no folder
		// lore can make (it failed as E_INTERNAL "create the store folder")
		return "", missingPathError(u.Path[1:])
	}
	p, err := filepath.Abs(fileuri.OSPath(u.Path))
	if err != nil {
		return "", errs.Wrap(errs.BadPath, "store: the file url's path", err)
	}
	u.Path = fileuri.URLPath(filepath.Clean(p))
	// A file:// url of a git repository is that repository as a git
	// store, as `git clone file:///srv/x.git` reads it (storelocalpath.go).
	if repo, ok := gitRepoOfFileURL(u.String()); ok {
		return repo, nil
	}
	return u.String(), nil
}

// Commit subjects of the store control-file commits below.
const (
	visibilitySubject = "lore: store visibility "
	folderAddSubject  = "lore: store folder add "
)

// findStore is the store a store command that writes it names
// (store.visibility, store.folder.add, store.set): lookupStore, and a
// read-only attachment is E_STORE_READ_ONLY.
func (a *App) findStore(ctx context.Context, verb, name string) (attach.Store, error) {
	src, err := a.lookupStore(ctx, verb, name)
	if err != nil {
		return attach.Store{}, err
	}
	if src.ReadOnly {
		return attach.Store{}, errs.New(errs.StoreReadOnly, verb+": store "+name+" is attached read-only")
	}
	return src, nil
}

// withStoreDir runs edit on the local folder of src's store and saves
// what it wrote: a file:// store is the folder itself (nothing more to
// do); a git store's edit runs in the lore-owned worktree, and the paths
// it returns are committed (gitCommitFilesAs: org identity or FLAG4) and
// pushed in one sync. edit returning no paths commits nothing.
func (a *App) withStoreDir(ctx context.Context, src attach.Store, subject string, edit func(dir string) ([]string, error)) error {
	canon, err := canonical(attach.Store{URL: src.URL, Branch: src.Branch})
	if err != nil {
		return err
	}
	u, err := url.Parse(canon)
	if err != nil {
		return errs.New(errs.StoreScheme, "wire: the store url does not parse")
	}
	if u.Scheme == folder.Scheme {
		_, err = edit(fileuri.OSPath(u.Path))
		return err
	}
	b, err := a.openStore(ctx, canon, src.URL)
	if err != nil {
		return err
	}
	defer func() { _ = b.Close() }() // the commit below already landed
	gb, ok := b.(*git.Backend)
	if !ok {
		return errs.New(errs.StoreScheme, "wire: the store has no local folder")
	}
	// A brand-new store (no marker yet) gets the marker `lore store init`
	// writes in the same commit as this first write, before the edit, so
	// what the edit derives from the store id (a folder's natural prj_ id)
	// uses it (owner report 2026-11-11: a folder pushed to an empty org
	// store without it made every later command refuse the store).
	newMarker := gb.Marker().Version == 0
	if newMarker {
		m, merr := rowfs.NewMarker(a.opts.Wall())
		if merr != nil {
			return merr
		}
		if merr = rowfs.WriteMarker(gb.Dir(), m); merr != nil {
			return merr
		}
	}
	paths, err := edit(gb.Dir())
	if newMarker && (err != nil || len(paths) == 0) {
		_ = os.Remove(filepath.Join(gb.Dir(), rowfs.MarkerFile)) // nothing to commit it with
	}
	if err != nil || len(paths) == 0 {
		return err
	}
	if newMarker && !slices.Contains(paths, rowfs.MarkerFile) {
		paths = append([]string{rowfs.MarkerFile}, paths...)
	}
	if readme, rerr := readmeForCommit(ctx, gb.Dir()); rerr != nil {
		return rerr
	} else if readme != "" && !slices.Contains(paths, readme) {
		paths = append(paths, readme)
	}
	if err = gitCommitFilesAs(ctx, gb.Dir(), a.opts.Wall(), storeIdentityFunc(a.opts), subject, paths...); err != nil {
		return err
	}
	rep, err := gb.Sync(ctx)
	if err != nil {
		return err
	}
	if len(rep.Refused) > 0 {
		return errs.New(errs.PolicyDenied, subject+": the remote refused the commit: "+rep.Refused[0].Reason)
	}
	if !rep.Offline {
		// the commit went out: it is this clone's last push (`lore store
		// show` pushed), as a row batch's or a compaction's is
		writeStamp(git.StateDirOf(gb.Dir()), pushStampFile, a.opts.Wall())
	}
	return nil
}

// StoreVisibility implements opsstores.Manager (CFG4).
func (a *App) StoreVisibility(ctx context.Context, name string) (opsstores.VisibilityOut, error) {
	src, err := a.findStore(ctx, "store visibility", name)
	if err != nil {
		return opsstores.VisibilityOut{}, err
	}
	dir, err := a.storeDir(ctx, src)
	if err != nil {
		return opsstores.VisibilityOut{}, err
	}
	m, err := rowfs.ReadMarker(dir)
	if errs.Has(err, errs.NotAStore) && !strings.HasPrefix(src.URL, folderURLScheme) {
		// a configured git store nothing has written to yet (a brand-new
		// org store): it has the visibility its first write gives it
		v := string(rowfs.VisibilityInternal)
		return opsstores.VisibilityOut{Name: name, Visibility: v, Previous: v}, nil
	}
	if err != nil {
		return opsstores.VisibilityOut{}, err
	}
	return opsstores.VisibilityOut{Name: name, Visibility: string(m.Visibility), Previous: string(m.Visibility)}, nil
}

// SetVisibility implements opsstores.Manager: it rewrites the store's
// lore-store.yaml with the new visibility (a store setting: every repo
// that uses the store sees it, CFG4). A v0.1.17 marker without an id is
// refused: `lore migrate run` gives the store its sto_ id first.
func (a *App) SetVisibility(ctx context.Context, in opsstores.VisibilityIn) (opsstores.VisibilityOut, error) {
	src, err := a.findStore(ctx, "store visibility", in.Name)
	if err != nil {
		return opsstores.VisibilityOut{}, err
	}
	out := opsstores.VisibilityOut{Name: in.Name, Visibility: in.Visibility}
	err = a.withStoreDir(ctx, src, visibilitySubject+in.Visibility, func(dir string) ([]string, error) {
		m, rerr := rowfs.ReadMarker(dir)
		if rerr != nil {
			return nil, rerr
		}
		out.Previous = string(m.Visibility)
		if m.Visibility == rowfs.Visibility(in.Visibility) {
			return nil, nil
		}
		if m.ID.IsZero() {
			return nil, errs.New(errs.OldLore, "store visibility: "+in.Name+" still has a v0.1.17 "+rowfs.MarkerFile+" (no store id)").
				WithHint("run lore fix first")
		}
		m.Visibility = rowfs.Visibility(in.Visibility)
		if werr := rowfs.WriteMarker(dir, m); werr != nil {
			return nil, werr
		}
		out.Changed = true
		return []string{rowfs.MarkerFile}, nil
	})
	if err != nil {
		return opsstores.VisibilityOut{}, err
	}
	return out, nil
}

// AddFolder implements opsstores.Manager (STORE TARGETS): the folder's
// _meta.json is written as its first row write would write it (natural
// prj_ id from the store id and the folder name, so two clones agree),
// under the store lock, then committed and pushed.
func (a *App) AddFolder(ctx context.Context, in opsstores.FolderAddIn) (opsstores.FolderAddOut, error) {
	src, err := a.findStore(ctx, "store folder add", in.Store)
	if err != nil {
		return opsstores.FolderAddOut{}, err
	}
	// team and an org store's url are shown as the store they resolved to
	out := opsstores.FolderAddOut{Store: a.storeLabel(in.Store, src), Folder: in.Folder}
	err = a.withStoreDir(ctx, src, folderAddSubject+in.Folder, func(dir string) (paths []string, ferr error) {
		root, oerr := rowfs.Open(dir, a.opts.storeRowfsOptions(dir)...)
		if oerr != nil {
			return nil, oerr
		}
		f, oerr := root.Folder(in.Folder)
		if oerr != nil {
			return nil, oerr
		}
		lock, oerr := root.Lock(ctx)
		if oerr != nil {
			return nil, oerr
		}
		defer func() { ferr = errors.Join(ferr, lock.Release()) }()
		if paths, ferr = f.Create(ctx); ferr != nil {
			return nil, ferr
		}
		out.Already = len(paths) == 0
		id, ierr := f.ProjectID(ctx)
		if ierr != nil {
			return nil, ierr
		}
		out.ProjectID = id.String()
		return paths, nil
	})
	if err != nil {
		return opsstores.FolderAddOut{}, err
	}
	return out, nil
}

// personalInitOutsideRepo reports whether e is `lore store init me` run
// outside any git work tree (G-CORE-7): it writes only the personal store
// and ~/.lore/config.yaml (never stores.yaml, never a row through the
// engine), so it runs without the repo-only engine (runDirect) instead of
// failing E_NOT_A_STORE. Every other store operation keeps needing a repo.
func (a *App) personalInitOutsideRepo(ctx context.Context, e op.Entry, d op.Decoder) bool {
	if e.Spec().Name != opsstores.OpInit || insideWorkTree(ctx, a.opts.RepoDir) {
		return false
	}
	in, err := e.Decode(d)
	if err != nil {
		return false // the normal path reports the decode error
	}
	for _, tv := range in.Texts() {
		if tv.Field == storeNameField {
			return tv.Text == opsstores.PersonalName
		}
	}
	return false
}
