package wire

// storeforget_standalone.go — `lore store forget <url>` on cmd/lore
// (ops/stores.Cloner, Options.Local.Clones; P3-CMDLORE-9), ported from the
// old binary's store_cmd.go forgetClone + store_forget_core.go: this
// machine's clones of a store (the core's git-backend clone and its branch
// clones, and the shared v0.1.17 link clone) are deleted, refused while any
// of them holds unpushed commits or uncommitted changes (side effect 20).
// store show|open|doctor forward to storeshow_standalone.go.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// Clone folder names (adapters/backend/git and adapters/orgcfg layouts).
const (
	coreCloneWork  = "work" // the worktree inside a core clone base
	bareCloneExt   = ".git"
	branchCloneSep = "@"
)

// standaloneClones implements opsstores.Cloner for cmd/lore.
type standaloneClones struct{ home string }

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

// cloneDirs is one clone: its git dir and the folders a forget removes.
type cloneDirs struct {
	git    string   // the repository to ask about unpushed commits
	work   string   // a worktree to ask about uncommitted changes ("" none)
	remove []string // what the forget deletes
}

// ForgetClone implements opsstores.Cloner.
func (c *standaloneClones) ForgetClone(ctx context.Context, in opsstores.ForgetIn) (opsstores.ForgetOut, error) {
	norm, err := repourl.Normalize(in.URL)
	if err != nil {
		return opsstores.ForgetOut{}, errs.Wrap(errs.InvalidInput, "store forget: not a store url: "+in.URL, err)
	}
	shown := norm
	if quietOutput() {
		shown = redactedText
	}
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
	for _, cl := range clones {
		for _, d := range cl.remove {
			if rerr := os.RemoveAll(d); rerr != nil {
				return opsstores.ForgetOut{}, errs.Wrap(errs.Internal, "remove the store clone", rerr)
			}
		}
		if out.Removed == "" {
			out.Removed = cl.remove[0]
		}
	}
	return out, nil
}

// clonesOf lists the clones of a store on this machine that exist.
func (c *standaloneClones) clonesOf(raw, norm string) ([]cloneDirs, error) {
	var out []cloneDirs
	if canon, err := gitbackend.CanonicalURL(raw); err == nil {
		if u, perr := url.Parse(canon); perr == nil {
			if r, rerr := gitbackend.RemoteOf(u); rerr == nil {
				base, berr := gitbackend.CloneBase(c.home, r)
				if berr != nil {
					return nil, errs.Wrap(errs.Internal, "locate the store clone", berr)
				}
				bases := []string{base}
				more, gerr := filepath.Glob(base + branchCloneSep + "*")
				if gerr != nil {
					return nil, errs.Wrap(errs.Internal, "list the store's branch clones", gerr)
				}
				for _, m := range more {
					if !strings.HasSuffix(m, bareCloneExt) {
						bases = append(bases, m)
					}
				}
				for _, b := range bases {
					bare := b + bareCloneExt
					if exists(bare) || exists(b) {
						out = append(out, cloneDirs{git: bare, work: filepath.Join(b, coreCloneWork), remove: []string{b, bare}})
					}
				}
			}
		}
	}
	link := filepath.Join(c.home, orgcfg.ClonesDir, filepath.FromSlash(norm)+bareCloneExt)
	if exists(link) {
		out = append(out, cloneDirs{git: link, remove: []string{link}})
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
		switch s := strings.TrimSpace(string(st)); {
		case err != nil:
			out = append(out, cl.work+" (unreadable)")
		case s != "":
			out = append(out, strconv.Itoa(len(strings.Split(s, "\n")))+" uncommitted change(s) in "+cl.work)
		}
	}
	return out
}
