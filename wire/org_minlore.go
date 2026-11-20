package wire

// org_minlore.go — `lore org config set --key min_lore_version` on the
// standalone org host (devco:D-735): an org admin declares the lore
// version the org's stores need. The floor goes into every folder's
// _meta.json of each store the org entry lists (one commit per store,
// pushed only with --push); every lore since 0.1.18 refuses to write a
// folder whose floor is above its own version and says so with its
// upgrade line, while reads keep working. So a member on an old lore (one
// that skips a team-audience store without a word, devco:D-734) is told
// to upgrade instead of silently missing notes, without waiting for them
// to install anything first.
//
//	set ─▶ admin? ─▶ version this lore passes? (never lock the admin out)
//	    ─▶ plan: one line per store, floor now ─▶ confirmed?
//	    ─▶ orgcfg.RaiseStoreFloor per store ─▶ a line per store

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	pluralword "github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// teamAudienceSince is the first lore that reads a team:@<team> store
// audience; an older one skips such a store without a word (devco:D-734),
// so a team audience offers to declare it as the org's floor (D-735).
const teamAudienceSince = "0.1.19"

// setOrgMinLore raises the lore version every store of org o needs to
// in.Values[0] (CheckConfigSet already allowed exactly one valid version).
func (h *standaloneOrg) setOrgMinLore(ctx context.Context, cfg *orgconf.Config, o *orgconf.Org, in opsorgcmd.ConfigSetIn, yes bool) (opsorgcmd.Report, error) {
	floor := strings.TrimPrefix(strings.TrimSpace(in.Values[0]), "v")
	this := h.reg.version
	if !formatgate.MayWrite(this, floor) {
		shown := this
		if shown == "" {
			shown = "unknown"
		}
		return opsorgcmd.Report{}, errs.New(errs.Refused, "org config set: this lore is "+shown+", below "+floor+": it would turn read-only on "+in.Org+"'s stores itself").
			WithHint("lore upgrade, then " + opsorgcmd.MinLoreCommand(in.Org, floor))
	}
	g := &orgcfg.GitLinks{Home: h.home, Config: cfg, Now: h.wall(), Route: h.router()}
	lines := []string{"declare that " + in.Org + "'s stores need lore " + floor + " or later (min_lore_version in every folder's _meta.json of " + pluralword.Of(len(o.Stores), "store", "stores") + "):"}
	for _, st := range o.Stores {
		cur, err := g.StoreFloor(ctx, st.URL, st.Branch)
		switch {
		case err != nil:
			lines = append(lines, "  "+st.URL+": cannot read it now ("+err.Error()+")")
		case cur != "" && !formatgate.VersionBelow(cur, floor):
			lines = append(lines, "  "+st.URL+": already needs "+cur+"; kept (a floor is never lowered)")
		default:
			lines = append(lines, "  "+st.URL+": "+orNoneFloor(cur)+" -> "+floor)
		}
	}
	lines = append(lines, "  a member on an older lore can still read these stores; their writes there are refused with an upgrade line (lore upgrade) until they upgrade")
	if !yes {
		return opsorgcmd.Report{Lines: lines, Ask: "Change it?"}, nil
	}
	sig := storeSignature(o)
	var failed error
	for _, st := range o.Stores {
		res, err := g.RaiseStoreFloor(ctx, orgcfg.StoreFloorEdit{URL: st.URL, Branch: st.Branch, Org: in.Org, Floor: floor, Push: in.Push, Signature: sig})
		switch {
		case err != nil:
			lines = append(lines, "✗ "+st.URL+": "+orgFileError(err, st.URL).Error())
			if failed == nil {
				failed = orgFileError(err, st.URL)
			}
		case res.Empty:
			lines = append(lines, "· "+st.URL+": no commit yet, so no folder to raise")
		case res.Folders == 0:
			lines = append(lines, "· "+st.URL+": every folder already needs "+floor+" or later; nothing committed")
		default:
			lines = append(lines, "✓ "+st.URL+": "+pluralword.Of(res.Folders, "folder", "folders")+" now need lore "+floor+storeFloorPushNote(res))
		}
	}
	return opsorgcmd.Report{Lines: lines}, failed
}

// orNoneFloor names a store without a floor.
func orNoneFloor(v string) string {
	if v == "" {
		return "no floor"
	}
	return v
}

// storeFloorPushNote says whether the floor commit left the machine.
func storeFloorPushNote(res orgcfg.StoreFloorResult) string {
	switch {
	case res.Pushed:
		return " (pushed)"
	case res.Offline:
		return " (committed; the store could not be reached, lore sync sends it)"
	case res.NoPush:
		return " (committed in the store clone; lore sync, or lore store push --store " + res.StoreURL + ", sends it)"
	}
	return ""
}

// orgFloor is the lore version org's folder store declares it needs (""
// none or unreadable now): org config get's min_lore_version line.
func (h *standaloneOrg) orgFloor(ctx context.Context, g *orgcfg.GitLinks, sc *orgconf.Store) string {
	f, err := g.StoreFloor(ctx, sc.URL, sc.Branch)
	if err != nil {
		return ""
	}
	return f
}

// teamAudienceFloorLines are the lines `lore org store audience` (and
// store add) print for a team audience while the org folder's store needs
// less than teamAudienceSince: what an older member's lore does, and the
// command that makes it say so (devco:D-735; never set without asking).
func (h *standaloneOrg) teamAudienceFloorLines(ctx context.Context, g *orgcfg.GitLinks, org string, sc *orgconf.Store) []string {
	if cur := h.orgFloor(ctx, g, sc); cur != "" && !formatgate.VersionBelow(cur, teamAudienceSince) {
		return nil
	}
	return []string{
		"  a member on lore older than " + teamAudienceSince + " skips a team store without a word; to make their lore read-only on " + org + "'s stores with an upgrade line, run: " + opsorgcmd.MinLoreCommand(org, teamAudienceSince),
	}
}
