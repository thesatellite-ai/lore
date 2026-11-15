package wire

// branchmode.go — the one place a code branch's mode (isolated: its own
// layer; shared: the shared notes) is decided for this command
// (devco:D-411, owner 2026-11-15 "we need a solid fix").
//
//	open ─▶ attached stores (repo, project folder, org, root, personal)
//	     ─▶ readBranchPolicy: each store's policies rows read straight from
//	        its row files (no cache, no git, no fetch) + config.yaml's
//	        machine policy, resolved by policy.Resolve exactly as the
//	        engine's settings read resolves them
//	     ─▶ branchSettingsOf ─▶ branchMode (branchlayer.Classify:
//	        per-branch choice > branches.isolate > branches.main_like >
//	        branches.default_mode; off and the default branch first)
//	     ─▶ pinned on the layer wrapper BEFORE the cache path is chosen:
//	        the cache path, every read, every write, `lore branch mode` and
//	        doctor all ask the same pinned decision
//
// Why from the files: the cache path is picked before the engine exists,
// and the engine reads its policy through those very caches. Before this,
// the cache path guessed from the branch name alone (any branch but the
// default got a layer cache), while the reads asked the engine: a shared
// branch (dev, staging, a branch marked `lore branch share`) kept a layer
// cache of its own, and pinning the cache to the default rules instead
// would have let a policy-isolated dev branch write its layer rows into
// main's cache. One answer, read once, closes both.
//
// A teammate's pushed policy change reaches the files with the command's
// fetch; the next command reads it and moves to the matching cache. Within
// one command the decision never changes, so a cache never holds rows of
// the other mode.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// rowFolderOf is implemented by the git and folder backends: the row folder
// a table can be read from straight off the disk.
type rowFolderOf interface{ RowFolder() *rowfs.Folder }

// policySource is one attached store whose policies rows count at level.
type policySource struct {
	Name    store.Source
	Level   policy.Level
	Backend store.Backend
}

// readBranchPolicy resolves the policy every attached store's policies rows
// and the machine policy give, read from the row files. A store whose
// backend has no row folder (none ships today) contributes no row. A
// malformed row fails closed (E_CONFIG_INVALID naming it), as the engine's
// read does.
func readBranchPolicy(ctx context.Context, home string, sources []policySource) (policy.Set, error) {
	var entries []policy.Entry
	for _, src := range sources {
		rf, ok := concrete(src.Backend).(rowFolderOf)
		if !ok || rf.RowFolder() == nil {
			continue
		}
		rows, _, err := rf.RowFolder().Read(store.WithTables(ctx, schema.TablePolicies), "")
		if err != nil {
			return policy.Set{}, errs.Prefix("read the policies rows of store "+string(src.Name), err)
		}
		got, err := policyEntriesOf(src.Level, rows)
		if err != nil {
			return policy.Set{}, err
		}
		entries = append(entries, got...)
	}
	machine, _, err := machinePolicy(home)
	if err != nil {
		return policy.Set{}, err
	}
	return policy.Resolve(append(entries, machine...))
}

// policyEntriesOf turns the live policies rows of one store into entries at
// level: tombstones and rows in the trash do not count (the engine's list
// hides both).
func policyEntriesOf(level policy.Level, rows []store.Row) ([]policy.Entry, error) {
	out := make([]policy.Entry, 0, len(rows))
	for _, sr := range rows {
		if sr.Table != schema.TablePolicies || sr.Deleted {
			continue
		}
		r, err := row.Decode(sr.Doc)
		if err != nil {
			return nil, errs.Wrap(errs.ConfigInvalid, "policy row "+sr.ID.String()+" does not read", err).
				WithHint("lore doctor names the row and its fix")
		}
		if r.Deleted || trash.Is(r) {
			continue
		}
		en, err := policy.EntryFromRow(level, r)
		if err != nil {
			return nil, err
		}
		out = append(out, en)
	}
	return out, nil
}

// readRepoPolicyDir reads the policies rows of a code repo's own notes
// (<top>/.lore/data/policies) without opening a backend: code-repo mode
// decides inside the live write path, where opening the state again would
// re-enter the live session. A repo with no such folder has none.
func readRepoPolicyDir(top string) ([]store.Row, error) {
	dir := filepath.Join(top, loreFolderName, rowfs.DataDir, string(schema.TablePolicies))
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errs.FileIO("read the policies folder", dir, err)
	}
	var out []store.Row
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), rowfs.RowFileExt) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, errs.FileIO("read a policy row", filepath.Join(dir, f.Name()), err)
		}
		r, err := rowfs.DecodeRowFile(string(schema.TablePolicies)+"/"+f.Name(), b)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// branchSettingsOf is the branches.* part of a resolved policy set.
func branchSettingsOf(set policy.Set) branchlayer.Settings {
	s := branchlayer.DefaultSettings()
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
	return s
}

// branchMode is THE decision of a code branch's mode, for every caller (the
// cache path, reads, writes, `lore branch mode`, code-repo mode, doctor):
// branchlayer.Classify over settings read by readBranchPolicy. A detached
// HEAD has no branch to keep a layer: the callers read the shared notes
// there and refuse a write unless layers are off (Step StepOff).
func branchMode(branch, defaultBranch string, s branchlayer.Settings) branchlayer.Decision {
	return branchlayer.Classify(branch, defaultBranch, s)
}

// attachedBranchSettings is the branches.* policy the engine will see for
// this repo: the repo store's rows at the repo level, then every attached
// store's at its level (levelOf), plus the machine policy.
func attachedBranchSettings(ctx context.Context, home string, repo store.Backend, attached []attach.Opened) (branchlayer.Settings, error) {
	sources := make([]policySource, 0, len(attached)+1)
	if repo != nil {
		sources = append(sources, policySource{Name: store.SourceRepo, Level: policy.LevelRepo, Backend: repo})
	}
	for _, o := range attached {
		sources = append(sources, policySource{Name: o.Name, Level: levelOf(o), Backend: o.Backend})
	}
	set, err := readBranchPolicy(ctx, home, sources)
	if err != nil {
		return branchlayer.DefaultSettings(), err
	}
	return branchSettingsOf(set), nil
}
