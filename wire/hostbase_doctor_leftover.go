package wire

// hostbase_doctor_leftover.go — the "notes left in a linked repo" check of
// `lore doctor` (devco:D-729). A repo linked to a store folder reads that
// folder and writes every row with no --to there, so live notes still in
// its .lore/data are a move not yet done: they show up in the repo's pull
// requests and never in the folder the team reads. The warning names the
// one command that moves them (`lore store adopt --move`, which also keeps
// the link and removes the moved row files as a diff to commit).
//
//	code repo (not a store clone, not linked by remote)
//	  ─► RepoLinkTarget: "<store>/<folder>" ("" = not linked: nothing)
//	  ─► orgcfg.HoldsNotes(.lore/data, routed kinds): a live note row?
//	  ─► one warning naming the folder and the adopt --move command

import (
	"context"
	"fmt"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
)

// doctorLinkedLeftoverWarning is the warning; %[1]s is the linked folder,
// %[2]s the command that moves the notes there.
const doctorLinkedLeftoverWarning = "this repo is linked to %[1]s, where its reads come from and every write with no --to goes, but its .lore/data still holds notes of its own; they reach the repo's pull requests, not the folder. Move them there with one command: %[2]s"

// doctorLinkedLeftovers warns when a linked repo still holds live note rows
// in .lore/data (D-729). Outside a code repo, in a store clone, in a repo
// linked by remote (it holds no lore files) or with no link it says
// nothing.
func (a *App) doctorLinkedLeftovers(ctx context.Context, warn func(string)) {
	dir := a.opts.RepoDir
	if dir == "" || inStore(dir) || a.opts.RepoLinkTarget == nil {
		return
	}
	if a.opts.RepoLinkedByRemote != nil && a.opts.RepoLinkedByRemote(ctx) != "" {
		return
	}
	target := a.opts.RepoLinkTarget(ctx)
	if target == "" || !orgcfg.HoldsNotes(dir, isRoutedTable) {
		return
	}
	warn(fmt.Sprintf(doctorLinkedLeftoverWarning, target, a.leftoverMoveCommand(ctx, target)))
}

// leftoverMoveCommand is the adopt --move that moves this repo's notes to
// target: --own for one of the org's own repos (adopt --move links the repo
// again, the way org link takes it), none for a client repo.
func (a *App) leftoverMoveCommand(ctx context.Context, target string) string {
	cmd := "lore store adopt --target " + target + " --move"
	if a.opts.ClientRepo == nil || a.opts.ClientRepo(ctx).Org == "" {
		cmd += " --own"
	}
	return cmd + " --yes"
}
