package wire

// orgsource.go — the org entry in ~/.lore/config.yaml as a stamped copy
// of the store's lore-org.yaml (SOURCE_OF_TRUTH row 9). lore-org.yaml is
// the source of stores, own, repos and defaults; the entry keeps its
// machine-local fields (label, identity, live, …) and carries
// `source: {store: sto_…, version: <blob id>}`.
//
//	refresh trigger: every core open (afterAttach) compares each entry's
//	stamp with lore-org.yaml in the org store's shared clone as last
//	fetched (no network); a fetch that brought a newer file makes the
//	next open copy it in:
//	  stale stamp            ─▶ copy the shared fields + new stamp
//	  no stamp, fields equal ─▶ stamp only
//	  no stamp, fields differ ─▶ left for the person: doctor warns, the
//	                             fix is `lore org refresh <org>`
//	doctor org_source: the stale and unstamped-different entries, fix
//	`lore org refresh <org>` (Refresh: the same copy, run on purpose)

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/store"
)

// Config keys of the copied fields.
const (
	orgKeyStores = "stores"
	orgKeyOwn    = "own"
	orgKeyRepos  = "repos"
	orgKeySource = "source"
)

// orgRefreshLockWait bounds the automatic refresh's wait for the config
// lock: it is best effort and must never stall a command (the next open
// tries again).
const orgRefreshLockWait = 200 * time.Millisecond

// afterAttach runs the source-of-truth steps of an open (openAttached):
// refresh the org entries from their stores' lore-org.yaml, then refuse
// writes to a second address of a store id.
func afterAttach(ctx context.Context, home, top string, opened []attach.Opened, urls map[store.Source]string) {
	refreshOrgSources(ctx, home)
	markSplitAddresses(home, top, opened, urls)
}

// orgSuggestionLocal is the lore-org.yaml org o's store holds on this
// machine (the org folder's store, else the first): ok is false when the
// store has no local copy or no lore-org.yaml.
func orgSuggestionLocal(ctx context.Context, home string, cfg *orgconf.Config, o *orgconf.Org) (orgcfg.Suggestion, bool) {
	sc := o.OrgFolderStore()
	if sc == nil {
		if len(o.Stores) == 0 {
			return orgcfg.Suggestion{}, false
		}
		sc = &o.Stores[0]
	}
	g := &orgcfg.GitLinks{Home: home, Config: cfg}
	sg, err := g.LocalSuggestion(ctx, sc.URL, sc.Branch)
	if err != nil || sg.Version == "" {
		return orgcfg.Suggestion{}, false
	}
	return sg, true
}

// refreshOrgSources is the automatic refresh (see the file comment). Best
// effort: any failure leaves the entry for the doctor.
func refreshOrgSources(ctx context.Context, home string) {
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil {
		return
	}
	for i := range cfg.Orgs {
		o := &cfg.Orgs[i]
		sg, ok := orgSuggestionLocal(ctx, home, cfg, o)
		if !ok {
			continue
		}
		switch orgconf.StateOf(o, sg.StoreID, sg.Version) {
		case orgconf.SourceCurrent:
			continue
		case orgconf.SourceStale:
		case orgconf.SourceUnstamped, orgconf.SourceOtherStore:
			if !orgconf.SharedOf(o).Equal(sg.Shared()) {
				continue // the person decides: doctor, lore org refresh
			}
		}
		_ = writeOrgSource(home, orgRefreshLockWait, o.Name, sg.Shared(), sg.Source()) // best effort, see above
	}
}

// writeOrgSource writes the copied fields and the stamp into org entry
// name, under the config lock (waiting at most wait), only when the whole
// file still validates.
func writeOrgSource(home string, wait time.Duration, name string, sh orgconf.Shared, src orgconf.OrgSource) (err error) {
	release, err := rowfs.LockFile(context.Background(), filepath.Join(home, configLockFile), wait)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	b, err := orgConfigWith(home, func(root *yaml.Node) error { return setOrgSourceNode(root, name, sh, src) })
	if err != nil {
		return err
	}
	return writeMachineConfigFile(configPath(home), b, configFileMode)
}

// orgEntryNode is the orgs[] item named name, or nil.
func orgEntryNode(root *yaml.Node, name string) *yaml.Node {
	orgs := mapGet(root, configKeyOrgs)
	if orgs == nil || orgs.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range orgs.Content {
		if n := mapGet(item, configKeyName); item.Kind == yaml.MappingNode && n != nil && n.Value == name {
			return item
		}
	}
	return nil
}

// setOrgSourceNode sets the copied fields and the stamp of org entry name.
func setOrgSourceNode(root *yaml.Node, name string, sh orgconf.Shared, src orgconf.OrgSource) error {
	entry := orgEntryNode(root, name)
	if entry == nil {
		return errs.New(errs.NotFound, "no org entry named "+name+" in ~/.lore/"+ConfigFile)
	}
	if err := setOrgKey(entry, orgKeyStores, sh.Stores, len(sh.Stores) == 0); err != nil {
		return err
	}
	if err := setOrgKey(entry, orgKeyOwn, sh.Own, len(sh.Own) == 0); err != nil {
		return err
	}
	if err := setOrgKey(entry, orgKeyRepos, sh.Repos, len(sh.Repos) == 0); err != nil {
		return err
	}
	if err := setOrgKey(entry, orgKeyDefaults, sh.Defaults, len(sh.Defaults.ClientRepos) == 0); err != nil {
		return err
	}
	return setOrgKey(entry, orgKeySource, src, false)
}

