package wire

// org_config.go — `lore org config get|set` on the standalone org host
// (devco D-453): read the org's shared lore-org.yaml from this machine's
// clone of the org store, or change its own:/repos: patterns as an admin:
// committed in the clone (pushed only with --push), then this machine's
// org entry copies the file (what `lore org refresh` did by hand).

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// orgFileStoreOf is the org entry org and the store holding its org
// folder (lore-org.yaml), or the refusal naming what is missing.
func (h *standaloneOrg) orgFileStoreOf(org string) (*orgconf.Config, *orgconf.Org, *orgconf.Store, error) {
	cfg, err := h.loadOrgConfig()
	if err != nil {
		return nil, nil, nil, err
	}
	o := cfg.FindOrg(org)
	if o == nil {
		return nil, nil, nil, errs.New(errs.NotFound, "no org entry named "+org+" in "+lorehome.Shown()+"/config.yaml").
			WithHint("entries: lore config check lists them")
	}
	sc := o.OrgFolderStore()
	if sc == nil {
		if len(o.Stores) == 0 {
			return nil, nil, nil, errs.New(errs.InvalidInput, "org entry "+org+" lists no store, so there is no lore-org.yaml").
				WithHint("import the org again: lore org import --url <store-url>")
		}
		sc = &o.Stores[0]
	}
	return cfg, o, sc, nil
}

// OrgConfigGet implements opsorgcmd.ConfigEditor.
func (h *standaloneOrg) OrgConfigGet(ctx context.Context, in opsorgcmd.ConfigGetIn) (opsorgcmd.ConfigGetOut, error) {
	cfg, _, sc, err := h.orgFileStoreOf(in.Org)
	if err != nil {
		return opsorgcmd.ConfigGetOut{}, err
	}
	g := &orgcfg.GitLinks{Home: h.home, Config: cfg, Now: h.wall(), Route: h.router()}
	sg, err := g.LocalSuggestion(ctx, sc.URL, sc.Branch)
	if err != nil {
		return opsorgcmd.ConfigGetOut{}, orgFileError(err, sc.URL)
	}
	out := opsorgcmd.ConfigGetOut{Org: in.Org, File: orgcfg.OrgFilePath(sc.OrgFolder) + " in " + sc.URL,
		Own: nonNil(sg.Doc.Own), Repos: nonNil(sg.Doc.Repos), Stores: []string{}}
	for _, st := range sg.Doc.Stores {
		s := st.URL
		if !orgconf.Audience(st.Audience).ForMembers() {
			s += " (admins)"
		}
		out.Stores = append(out.Stores, s)
	}
	return out, nil
}

// nonNil is s, or an empty list for JSON.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// OrgConfigSet implements opsorgcmd.ConfigEditor.
func (h *standaloneOrg) OrgConfigSet(ctx context.Context, in opsorgcmd.ConfigSetIn, yes bool) (opsorgcmd.Report, error) {
	cfg, o, sc, err := h.orgFileStoreOf(in.Org)
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	cmd := opsorgcmd.ConfigSetCommand(in.Org, in.Key)
	if !isOrgAdmin(h.home, in.Org) {
		return opsorgcmd.Report{}, errs.New(errs.Refused, "org config set: only an admin of "+in.Org+" changes its lore-org.yaml; this machine has no admin setup of "+in.Org).
			WithHint("ask the org admin to run `" + cmd + " --add <pattern> --push --yes`")
	}
	g := &orgcfg.GitLinks{Home: h.home, Config: cfg, Now: h.wall(), Route: h.router()}
	sg, err := g.LocalSuggestion(ctx, sc.URL, sc.Branch)
	if err != nil {
		return opsorgcmd.Report{}, orgFileError(err, sc.URL)
	}
	cur := sg.Doc.Own
	if in.Key == opsorgcmd.ConfigKeyRepos {
		cur = sg.Doc.Repos
	}
	next := opsorgcmd.NewList(cur, in)
	file := orgcfg.OrgFilePath(sc.OrgFolder) + " in " + sc.URL
	lines := []string{"set " + in.Key + " of org " + in.Org + " (" + file + "):",
		"  was: " + orNoneList(cur), "  now: " + orNoneList(next)}
	if in.Key == opsorgcmd.ConfigKeyRepos {
		lines = append(lines, "  a repo a repos: pattern matches is a client repo (leak guards on), even when an own: pattern matches it too")
	}
	if !yes {
		return opsorgcmd.Report{Lines: lines, Ask: "Change it?"}, nil
	}
	res, err := g.EditOrgFile(ctx, orgcfg.OrgFileEdit{URL: sc.URL, OrgFolder: sc.OrgFolder, Branch: sc.Branch, Org: in.Org,
		Key: in.Key, Values: next, Push: in.Push, Signature: storeSignature(o)})
	if err != nil {
		return opsorgcmd.Report{Lines: lines}, orgFileError(err, sc.URL)
	}
	if res.Existed {
		lines = append(lines, "· "+in.Key+" in "+file+" already holds that list; nothing committed")
	} else {
		lines = append(lines, "✓ "+in.Key+" in "+file+" changed"+audiencePushNote(res, cmd))
	}
	sg, err = g.LocalSuggestion(ctx, sc.URL, sc.Branch)
	if err != nil {
		return opsorgcmd.Report{Lines: lines}, orgFileError(err, sc.URL)
	}
	mine, _ := mineOf(ctx, cfg, o, sg)
	if err := writeOrgSource(h.home, configLockWait, in.Org, mine, sg.Source()); err != nil {
		return opsorgcmd.Report{Lines: lines}, err
	}
	lines = append(lines, "✓ org entry "+in.Org+" copies it (version "+short(sg.Version)+")")
	return opsorgcmd.Report{Lines: lines}, nil
}

// orNoneList is a pattern list as one line, "(none)" when empty.
func orNoneList(v []string) string {
	if len(v) == 0 {
		return "(none)"
	}
	return strings.Join(v, ", ")
}

// compile-time check: the org host offers org config.
var _ opsorgcmd.ConfigEditor = (*standaloneOrg)(nil)
