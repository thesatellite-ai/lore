package wire

// storeforget_standalone.go — `lore store forget <url>` on cmd/lore
// (ops/stores.Cloner, Options.Local.Clones; P3-CMDLORE-9), ported from the
// old binary's store_cmd.go forgetClone + store_forget_core.go: this
// machine's clones of a store (its clone under ~/.lore/stores and its
// branch clones) are deleted, refused while any of them holds unpushed
// commits or uncommitted changes (side effect 20). A person's own clone
// (`lore store use`) is never deleted: lore drops its record and leaves
// the folder.
// store show|open|doctor forward to storeshow_standalone.go.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// branchCloneSep joins a clone folder and its pinned branch
// (adapters/backend/git layout: <repo>@<branch>).
const branchCloneSep = "@"

// standaloneClones implements opsstores.Cloner for cmd/lore.
type standaloneClones struct {
	home string
	// now is the app clock for the clones' fetch stamps (nil = wall clock).
	now func() time.Time
}

var _ opsstores.Cloner = (*standaloneClones)(nil)

// ShowClones implements opsstores.Cloner (storeshow_standalone.go).
func (c *standaloneClones) ShowClones(ctx context.Context, in opsstores.ShowIn) (opsstores.ClonesOut, error) {
	return c.showClones(ctx, in)
}

// OpenClone implements opsstores.Cloner (storeshow_standalone.go).
func (c *standaloneClones) OpenClone(ctx context.Context, in opsstores.OpenIn) (opsstores.OpenOut, error) {
	return c.openClone(ctx, in)
}

// DoctorClones implements opsstores.Cloner (storeshow_standalone.go).
func (c *standaloneClones) DoctorClones(ctx context.Context) (opsstores.ClonesOut, error) {
	return c.doctorClones(ctx)
}

// folderURLScheme starts a folder store's url (the folder backend).
const folderURLScheme = "file://"

// cloneDirs is one clone: its folder and what a forget does with it.
type cloneDirs struct {
	git    string   // the repository to ask about unpushed commits
	work   string   // a checkout to ask about uncommitted changes ("" none)
	remove []string // what the forget deletes (none for an own clone)
	// own is a person's own clone: never removed; key is its record entry.
	own bool
	key string
}

// ForgetClone implements opsstores.Cloner.
func (c *standaloneClones) ForgetClone(ctx context.Context, in opsstores.ForgetIn) (opsstores.ForgetOut, error) {
	if strings.HasPrefix(in.URL, folderURLScheme) {
		// A folder store (file://, the folder backend: `me`, a shared
		// folder) keeps its rows in that folder; this machine holds no
		// clone of it, so there is nothing to forget. It answered
		// "not a store url" for a url `store add` had just accepted.
		return opsstores.ForgetOut{URL: in.URL, Shown: repourl.Mask(in.URL)}, nil
	}
	norm, err := repourl.Normalize(in.URL)
	if err != nil {
		return opsstores.ForgetOut{}, errs.Wrap(errs.InvalidInput, "store forget: not a store url: "+in.URL, err).
			WithHint("give the store's name from `lore store list`, or its git url (https://host/org/repo or git@host:org/repo)")
	}
	shown := repourl.Mask(norm)
	clones, err := c.clonesOf(in.URL, norm)
	if err != nil {
		return opsstores.ForgetOut{}, err
	}
	var pending []string
	for _, cl := range clones {
		pending = append(pending, unpushedIn(ctx, cl)...)
	}
	if len(pending) > 0 {
		return opsstores.ForgetOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("the clone holds unpushed work (%s); forgetting it would lose it", strings.Join(pending, ", "))).
			WithHint("run `lore sync` when the store is reachable, then forget it")
	}
	out := opsstores.ForgetOut{URL: norm, Shown: shown}
	// The store's other copies on this machine (row caches, private
	// renders) hold its notes as readable text; a clone forgotten with them
	// kept left a team's notes on the machine of someone who left it
	// (journeys joiner/month3/leave-job-a, agency offboard). The ids are
	// read before the clones are deleted.
	ids := c.storeIDsOf(ctx, clones)
	for _, cl := range clones {
		if cl.own {
			if ferr := gitbackend.ForgetCloneRecord(c.home, cl.key); ferr != nil {
				return opsstores.ForgetOut{}, ferr
			}
			if out.Kept == "" {
				out.Kept = cl.work
			}
			continue
		}
		for _, d := range cl.remove {
			if rerr := os.RemoveAll(d); rerr != nil {
				return opsstores.ForgetOut{}, errs.Wrap(errs.Internal, "remove the store clone", rerr)
			}
		}
		if cl.key != "" {
			if ferr := gitbackend.ForgetCloneRecord(c.home, cl.key); ferr != nil {
				return opsstores.ForgetOut{}, ferr
			}
		}
		if out.Removed == "" {
			out.Removed = cl.remove[0]
		}
	}
	for _, id := range ids {
		if err := c.forgetCopies(id); err != nil {
			return opsstores.ForgetOut{}, err
		}
	}
	return out, nil
}