// orgKeyValue is a value setOrgSourceNode writes.
type orgKeyValue interface {
	[]orgconf.Store | []string | orgconf.OrgDefaults | orgconf.OrgSource
}

// setOrgKey sets key of entry to v (removed when empty).
func setOrgKey[T orgKeyValue](entry *yaml.Node, key string, v T, empty bool) error {
	if empty {
		mapDelete(entry, key)
		return nil
	}
	n := &yaml.Node{}
	if err := n.Encode(v); err != nil {
		return errs.Wrap(errs.Internal, "wire: encode org entry "+key, err)
	}
	mapSet(entry, key, n)
	return nil
}

// DoctorCheckOrgSource is the "org entry older than the store's
// suggestion" check of `lore doctor` (SOURCE_OF_TRUTH row 9).
const DoctorCheckOrgSource DoctorCheck = "org_source"

// DoctorOrgSources warns about each org entry whose copied fields are
// older than (or, unstamped, differ from) the store's lore-org.yaml.
func (a *App) DoctorOrgSources(ctx context.Context) ([]DoctorFinding, error) {
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return nil, nil // lore config check reports a broken config
	}
	var out []DoctorFinding
	for i := range cfg.Orgs {
		o := &cfg.Orgs[i]
		sg, ok := orgSuggestionLocal(ctx, a.opts.Home, cfg, o)
		if !ok {
			continue
		}
		msg := ""
		switch orgconf.StateOf(o, sg.StoreID, sg.Version) {
		case orgconf.SourceCurrent:
			continue
		case orgconf.SourceStale:
			msg = "org entry " + o.Name + " is older than the store's suggestion (" + orgFileShown(sg) + " is at version " + short(sg.Version) + ", the entry copied " + short(o.Source.Version) + ")"
		case orgconf.SourceUnstamped, orgconf.SourceOtherStore:
			if orgconf.SharedOf(o).Equal(sg.Shared()) {
				continue // stamped by the next open
			}
			msg = "org entry " + o.Name + " differs from the store's suggestion " + orgFileShown(sg) + " and records no source version (stores, own, repos or defaults were changed by hand or by an older lore)"
		}
		out = append(out, DoctorFinding{Check: DoctorCheckOrgSource, Level: DoctorWarning, Message: msg, Fix: "lore org refresh --org " + o.Name})
	}
	return out, nil
}

// orgFileShown names lore-org.yaml in its store.
func orgFileShown(sg orgcfg.Suggestion) string {
	p := sg.Path
	if p == "" {
		p = orgcfg.OrgFilePath(sg.Folder)
	}
	return p + " in " + sg.StoreURL
}

// short is a blob id's first 7 characters.
func short(v string) string {
	if len(v) > 7 {
		return v[:7]
	}
	return v
}

// Refresh implements opsorgcmd.Refresher: fetch the org store, read its
// lore-org.yaml and copy stores, own, repos and defaults into the entry
// with the new stamp (the doctor's fix for org_source).
func (h *standaloneOrg) Refresh(ctx context.Context, in opsorgcmd.RefreshIn) (opsorgcmd.Report, error) {
	cfg, err := h.loadOrgConfig()
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	o := cfg.FindOrg(in.Org)
	if o == nil {
		return opsorgcmd.Report{}, errs.New(errs.NotFound, "no org entry named "+in.Org+" in ~/.lore/config.yaml").
			WithHint("entries: lore config check lists them; join one with lore org import <url>")
	}
	sc := o.OrgFolderStore()
	if sc == nil {
		if len(o.Stores) == 0 {
			return opsorgcmd.Report{}, errs.New(errs.InvalidInput, "org entry "+in.Org+" lists no store, so there is no lore-org.yaml to refresh from").
				WithHint("add the store to the entry, or import the org again: lore org import --url <store-url>")
		}
		sc = &o.Stores[0]
	}
	g := &orgcfg.GitLinks{Home: h.home, Config: cfg, Now: h.wall(), Route: h.router()}
	sg, err := g.ReadSuggestion(ctx, sc.URL, sc.Branch)
	if err != nil {
		return opsorgcmd.Report{}, orgFileError(err, sc.URL)
	}
	before := orgconf.SharedOf(o)
	state := orgconf.StateOf(o, sg.StoreID, sg.Version)
	if state == orgconf.SourceCurrent && before.Equal(sg.Shared()) {
		return opsorgcmd.Report{Lines: []string{"org entry " + in.Org + " already matches " + orgFileShown(sg) + " (version " + short(sg.Version) + "); nothing changed"}}, nil
	}
	if err := writeOrgSource(h.home, configLockWait, in.Org, sg.Shared(), sg.Source()); err != nil {
		return opsorgcmd.Report{}, err
	}
	lines := []string{"✓ org entry " + in.Org + " now copies " + orgFileShown(sg) + " (version " + short(sg.Version) + ")"}
	after := sg.Shared()
	for i := range after.Stores {
		if i >= len(before.Stores) || before.Stores[i].URL != after.Stores[i].URL {
			lines = append(lines, "  store "+after.Stores[i].URL+" (was "+urlAt(before.Stores, i)+")")
		}
	}
	return opsorgcmd.Report{Lines: lines}, nil
}

// urlAt is the url of stores[i], or "none".
func urlAt(stores []orgconf.Store, i int) string {
	if i < len(stores) {
		return stores[i].URL
	}
	return "none"
}

// compile-time check: the org host offers org.refresh.
var _ opsorgcmd.Refresher = (*standaloneOrg)(nil)
