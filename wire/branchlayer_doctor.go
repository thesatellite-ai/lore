package wire

// branchlayer_doctor.go — `lore doctor` on branch layers (BRANCH_LAYERS
// 10, 12): one note naming the open layers of this repo's project folder
// (merged ones offered for landing, orphaned ones for land or drop), and
// a warning for a layer whose last write is older than 90 days. It reads
// only what the store clone already holds (no fetch, no write).

import (
	"context"
	"strconv"
	"strings"
	"time"

	opsbranch "github.com/thesatellite-ai/lore/ops/branch"
)

// DoctorCheckBranchLayers is the branch layer check's name.
const DoctorCheckBranchLayers DoctorCheck = "branch-layers"

// oldLayerDays is the age past which doctor warns about an open layer.
const oldLayerDays = 90

// DoctorBranchLayers is the branch layer check.
func (a *App) DoctorBranchLayers(ctx context.Context) ([]DoctorFinding, error) {
	_, lb, name, err := a.branchTarget(ctx)
	if err != nil || lb == nil {
		return nil, nil // no project folder in a git store: nothing to say
	}
	entries, err := lb.gb.LayerList(ctx, lb.key.RepoKey, false)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	now := a.opts.Wall()
	var parts []string
	var out []DoctorFinding
	for _, e := range entries {
		what := e.Layer.Key.Branch + ": " + opsbranch.RowsText(tableCounts(e.Counts))
		if st, _ := codeState(ctx, lb, e.Layer); st != "" {
			what += ", " + st
		}
		parts = append(parts, what)
		if last, terr := lb.gb.LayerTipTime(ctx, e.Layer); terr == nil && now.Sub(last) > oldLayerDays*24*time.Hour {
			out = append(out, DoctorFinding{Check: DoctorCheckBranchLayers, Level: DoctorWarning, Store: name,
				Message: "the notes of " + e.Layer.Key.Branch + " in " + e.Layer.Key.Folder + " were last written " + strconv.Itoa(int(now.Sub(last).Hours()/24)) + " days ago",
				Fix:     "lore branch land --branch " + e.Layer.Key.Branch + " (merged) or lore branch drop --branch " + e.Layer.Key.Branch})
		}
	}
	note := DoctorFinding{Check: DoctorCheckBranchLayers, Level: DoctorNote, Store: name,
		Message: strconv.Itoa(len(entries)) + " branch layer(s) open in " + lb.key.Folder + " (" + strings.Join(parts, "; ") + ")",
		Fix:     "lore branch list"}
	if offer := a.ciOffer(ctx, lb); offer != "" {
		out = append(out, DoctorFinding{Check: DoctorCheckBranchLayers, Level: DoctorNote, Store: name, Message: offer, Fix: "lore branch install-action --dry-run"})
	}
	return append([]DoctorFinding{note}, out...), nil
}
