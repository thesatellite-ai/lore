package wire

// branchlayer_doctor.go — `lore doctor` on branch layers (BRANCH_LAYERS
// 10, 12): one note naming the open layers of this repo's project folder
// (merged ones offered for landing, orphaned ones for land or drop), and
// a warning for a layer whose last write is older than 90 days. It reads
// only what the store clone already holds (no fetch, no write).

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	opsbranch "github.com/thesatellite-ai/lore/ops/branch"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckBranchLayers is the branch layer check's name.
const DoctorCheckBranchLayers DoctorCheck = "branch-layers"

// oldLayerDays is the age past which doctor warns about an open layer.
const oldLayerDays = 90

// DoctorBranchLayers is the branch layer check; fix (doctor --fix, lore
// fix) removes the stray layer caches it lists (devco:D-411).
func (a *App) DoctorBranchLayers(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	_, lb, name, err := a.branchTarget(ctx)
	if err != nil || lb == nil {
		return nil, nil // no project folder in a git store: nothing to say
	}
	strays, err := a.doctorStrayLayerCaches(ctx, lb, name, fix)
	if err != nil {
		return nil, err
	}
	entries, err := lb.gb.LayerList(ctx, lb.key.RepoKey, false)
	if err != nil || len(entries) == 0 {
		return strays, err
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
	return append(append([]DoctorFinding{note}, out...), strays...), nil
}

// doctorStrayLayerCaches is one finding per stray layer cache
// (strayLayerCaches): a warning naming the branch and the folder, removed
// with fix.
func (a *App) doctorStrayLayerCaches(ctx context.Context, lb *layerBackend, name store.Source, fix bool) ([]DoctorFinding, error) {
	strays, err := strayLayerCaches(ctx, a.opts.Home, lb)
	if err != nil || len(strays) == 0 {
		return nil, err
	}
	out := make([]DoctorFinding, 0, len(strays))
	for _, b := range strays {
		dir := layerCacheFolder(a.opts.Home, lb, b)
		f := DoctorFinding{Check: DoctorCheckBranchLayers, Level: DoctorWarning, Store: name,
			Message: "stray layer cache of " + b + " (no open layer of an isolated branch reads it): " + dir,
			Fix:     "lore doctor --fix removes it (lore fix too, in a repo with notes of its own)"}
		if fix {
			if rerr := os.RemoveAll(dir); rerr != nil {
				return nil, errs.FileIO("remove a stray layer cache", dir, rerr)
			}
			f.Fixed = true
		}
		out = append(out, f)
	}
	return out, nil
}
