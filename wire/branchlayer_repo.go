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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/row"
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
		s := repoBranchSettings(top)
		a.repoBranch.isolated = branchlayer.Classify(code.Branch, code.Default, s).Mode == branchlayer.Isolated
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
// set (.lore/data/policies at the repo top), over the defaults. A row that
// does not read is skipped: a broken policy row is `lore doctor`'s to name.
func repoBranchSettings(top string) branchlayer.Settings {
	s := branchlayer.DefaultSettings()
	dir := filepath.Join(top, loreFolderName, "data", "policies")
	files, err := os.ReadDir(dir)
	if err != nil {
		return s
	}
	for _, f := range files {
		b, rerr := os.ReadFile(filepath.Join(dir, f.Name()))
		if rerr != nil {
			continue
		}
		var r struct {
			Key       string          `json:"key"`
			Value     json.RawMessage `json:"value"`
			Deleted   bool            `json:"_deleted"`
			DeletedAt string          `json:"deleted_at"`
		}
		if json.Unmarshal(row.TrimBOM(b), &r) != nil || r.Deleted || r.DeletedAt != "" || len(r.Value) == 0 {
			continue
		}
		raw := []byte(r.Value)
		var items []string
		var flag bool
		var text string
		switch policy.Key(r.Key) {
		case policy.KeyBranchesLayers:
			if json.Unmarshal(raw, &flag) == nil {
				s.Layers = flag
			}
		case policy.KeyBranchesDefaultMode:
			if json.Unmarshal(raw, &text) == nil {
				s.DefaultMode = branchlayer.ModeOfPolicy(text)
			}
		case policy.KeyBranchesIsolate:
			if json.Unmarshal(raw, &items) == nil {
				s.Isolate = items
			}
		case policy.KeyBranchesMainLike:
			if json.Unmarshal(raw, &items) == nil {
				s.MainLike = items
			}
		case policy.KeyBranchesIsolated:
			if json.Unmarshal(raw, &items) == nil {
				s.IsolatedSet = items
			}
		case policy.KeyBranchesShared:
			if json.Unmarshal(raw, &items) == nil {
				s.SharedSet = items
			}
		}
	}
	return s
}
