package wire

// branchlayer_repo.go — code-repo mode of branch layers (BRANCH_LAYERS 4,
// owner decision 2026-11-14 "everything should isolate, even the task"):
// in a repo whose notes live in its own .lore/data, a new row of a live
// kind (tasks, missions, handoffs, comments) written on an isolated code
// branch stays with the code on that branch (as --no-live writes it) and
// lands with the branch's merge. The default branch and main-like branches
// keep today's live rows; --no-layer and LORE_LAYER=off keep them for one
// write or one shell. Classified by the same rules and policy keys as a
// store layer (logic/branchlayer.Classify over the repo's branches.*).

import (
	"context"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/logic/policy"
)

// repoBranchCache keeps one command's answer.
type repoBranchCache struct {
	once     sync.Once
	isolated bool
}

// RepoBranchIsolated reports whether the checkout of this App's repo is on
// an isolated code branch (read once per command; false on a detached
// HEAD, the default branch, a main-like branch, with layers off, or when
// the classification cannot be read).
func (a *App) RepoBranchIsolated(ctx context.Context) bool {
	a.repoBranch.once.Do(func() {
		if envLayerIsOff() {
			return
		}
		top := gitTopLevel(ctx, a.projectDir())
		if top == "" {
			return
		}
		code := readCodeBranch(ctx, top)
		if code.Branch == "" {
			return
		}
		// read from the repo's own policy rows, never through the App's
		// state: this runs inside the live write path (wire/host), where
		// opening the state again would re-enter the live session
		s, err := repoBranchSettings(a.opts.Home, top)
		if err != nil {
			return // a policy that does not read: today's live rows, doctor names it
		}
		a.repoBranch.isolated = branchMode(code.Branch, code.Default, s).Mode == branchlayer.Isolated
	})
	return a.repoBranch.isolated
}

// readCodeBranch is readCodeRepo without the remote: the branch and the
// default branch of the checkout at top (a repo with its own notes needs
// no origin to have branches).
func readCodeBranch(ctx context.Context, top string) codeRepo {
	run := func(args ...string) string {
		out, err := liveGit(ctx, top, args...)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	c := codeRepo{Top: top}
	c.Branch = run("symbolic-ref", "--short", "--quiet", "HEAD")
	c.Default = strings.TrimPrefix(run("symbolic-ref", "--short", "--quiet", "refs/remotes/origin/HEAD"), "origin/")
	if c.Default == "" {
		// no origin/HEAD: the repo's own main or master, else the
		// configured default; a repo with none of them treats the branch it
		// is on as its default (nothing to isolate from)
		for _, b := range []string{defaultCodeBranch, "master", run("config", "--get", "init.defaultBranch")} {
			if b != "" && run("rev-parse", "--verify", "--quiet", "refs/heads/"+b) != "" {
				c.Default = b
				break
			}
		}
	}
	if c.Default == "" {
		c.Default = c.Branch
	}
	return c
}

// repoBranchSettings are the branches.* keys the repo's own policy rows
// (.lore/data/policies at the repo top) and the machine policy set, read
// and resolved as readBranchPolicy resolves a store's (branchmode.go), so
// code-repo mode and the store layers decide by one rule. A policy that
// does not read is returned as the error: the caller treats the branch as
// shared for live rows and `lore doctor` names the row.
func repoBranchSettings(home, top string) (branchlayer.Settings, error) {
	rows, err := readRepoPolicyDir(top)
	if err != nil {
		return branchlayer.DefaultSettings(), err
	}
	entries, err := policyEntriesOf(policy.LevelRepo, rows)
	if err != nil {
		return branchlayer.DefaultSettings(), err
	}
	machine, _, err := machinePolicy(home)
	if err != nil {
		return branchlayer.DefaultSettings(), err
	}
	set, err := policy.Resolve(append(entries, machine...))
	if err != nil {
		return branchlayer.DefaultSettings(), err
	}
	return branchSettingsOf(set), nil
}
