package wire

// org_storeaudience.go — `lore org store audience` on the standalone org
// host (devco D-442): an admin marks a store of the org as used only by
// its admins (servers, domains, accounts), or opens it to every member
// again. The store's entry in lore-org.yaml gets `audience: admins` (or
// loses the key), committed in the org store's clone (pushed only with
// --push); this machine's entry copies it. A member's next open copies
// the file without the store, so their lore never reads it.

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// audienceAdminsLine says what an admins-only store means, in every
// command's plan that makes one.
const audienceAdminsLine = "audience: admins: only the org's admins use it; members' lore never clones or reads it, and no repo can be linked to it"

// audiencePushNote is the tail of an org-file change line: pushed, or the
// command that pushes the commit. That command is `lore store push --store
// <url>` of the org file's store (devco:D-544): the change's own command
// with --push alone refused "nothing to change", and run again with its
// change it found nothing to commit, so the printed line never pushed.
func audiencePushNote(res orgcfg.OrgFileResult, storeURL string) string {
	switch {
	case res.Pushed:
		return " (pushed)"
	case res.Offline:
		return " (committed; the org store is unreachable, the next write to it pushes)"
	}
	return " (committed in this machine's clone of the store, not pushed; push it with: " + StorePushCommand(storeURL) + ")"
}

// audienceTeamLine says what a team store means, in every command's plan
// that makes one (D-727).
func audienceTeamLine(team string) string {
	return "audience: team:@" + team + ": only @" + team + "'s members and the org's admins use it; every other member's lore never clones, fetches or names it"
}

// checkAudience refuses an audience the org cannot take (D-727): a team
// audience naming a team the org's roster (in this machine's clone of the
// org folder's store sc) does not hold. cmd is the command, without its
// --audience, that the hint names again.
func checkAudience(ctx context.Context, home string, sc *orgconf.Store, org string, aud orgconf.Audience, cmd string) error {
	team, ok := aud.Team()
	if !ok {
		return nil
	}
	teams := orgcfg.TeamHandles(ctx, home, sc.URL, sc.OrgFolder)
	if slices.Contains(teams, team) {
		return nil
	}
	e := errs.New(errs.NotFound, "--audience team:@"+team+": org "+org+"'s roster has no team @"+team)
	if len(teams) == 0 {
		return e.WithHint("make the team first: lore team add --handle @" + team + " --members @<member>; then: " + cmd + " --audience team:@" + team)
	}
	return e.WithHint("its teams: @" + strings.Join(teams, ", @") + "; e.g. " + cmd + " --audience team:@" + teams[0] + " (or make it: lore team add --handle @" + team + " --members @<member>)")
}

// orgAudienceRefusal is the refusal of a store audience change by a person
// with no admin setup of org (the rule every org config write follows).
func orgAudienceRefusal(org, cmd string) error {
	return errs.New(errs.Refused, "org store audience: only an admin of "+org+" sets who uses its stores; this machine has no admin setup of "+org).
		WithHint("ask the org admin to run `" + cmd + " --push --yes`")
}

// StorePushCommand is the command that pushes a store clone's commits,
// named by the store's url (which works from any folder, a repo that does
// not attach the store too).
func StorePushCommand(storeURL string) string {
	return "lore store push --store " + storeURL
}

