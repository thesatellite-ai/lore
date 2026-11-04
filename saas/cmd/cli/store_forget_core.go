// store_forget_core.go — `lore store forget <url>` for the 0.1.18 core's
// clone of the same store.
//
// Why it exists: forget deleted only the v0.1.17 clone (clones-v017/). The
// core writes into its own clone (<home>/clones/<host>/<path>.git plus the
// …/work worktree, and …@<branch> for a branch entry), so a store write
// made offline sat there unpushed while forget answered ✓ and left it
// behind. v0.1.17's rule holds for both clones (side effect 20): a clone
// holding unpushed work is never deleted; once nothing waits, both go.
package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
)

// coreCloneWorkDir is the worktree folder inside a core clone base
// (adapters/backend/git workDir).
const coreCloneWorkDir = "work"

// coreClone is one core clone of a store: its bare repo and its base
// folder (which holds the worktree and the lock).
type coreClone struct {
	bare, base string
}

// coreClonesOf lists the core clones of rawURL under home: the plain one
// and every branch clone (<base>@<branch>). A url the core cannot place has
// none (the v0.1.17 path reports it).
func coreClonesOf(home, rawURL string) ([]coreClone, error) {
	r, ok := coreRemoteOf(rawURL)
	if !ok {
		return nil, nil // not a git store url: the core never cloned it
	}
	base, err := gitbackend.CloneBase(home, r)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "locate the store clone").WithCause(err)
	}
	bases := []string{base}
	more, err := filepath.Glob(base + "@*")
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "list the store's branch clones").WithCause(err)
	}
	for _, m := range more {
		if !strings.HasSuffix(m, ".git") {
			bases = append(bases, m)
		}
	}
	var out []coreClone
	for _, b := range bases {
		bare := b + ".git"
		if _, err := os.Stat(bare); err == nil {
			out = append(out, coreClone{bare: bare, base: b})
		} else if _, err := os.Stat(b); err == nil {
			out = append(out, coreClone{bare: bare, base: b})
		}
	}
	return out, nil
}

// coreRemoteOf parses rawURL the way the core's git backend does; false
// for a url it would not clone.
func coreRemoteOf(rawURL string) (gitbackend.Remote, bool) {
	canon, err := gitbackend.CanonicalURL(rawURL)
	if err != nil {
		return gitbackend.Remote{}, false
	}
	u, err := url.Parse(canon)
	if err != nil {
		return gitbackend.Remote{}, false
	}
	r, err := gitbackend.RemoteOf(u)
	return r, err == nil
}

// coreCloneUnpushed names what a core clone holds that its remote does
// not: commits on a local branch that no origin ref contains, and
// uncommitted changes in its worktree. A clone git cannot read counts as
// holding work (fail closed: never delete what cannot be checked).
func coreCloneUnpushed(ctx context.Context, c coreClone) []string {
	var out []string
	if _, err := os.Stat(c.bare); err == nil {
		n, err := gitOut(ctx, c.bare, "rev-list", "--count", "--branches", "--not", "--remotes="+gitbackend.RemoteName)
		switch {
		case err != nil:
			out = append(out, c.bare+" (unreadable)")
		case n != "0":
			out = append(out, n+" unpushed commit(s) in "+c.bare)
		}
	}
	work := filepath.Join(c.base, coreCloneWorkDir)
	if _, err := os.Stat(work); err == nil {
		st, err := gitOut(ctx, work, "status", "--porcelain")
		switch {
		case err != nil:
			out = append(out, work+" (unreadable)")
		case st != "":
			out = append(out, strconv.Itoa(len(strings.Split(st, "\n")))+" uncommitted change(s) in "+work)
		}
	}
	return out
}

// gitOut runs git in dir (through gitref, the package's one git seam) and
// returns its trimmed stdout.
func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	r, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		return "", err
	}
	b, err := r.Run(ctx, gitref.Invocation{Args: args})
	return strings.TrimSpace(string(b)), err
}

// removeCoreClones deletes the core clones (bare repo and base folder).
func removeCoreClones(cs []coreClone) error {
	for _, c := range cs {
		for _, d := range []string{c.bare, c.base} {
			if err := os.RemoveAll(d); err != nil {
				return errcodes.New(errcodes.Internal, "remove the store clone").WithCause(err)
			}
		}
	}
	return nil
}
