package wire

// org_storeadd.go — `lore org store add` on the standalone org host
// (B-4): the store goes into the org's lore-org.yaml, committed in the
// org store's clone (pushed only with --push), then this machine's org
// entry copies the file, so `lore org link --store <it>` works at once.
// The commit is the team's source; teammates get the store with their
// next `lore org refresh` (or the automatic one) once it is pushed.

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
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
	lines = append(lines, "✓ org entry "+in.Org+" lists it (version "+short(sg.Version)+")",
		"  next: lore org link --org "+in.Org+" --project <folder> --store "+in.URL)
	return opsorgcmd.Report{Lines: lines}, nil
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
