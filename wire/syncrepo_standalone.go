package wire

// syncrepo_standalone.go — cmd/lore's host for `lore sync install-action`
// and `lore sync ci-merge` (LORE_TEAM_SPEC command table; v0.1.17
// saas/cmd/cli sync_action.go / sync_ci.go, now adapters/reposync). The
// binary's opssync.Env (wire/host liveSync) delegates these verbs here.
//
//	install-action ─▶ reposync.Install (render, write or --dry-run)
//	ci-merge       ─▶ reposync.CIMerge (RX7 key loosening first, OV64;
//	                  lore's drivers = this binary, per git invocation)

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/reposync"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// RepoSync is the repo-level sync host: WorkDir is the folder the command
// runs in (the lore project), Version the binary's version (the workflow
// pin), Driver the program git runs as lore's merge driver ("" = this
// executable).
type RepoSync struct {
	WorkDir string
	Version string
	Driver  string
}

// SyncInstallAction implements opssync.Env's install-action verb.
func (r RepoSync) SyncInstallAction(ctx context.Context, in opssync.InstallActionIn) (opssync.ActionOut, error) {
	out := opssync.ActionOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindAction}
	res, err := reposync.Install(ctx, reposync.InstallOptions{
		Dir: r.dir(), Branches: in.Branches, Auto: in.Auto, Manual: in.Manual,
		LoreVersion: in.LoreVersion, DryRun: in.DryRun, Force: in.Force, Own: r.Version,
	})
	if err != nil {
		return out, err
	}
	out.Data = opssync.ActionData{
		Path: res.Path, Status: string(res.Status), LoreVersion: res.LoreVersion, Pin: string(res.Pin),
		Branches: res.Branches, Manual: res.Manual, Auto: res.Auto, Content: res.Content,
	}
	out.Lines = reposync.ActionLines(res)
	return out, nil
}

// SyncCIMerge implements opssync.Env's ci-merge verb; every outcome exits 0
// (LORE_SYNC 9.1), the JSON data keys are v0.1.17's.
func (r RepoSync) SyncCIMerge(ctx context.Context, in opssync.CIMergeIn) (opssync.CIMergeOut, error) {
	out := opssync.CIMergeOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindCIMerge}
	res, err := reposync.CIMerge(ctx, reposync.CIMergeOptions{
		Dir: r.dir(), Base: in.Base, DryRun: in.DryRun, Driver: r.driver(),
		CheckBase: func(ctx context.Context, dir, base string) error {
			found, kerr := KeyLooseningsSince(ctx, dir, base)
			if kerr != nil || len(found) == 0 {
				return nil // no common history: nothing to compare (v0.1.17 checkKeyLoosening)
			}
			return keyLoosening(found)
		},
	})
	if err != nil {
		return out, err
	}
	out.Data = opssync.CIMergeData{
		Outcome: string(res.Outcome), Base: res.Base, Commit: res.Commit,
		Merged: res.Merged, Blocking: res.Blocking, Reason: res.Reason,
	}
	out.Lines = reposync.CIMergeLines(res, in.DryRun)
	return out, nil
}

func (r RepoSync) dir() string {
	if r.WorkDir == "" {
		return "."
	}
	return r.WorkDir
}

// driver is the merge driver program: the configured one, else this
// executable's resolved path single-quoted for the shell git runs it with
// (so a merge never falls to an older lore on PATH that cannot read `_v: 2`
// rows), else BinaryName.
func (r RepoSync) driver() string {
	if r.Driver != "" {
		return r.Driver
	}
	exe, err := os.Executable()
	if err != nil {
		return reposync.BinaryName
	}
	if real, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = real
	}
	if strings.ContainsRune(exe, '\'') {
		return reposync.BinaryName
	}
	return "'" + exe + "'"
}