// storeIDsOf is the sto_ id of every clone's store (its lore-store.yaml,
// read from the checkout, else from the clone's HEAD): what the store's
// other copies on this machine are named by. A clone with no readable id (a
// v0.1.17 marker) has no id-named copy to remove.
func (c *standaloneClones) storeIDsOf(ctx context.Context, clones []cloneDirs) []string {
	var out []string
	for _, cl := range clones {
		var raw []byte
		if cl.work != "" {
			raw, _ = os.ReadFile(filepath.Join(cl.work, rowfs.MarkerFile))
		}
		if len(raw) == 0 && exists(cl.git) {
			raw, _ = gitRun{dir: cl.git, args: []string{"show", "HEAD:" + rowfs.MarkerFile}}.output(ctx)
		}
		m, err := rowfs.ParseMarker(raw)
		if err != nil || m.ID.IsZero() {
			continue
		}
		if id := m.ID.String(); !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// forgetCopies deletes what this machine still holds of store id once its
// clone is gone: its row caches (~/.lore/cache/stores/<id>) and its private
// renders (~/.lore/render/<repo>/<id>.md, and its line in each index.md).
// A person forgets a store when they lose access to it (they left the
// company, the client ended the contract); its notes must not stay
// readable on the laptop. The next command that attaches the store again
// clones and renders it afresh.
func (c *standaloneClones) forgetCopies(id string) error {
	if err := os.RemoveAll(filepath.Join(c.home, cacheDir, cacheStoresDir, id)); err != nil {
		return errs.Wrap(errs.Internal, "remove the store's caches", err)
	}
	repos, err := filepath.Glob(filepath.Join(c.home, renderDirName, "*"))
	if err != nil {
		return errs.Wrap(errs.Internal, "list the private renders", err)
	}
	for _, dir := range repos {
		if err := os.Remove(filepath.Join(dir, id+privateRenderExt)); err != nil && !os.IsNotExist(err) {
			return errs.Wrap(errs.Internal, "remove the store's private render", err)
		}
		idx := filepath.Join(dir, privateRenderIndex)
		b, err := os.ReadFile(idx)
		if err != nil {
			continue
		}
		var keep []string
		for _, l := range strings.SplitAfter(string(b), "\n") {
			if !strings.HasPrefix(l, id+privateRenderExt+" ") {
				keep = append(keep, l)
			}
		}
		if kept := strings.Join(keep, ""); kept != string(b) {
			if err := os.WriteFile(idx, []byte(kept), privateRenderFileMode); err != nil {
				return errs.Wrap(errs.Internal, "update the private render index", err)
			}
		}
	}
	return nil
}

// Private render names (wire/renderhost/render_private.go's layout:
// ~/.lore/render/<repo id>/<store id>.md and index.md, owner-only files).
const (
	renderDirName         = "render"
	privateRenderExt      = ".md"
	privateRenderIndex    = "index.md"
	privateRenderFileMode = 0o600
)

// clonesOf lists the clones of a store on this machine that exist: its
// clone (CloneDirFor), its pinned-branch and collision siblings whose
// origin is this store, and every recorded clone of it (own clones
// included, marked own).
func (c *standaloneClones) clonesOf(raw, norm string) ([]cloneDirs, error) {
	r, err := gitbackend.RemoteOfRaw(raw)
	if err != nil {
		r = gitbackend.Remote{Norm: norm} // a host/path spelling
	}
	var out []cloneDirs
	seen := map[string]bool{}
	add := func(dir, key string, own bool) {
		if dir == "" || seen[dir] || !exists(dir) {
			return
		}
		seen[dir] = true
		cl := cloneDirs{git: dir, work: dir, own: own, key: key}
		if !own {
			cl.remove = []string{dir}
		}
		out = append(out, cl)
	}
	rec := gitbackend.ReadCloneRecord(c.home)
	keys := make([]string, 0, len(rec.Clones))
	for k := range rec.Clones {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if k == r.Norm || strings.HasPrefix(k, r.Norm+branchCloneSep) {
			e := rec.Clones[k]
			add(e.Path, k, e.Why == gitbackend.WhyOwn)
		}
	}
	dir, own, err := gitbackend.CloneDirFor(c.home, r, "")
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "locate the store clone", err)
	}
	add(dir, "", own)
	base, err := gitbackend.CloneBase(c.home, r)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "locate the store clone", err)
	}
	for _, pattern := range []string{base + branchCloneSep + "*", base + "~*"} {
		more, gerr := filepath.Glob(pattern)
		if gerr != nil {
			return nil, errs.Wrap(errs.Internal, "list the store's branch clones", gerr)
		}
		for _, m := range more {
			if o, oerr := gitbackend.RemoteOfRaw(gitbackend.OriginAt(m)); oerr == nil && o.Norm == r.Norm {
				add(m, "", false)
			}
		}
	}
	return out, nil
}

// exists reports whether p exists.
func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// unpushedIn names what a clone holds that its remote does not; a clone
// git cannot read counts as holding work (never delete what cannot be
// checked).
func unpushedIn(ctx context.Context, cl cloneDirs) []string {
	var out []string
	if exists(cl.git) {
		n, err := gitRun{dir: cl.git, args: []string{"rev-list", "--count", "--branches", "--not", "--remotes=" + gitbackend.RemoteName}}.output(ctx)
		switch s := strings.TrimSpace(string(n)); {
		case err != nil:
			out = append(out, cl.git+" (unreadable)")
		case s != "0":
			out = append(out, s+" unpushed commit(s) in "+cl.git)
		}
	}
	if cl.work != "" && exists(cl.work) {
		st, err := gitRun{dir: cl.work, args: []string{"status", "--porcelain"}}.output(ctx)
		if err != nil {
			out = append(out, cl.work+" (unreadable)")
		} else if n := realChanges(string(st)); n > 0 {
			out = append(out, strconv.Itoa(n)+" uncommitted change(s) in "+cl.work)
		}
	}
	return out
}

// realChanges counts the lines of `git status --porcelain` that are not
// operating-system clutter: an untracked .DS_Store Finder dropped into a
// store clone is not work the forget would lose.
func realChanges(porcelain string) int {
	n := 0
	for _, l := range strings.Split(porcelain, "\n") {
		if len(l) < 4 {
			continue
		}
		p := strings.Trim(l[3:], `"`)
		if i := strings.LastIndex(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		if rowfs.IsOSJunk(p) {
			continue
		}
		n++
	}
	return n
}