// SetStoreAudience implements opsorgcmd.StoreAudiencer.
func (h *standaloneOrg) SetStoreAudience(ctx context.Context, in opsorgcmd.StoreAudienceIn, yes bool) (opsorgcmd.Report, error) {
	cfg, err := h.loadOrgConfig()
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	o := cfg.FindOrg(in.Org)
	if o == nil {
		return opsorgcmd.Report{}, errs.New(errs.NotFound, "no org entry named "+in.Org+" in "+lorehome.Shown()+"/config.yaml").
			WithHint("entries: lore config check lists them")
	}
	aud, err := orgconf.ParseAudience(in.Audience)
	if err != nil {
		return opsorgcmd.Report{}, errs.Wrap(errs.InvalidInput, "--audience "+in.Audience, err).
			WithHint(opsorgcmd.StoreAudienceCommand(in.Org, in.URL, orgconf.AudienceAdmins))
	}
	want, err := repourl.NormalizeStore(in.URL)
	if err != nil {
		return opsorgcmd.Report{}, errs.Wrap(errs.InvalidInput, "--url "+in.URL+" is not a store url", err).
			WithHint("give the store's git url as lore store list shows it, e.g. --url git@github.com:agency/ops.git")
	}
	cmd := opsorgcmd.StoreAudienceCommand(in.Org, in.URL, aud)
	if !isOrgAdmin(h.home, in.Org) {
		return opsorgcmd.Report{}, orgAudienceRefusal(in.Org, cmd)
	}
	sc := o.OrgFolderStore()
	if sc == nil {
		if len(o.Stores) == 0 {
			return opsorgcmd.Report{}, errs.New(errs.InvalidInput, "org entry "+in.Org+" lists no store, so there is no lore-org.yaml to change").
				WithHint("import the org again: lore org import --url <store-url>")
		}
		sc = &o.Stores[0]
	}
	if n, nerr := repourl.NormalizeStore(sc.URL); nerr == nil && n == want && !aud.ForMembers() {
		return opsorgcmd.Report{}, errs.New(errs.Refused, "org store audience: "+want+" holds "+in.Org+"'s org folder (its roster, teams and company rules), which every member reads; it cannot be for "+aud.String()+" only").
			WithHint("keep those notes in a store of their own: lore org store add --org " + in.Org + " --url <url> --audience " + aud.String() + " --push --yes")
	}
	if err := checkAudience(ctx, h.home, sc, in.Org, aud, "lore org store audience --org "+in.Org+" --url "+in.URL); err != nil {
		return opsorgcmd.Report{}, err
	}
	if o.StoreFor(want) == nil {
		return opsorgcmd.Report{}, errs.New(errs.NotFound, "org "+in.Org+" lists no store "+want).
			WithHint("add it with: " + opsorgcmd.StoreAddCommand(in.Org, in.URL) + " --audience " + aud.String())
	}
	file := orgcfg.OrgFilePath(sc.OrgFolder) + " in " + sc.URL
	lines := []string{"set who uses store " + want + " in org " + in.Org + ": " + aud.String()}
	if team, ok := aud.Team(); ok {
		lines = append(lines, "  "+audienceTeamLine(team), "  a member outside @"+team+" drops the store at their next sync after they get the commit; their clone of it, if any, stays on disk and is no longer read")
	} else if aud == orgconf.AudienceAdmins {
		lines = append(lines, "  "+audienceAdminsLine, "  members' lore drops the store at their next open after they get the commit; their clone of it, if any, stays on disk")
	} else {
		lines = append(lines, "  every member's lore clones and reads it after they get the commit")
	}
	if !yes {
		return opsorgcmd.Report{Lines: lines, Ask: "Change it?"}, nil
	}
	g := &orgcfg.GitLinks{Home: h.home, Config: cfg, Now: h.wall(), Route: h.router()}
	res, err := g.AddOrgFileStore(ctx, orgcfg.OrgFileStoreAdd{URL: sc.URL, OrgFolder: sc.OrgFolder, Branch: sc.Branch, Org: in.Org,
		Store: orgcfg.OrgFileStore{URL: in.URL, Audience: string(aud)}, Push: in.Push, SetAudience: true, Signature: storeSignature(o)})
	if err != nil {
		return opsorgcmd.Report{Lines: lines}, orgFileError(err, sc.URL)
	}
	switch {
	case res.AudienceChanged:
		lines = append(lines, "✓ store "+want+" in "+file+" is now for "+aud.String()+audiencePushNote(res, sc.URL))
	case !res.Existed:
		// a store the entry lists but the file does not (a hand edit): the
		// add appended it with the audience asked for
		lines = append(lines, "✓ "+file+" now lists store "+want+" for "+aud.String()+audiencePushNote(res, sc.URL))
	default:
		lines = append(lines, "· store "+want+" in "+file+" is already for "+aud.String()+"; nothing committed")
	}
	sg, serr := g.LocalSuggestion(ctx, sc.URL, sc.Branch)
	if serr != nil {
		return opsorgcmd.Report{Lines: lines}, orgFileError(serr, sc.URL)
	}
	mine, _ := mineOf(ctx, cfg, o, sg)
	if err := writeOrgSource(h.home, configLockWait, in.Org, mine, sg.Source()); err != nil {
		return opsorgcmd.Report{Lines: lines}, err
	}
	lines = append(lines, "✓ org entry "+in.Org+" copies it (version "+short(sg.Version)+")")
	return opsorgcmd.Report{Lines: lines}, nil
}

// compile-time check: the org host offers org.store.audience.
var _ opsorgcmd.StoreAudiencer = (*standaloneOrg)(nil)
