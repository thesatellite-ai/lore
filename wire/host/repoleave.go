package host

// repoleave.go — the operations that never change the repo the command
// runs in, so the host leaves that repo's git alone for them: no merge
// driver, .gitattributes, hook blocks (gitwire.go) or .gitignore lines
// (gitignore.go). Owner report 2026-11-11: `lore store folder add team x`
// run inside an unrelated repo rewrote its .gitignore and installed hooks
// there, a side effect of a command that only changes the org store.

import (
	"slices"

	"github.com/thesatellite-ai/lore/ops/profilestore"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// storeOnlyOps are the operations outside the store noun that write only
// an org store (the invite rows) and read this machine's setup.
// The org's members (member add|edit|remove …) and teams are roster rows of
// the org store too (B1-1: `lore member add` in a repo installed hooks and
// edited its .gitignore before being refused).
var storeOnlyOps = []string{opssetup.OpTeamInvite, opssetup.OpTeamInviteList, opssetup.OpTeamInviteRevoke, opssetup.OpTeamInviteResend,
	profilestore.OpStaffAdd, profilestore.OpStaffList, profilestore.OpStaffShow, profilestore.OpStaffEdit, profilestore.OpStaffRemove,
	profilestore.OpTeamAdd, profilestore.OpTeamList, profilestore.OpTeamShow, profilestore.OpTeamRemove, profilestore.OpTeamMemberAdd, profilestore.OpTeamMemberRemove}

// leavesWorkRepo reports whether the operation name changes only a store
// or this machine's files, never the repo it runs in: every store
// operation but the .lore/stores.yaml writers (opsstores.LeavesRepo), and
// storeOnlyOps.
func leavesWorkRepo(name string) bool {
	return opsstores.LeavesRepo(name) || slices.Contains(storeOnlyOps, name)
}
