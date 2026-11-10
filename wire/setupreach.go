package wire

// setupreach.go — folders.reachable (realmac NEW-4): a setup with no
// folder that is not the default (and not a team setup lore picks by its
// org) is never chosen by ONB 3.3 on its own. The Folders step's answers
// count over the saved entry; the decision is onboard.FoldersReachable.

import (
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// foldersReach is folders.reachable.
func (sp setupProber) foldersReach(r opssetup.ProbeRequest) opssetup.ProbeResult {
	in := onboard.FoldersReachIn{Folders: r.Setup.Folders, Default: r.Setup.Default, Org: r.Setup.Org, Disabled: r.Setup.Disabled}
	if a, ok := r.Answers[onboard.QIDFolders]; ok && a.IsList {
		in.Folders = a.List
	}
	if a, ok := r.Answers[onboard.QIDDefault]; ok && a.IsBool {
		in.Default = a.Bool
	}
	st, detail := onboard.FoldersReachable(in)
	return result(st, detail, map[string]string{onboard.VarSetup: r.Setup.Name})
}
