package wire

// org_storeadd.go — `lore org store add` on the standalone org host
// (B-4): the store goes into the org's lore-org.yaml, committed in the
// org store's clone (pushed only with --push), then this machine's org
// entry copies the file, so `lore org link --store <it>` works at once.
// The commit is the team's source; teammates get the store with their
// next `lore org refresh` (or the automatic one) once it is pushed.

import (
	"cmp"
	"context"

	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// AddStore implements opsorgcmd.StoreAdder.
func (h *standaloneOrg) AddStore(ctx context.Context, in opsorgcmd.StoreAddIn, yes bool) (opsorgcmd.Report, error) {
	cfg, err := h.loadOrgConfig()
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	o := cfg.FindOrg(in.Org)
	if o == nil {
		return opsorgcmd.Report{}, errs.New(errs.NotFound, "no org entry named "+in.Org+" in "+lorehome.Shown()+"/config.yaml").
			WithHint("entries: lore config check lists them; start one with lore org create --org <org> --store <url>")
	}
	want, err := repourl.NormalizeStore(in.URL)
	if err != nil {
		return opsorgcmd.Report{}, errs.Wrap(errs.InvalidInput, "--url "+in.URL+" is not a store url", err).
			WithHint("give the store's git url, e.g. --url git@github.com:agency/client-notes.git")
	}
	aud, aerr := orgconf.ParseAudience(in.Audience)
	if aerr != nil {
		return opsorgcmd.Report{}, errs.Wrap(errs.InvalidInput, "--audience "+in.Audience, aerr).
			WithHint(opsorgcmd.StoreAddCommand(in.Org, in.URL) + " --audience admins")
	}
	sc := o.OrgFolderStore()
	if sc == nil {
		if len(o.Stores) == 0 {
			return opsorgcmd.Report{}, errs.New(errs.InvalidInput, "org entry "+in.Org+" lists no store, so there is no lore-org.yaml to add the store to").
				WithHint("import the org again: lore org import --url <store-url>")
		}
		sc = &o.Stores[0]
	}
	if aud != orgconf.AudienceEveryone {
		// D-727: keeping a store from some members is an org config
		// change, an admin's (the rule of lore org store audience)
		if !isOrgAdmin(h.home, in.Org) {
			return opsorgcmd.Report{}, orgAudienceRefusal(in.Org, opsorgcmd.StoreAddCommand(in.Org, in.URL)+" --audience "+aud.String())
		}
		if err := checkAudience(ctx, h.home, sc, in.Org, aud, opsorgcmd.StoreAddCommand(in.Org, in.URL)); err != nil {
			return opsorgcmd.Report{}, err
		}
	}
	named := ""
	if in.Name == "" {
		// devco T-5: the name the person gave the store in this repo
		// (`lore store init --name clientco`) goes into lore-org.yaml, so it
		// is the store's name in every repo of the org, not its repo name
		if n := storesFileName(gitTop(ctx, h.reg.dir()), want); n != "" {
			in.Name, named = n, "  named "+n+" (its name in this repo's .lore/stores.yaml; --name sets another)"
		}
	}
	file := orgcfg.OrgFilePath(sc.OrgFolder) + " in " + sc.URL
	push := "committed in this machine's clone of the store; not pushed without --push"
	if in.Push {
		push = "committed and pushed to the org store"
	}
	lines := []string{"add store " + want + " to org " + in.Org + ":", "  " + file + " lists it (" + push + ")", "  this machine's org entry " + in.Org + " copies it"}
	if aud == orgconf.AudienceAdmins {
		lines = append(lines, "  "+audienceAdminsLine)
	} else if team, ok := aud.Team(); ok {
		lines = append(lines, "  "+audienceTeamLine(team))
		// devco:D-735: an older member's lore skips a team store silently;
		// offer the org floor that makes it say so (never set unasked)
		lines = append(lines, h.teamAudienceFloorLines(ctx, &orgcfg.GitLinks{Home: h.home, Config: cfg, Now: h.wall(), Route: h.router()}, in.Org, sc)...)
	}
	otherStore := !storeaddr.Same(in.URL, sc.URL)
	if otherStore {
		lines = append(lines, "  "+want+"'s lore-store.yaml names the org and its main store "+sc.URL+", so a join by its url finds the org")
	}
	if named != "" {
		lines = append(lines, named)
	}
	if !yes {
		return opsorgcmd.Report{Lines: lines, Ask: "Add it?"}, nil
	}
	g := &orgcfg.GitLinks{Home: h.home, Config: cfg, Now: h.wall(), Route: h.router()}
	res, err := g.AddOrgFileStore(ctx, orgcfg.OrgFileStoreAdd{URL: sc.URL, OrgFolder: sc.OrgFolder, Branch: sc.Branch, Org: in.Org,
		Store: orgcfg.OrgFileStore{URL: in.URL, Name: in.Name, Branch: in.Branch, Audience: string(aud)}, Push: in.Push, Signature: storeSignature(o),
		SetAudience: in.Audience != ""})
	if err != nil {
		return opsorgcmd.Report{Lines: lines}, orgFileError(err, sc.URL)
	}
	switch {
	case res.AudienceChanged:
		lines = append(lines, "✓ "+file+" already listed it; its audience is now "+aud.String()+audiencePushNote(res, sc.URL))
	case res.Existed && res.Pushed:
		lines = append(lines, "✓ "+file+" already listed it; pushed the commit that added it")
	case res.Existed:
		lines = append(lines, "· "+file+" already lists it; nothing committed")
	case res.Pushed:
		lines = append(lines, "✓ added store "+want+" to "+file+" (pushed)")
	case res.Offline:
		lines = append(lines, "✓ added store "+want+" to "+file+" (committed; the org store is unreachable, the next write to it pushes)")
	default:
		lines = append(lines, "✓ added store "+want+" to "+file+audiencePushNote(res, sc.URL))
	}
	// The entry copies the file as committed in the clone (the local tip,
	// pushed or not), with its stamp, as `lore org refresh` does.
	sg, serr := g.LocalSuggestion(ctx, sc.URL, sc.Branch)
	if serr != nil {
		return opsorgcmd.Report{Lines: lines}, orgFileError(serr, sc.URL)
	}
	mine, _ := mineOf(ctx, cfg, o, sg)
	if err := writeOrgSource(h.home, configLockWait, in.Org, mine, sg.Source()); err != nil {
		return opsorgcmd.Report{Lines: lines}, err
	}
	lines = append(lines, "✓ org entry "+in.Org+" lists it (version "+short(sg.Version)+")")
	if otherStore {
		lines = append(lines, markStoreOrg(ctx, g, orgcfg.OrgStoreMark{URL: in.URL, Branch: in.Branch, Org: cmp.Or(sg.Doc.Name, in.Org),
			MainStore: sc.URL, Signature: storeSignature(o)}, want))
	}
	lines = append(lines, "  next: lore org link --org "+in.Org+" --project <folder> --store "+in.URL)
	return opsorgcmd.Report{Lines: lines}, nil
}

// markStoreOrg names the org and its main store in the added store's
// lore-store.yaml (devco:D-721) and says how that went in one line. The
// commit waits in the store's clone for the next `lore sync`, also with
// --push: pushing it would send the store's other waiting commits (a link
// made before) under the org file's --push. It never fails the add: a
// store that is no lore store yet, or that does not answer, is named by
// an admin's next `lore sync`.
func markStoreOrg(ctx context.Context, g *orgcfg.GitLinks, m orgcfg.OrgStoreMark, want string) string {
	res, err := g.MarkOrgStore(ctx, m)
	switch {
	case err != nil:
		return "· " + want + "'s lore-store.yaml does not name the org yet (" + err.Error() + "); the admin's next lore sync names it"
	case res.NotAStore:
		return "· " + want + " is no lore store yet; once it is, the admin's next lore sync names the org in its lore-store.yaml"
	case res.Existed:
		return "· " + want + "'s lore-store.yaml already names org " + m.Org
	}
	return "✓ " + want + "'s lore-store.yaml names org " + m.Org + " and its main store (committed; lore sync sends it)"
}

// storesFileName is the name top's .lore/stores.yaml gives the store of
// normalised url want ("" when top is "" or nothing there names it).
func storesFileName(top, want string) string {
	if top == "" {
		return ""
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return ""
	}
	for _, st := range cfg.Stores {
		if n, nerr := repourl.NormalizeStore(st.URL); nerr == nil && n == want {
			return string(st.Name)
		}
	}
	return ""
}

// compile-time check: the org host offers org.store.add.
var _ opsorgcmd.StoreAdder = (*standaloneOrg)(nil)
