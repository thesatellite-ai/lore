package wire

// org_create_standalone.go — `lore org create|import|rename` on cmd/lore
// (P3-ORG-G-2; CLI_REFERENCE 2.26, CONFIG_GUIDE 6.4, PROV 11.5): the
// v0.1.17 bodies (saas org_create_cmd.go, org_import_cmd.go,
// org_rename_cmd.go) ported onto the core: lore-org.yaml goes through the
// shared bare clones (adapters/orgcfg WriteOrgFile / ReadSuggestion), the
// entry into ~/.lore/config.yaml through one validated node edit, and the
// roster row through a fresh App pinned to the new entry with LORE_ORG.
//
//	create ─▶ --store → url (stores.yaml name or url) ─▶ entry checks ─▶
//	          plan + notes ─▶ not confirmed? ask ─▶ WriteOrgFile ─▶ add
//	          entry ─▶ register on the roster (a failure warns: the org
//	          exists)
//	import ─▶ ReadSuggestion ─▶ entry (suggestion + your identity) ─▶ plan
//	          + notes ─▶ ask ─▶ add entry ─▶ register (a failure keeps the
//	          entry and fails with the cause's code, BUG-T3)
//	rename ─▶ entry exists ─▶ the org folder's prj_ id when attached here
//	          ─▶ name, id:, repo_orgs, defaults.client_repos in one write

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/yamlfmt"
	"github.com/thesatellite-ai/lore/model/errs"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/store"
)

// org create/import constants (v0.1.17 wording).
const (
	orgKeyID       = "id"
	orgKeyDefaults = "defaults"
	orgKeyClient   = "client_repos"
	orgKeyOrg      = "org"
	// planIndent indents the entry printed in a plan.
	planIndent = "    "
	askCreate  = "Create it?"
	askImport  = "Add it?"
	askRisky   = "Accept the notes above and add it?"
)

// orgRegistrar builds the App that registers the caller on a new
// entry's roster (the binary's own repo, home, folder and version).
type orgRegistrar struct {
	repoDir, home, workDir, version string
}

// register puts the caller on entry's roster: a fresh App (it reads the
// config.yaml just written) resolving with LORE_ORG=entry, as v0.1.17's
// registerAfterImport did; outside any repo a store-only App on the org
// folder target (BUG-T3). It returns the `lore actor register` line.
func (r orgRegistrar) register(ctx context.Context, entry, orgFolder string, wall func() time.Time) (string, error) {
	prev, had := os.LookupEnv(orgcfg.EnvOrg)
	if err := os.Setenv(orgcfg.EnvOrg, entry); err != nil {
		return "", errs.Wrap(errs.Internal, "wire: set "+orgcfg.EnvOrg, err)
	}
	defer func() {
		if had {
			_ = os.Setenv(orgcfg.EnvOrg, prev) // restoring the caller's environment
		} else {
			_ = os.Unsetenv(orgcfg.EnvOrg) // restoring the caller's environment
		}
	}()
	opts := StandaloneOptions(r.repoDir, r.home, r.workDir, r.version)
	opts.Wall = wall // the binary's clock (LORE_NOW in a test build)
	app, alone, err := StoreOnlyRoster(ctx, opts, store.Source(entry+orgconf.RefSep+orgFolder))
	if err != nil {
		return "", err
	}
	if !alone {
		if app, err = New(opts); err != nil {
			return "", err
		}
	}
	defer func() { _ = app.Close() }() // the roster write has saved by return
	out, err := app.ActorRegister(ctx, opsinfra.ActorRegisterIn{})
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out.Text(), "\n"), nil
}

// orgEntryLines is the plan's entry block.
func orgEntryLines(header string, o orgconf.Org) ([]string, error) {
	b, err := yaml.Marshal(o)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: encode the entry", err)
	}
	lines := []string{header, ""}
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		lines = append(lines, planIndent+l)
	}
	lines = append(lines, "")
	for _, n := range riskyEntry(o) {
		lines = append(lines, "  note: "+n)
	}
	return lines, nil
}

// riskyEntry lists what the user must accept explicitly: a default that
// writes into client repos, and owner wildcards that match whole hosts.
func riskyEntry(o orgconf.Org) []string {
	var out []string
	for kind, where := range o.Defaults.ClientRepos {
		if where == orgconf.TargetRepo {
			out = append(out, fmt.Sprintf("defaults.client_repos.%s = repo: %s written in client repos go INTO the client repo", kind, kind))
		}
	}
	for _, p := range append(append([]string{}, o.Own...), o.Repos...) {
		segs := strings.Split(p, "/")
		if len(segs) >= 2 && strings.Contains(segs[1], "*") {
			out = append(out, "pattern "+p+" has a wildcard owner: it matches every account on that host")
		}
	}
	return out
}

// needIdentity refuses an entry without --key and --name.
func needIdentity(o orgconf.Org) error {
	if o.Identity.Key == "" || o.Identity.Name == "" {
		return errs.New(errs.InvalidInput, "your identity in "+o.Name+" is required: --key human:<your email there> --name \"Your Name\" [--handle h]")
	}
	return nil
}

// wall is the binary's clock: the one cmd/lore put on the resolution
// (OrgResolution.Now, LORE_NOW in a test build), else the wall clock.
func (h *standaloneOrg) wall() func() time.Time {
	if h.res != nil && h.res.Now != nil {
		return h.res.Now
	}
	return time.Now
}

// loadOrgConfig reads ~/.lore/config.yaml for an org command.
func (h *standaloneOrg) loadOrgConfig() (*orgconf.Config, error) {
	cfg, _, err := orgcfg.LoadConfig(h.home)
	return cfg, err
}

// createStoreURL turns --store into a store url: the entry of that name in
// the repo's .lore/stores.yaml (a single-folder entry is refused: an org
// needs the whole store), else the argument when it looks like a url.
func (h *standaloneOrg) createStoreURL(ctx context.Context, arg string) (string, error) {
	if top := gitTop(ctx, h.res.dir); top != "" {
		c, err := readStoresFile(top)
		if err != nil {
			return "", errs.Wrap(errs.ConfigInvalid, ".lore/stores.yaml is not valid", err)
		}
		for _, s := range c.Stores {
			if string(s.Name) != arg {
				continue
			}
			if s.Folder != "" {
				return "", errs.New(errs.InvalidInput, "store "+arg+" attaches one folder ("+s.Folder+"); an org needs the whole store").
					WithHint("pass the store's url with --store")
			}
			return s.URL, nil
		}
	}
	if strings.ContainsAny(arg, ":/") {
		return arg, nil
	}
	return "", errs.New(errs.InvalidInput, "no store named "+arg+" in this repo's .lore/stores.yaml").
		WithHint("make one with `lore store init " + arg + " <url>`, or pass the store url")
}

// Create implements opsorgcmd.Host.
func (h *standaloneOrg) Create(ctx context.Context, in opsorgcmd.CreateIn, yes bool) (opsorgcmd.Report, error) {
	url, err := h.createStoreURL(ctx, in.Store)
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	typed := canonicalTyped(ctx, url) // brief 3.7: lore-org.yaml names the canonical url
	url = typed.URL
	cfg, err := h.loadOrgConfig()
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	if cfg.FindOrg(in.Org) != nil {
		return opsorgcmd.Report{}, errs.New(errs.InvalidInput, "an org entry named "+in.Org+" already exists in ~/.lore/config.yaml").
			WithHint("pick another name, or link repos to the existing org with `lore org link`")
	}
	entry := orgconf.Org{Name: in.Org, Own: in.Own, Repos: in.Repos,
		Stores:   []orgconf.Store{{URL: url, OrgFolder: in.OrgFolder}},
		Identity: orgconf.OrgIdentity{Key: in.Key, Name: in.Name, Handle: in.Handle}}
	if ierr := needIdentity(entry); ierr != nil {
		return opsorgcmd.Report{}, ierr
	}
	lines, err := orgEntryLines(fmt.Sprintf("This writes %s into the store (no identity) and adds to ~/.lore/config.yaml:", orgcfg.OrgFilePath(in.OrgFolder)), entry)
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	if n := typed.Note(); n != "" {
		lines = append(lines, "  "+n)
	}
	if !yes {
		return opsorgcmd.Report{Lines: lines, Ask: askCreate}, nil
	}
	if err := recordAccess(h.home, typed); err != nil { // before the store is reached: the route needs it
		return opsorgcmd.Report{Lines: lines}, err
	}
	// The entry must validate before anything reaches the store.
	if _, verr := orgConfigWith(h.home, func(root *yaml.Node) error { return appendOrgNode(root, entry) }); verr != nil {
		return opsorgcmd.Report{Lines: lines}, verr
	}
	doc := orgcfg.OrgFileDoc{Name: in.Org, Own: in.Own, Repos: in.Repos,
		Stores: []orgcfg.OrgFileStore{{URL: url, OrgFolder: in.OrgFolder}}}
	g := &orgcfg.GitLinks{Home: h.home, Config: cfg, Now: h.wall(), Route: h.router()}
	res, err := g.WriteOrgFile(ctx, orgcfg.OrgFileWrite{URL: url, OrgFolder: in.OrgFolder, Doc: doc, Signature: storeSignature(&entry)})
	if err != nil {
		return opsorgcmd.Report{Lines: lines}, orgFileError(err, url)
	}
	file := orgcfg.OrgFilePath(in.OrgFolder)
	sg, serr := g.LocalSuggestion(ctx, url, "")
	switch {
	case res.Existed:
		lines = append(lines, fmt.Sprintf("✓ the store already suggests org %s (%s kept)", in.Org, file))
		if serr == nil {
			// B2-19: the entry is the file's (every store, own and repos),
			// as org import builds it for a member; not the one store and
			// patterns typed here, which would make the org's own repos
			// client repos on this machine only.
			suggestedStores(&entry, sg, url)
			if _, verr := orgConfigWith(h.home, func(root *yaml.Node) error { return appendOrgNode(root, entry) }); verr != nil {
				return opsorgcmd.Report{Lines: lines}, verr
			}
			lines = append(lines, "  the entry takes the stores, own and repos of the store's "+file)
		}
	case res.Offline:
		lines = append(lines, fmt.Sprintf("✓ wrote %s (committed in the local clone; offline, pushed by the next command)", file))
	case !res.Pushed:
		lines = append(lines, fmt.Sprintf("✓ wrote %s (committed in the local clone; `lore sync` pushes it)", file))
	default:
		lines = append(lines, fmt.Sprintf("✓ wrote %s (pushed)", file))
	}
	if serr == nil && sg.Version != "" {
		src := sg.Source() // SOURCE_OF_TRUTH row 9: the entry copies the file just written
		entry.Source = &src
	}
	if err := h.addOrgEntry(entry); err != nil {
		return opsorgcmd.Report{Lines: lines}, err
	}
	lines = append(lines, "✓ added org entry "+in.Org)
	regFolder := in.OrgFolder
	if sc := entry.OrgFolderStore(); sc != nil {
		regFolder = sc.OrgFolder // the file's org folder when the entry is the file's (B2-19)
	}
	if line, rerr := h.reg.register(ctx, in.Org, regFolder, h.wall()); rerr != nil {
		// v0.1.17: the org exists; registering is finished by hand.
		lines = append(lines, "  warning: the org is created, but registering on the roster failed: "+rerr.Error(),
			"  run `LORE_ORG="+in.Org+" lore actor register` in one of "+in.Org+"'s repos or outside any repo")
	} else if line != "" {
		lines = append(lines, line)
	}
	lines = append(lines, "  teammates join with: lore org import "+url+" --key human:<their email> --name \"Their Name\"",
		"  tie a client repo to it (written only into the store): lore org link "+in.Org+" --project <folder>")
	return opsorgcmd.Report{Lines: lines}, nil
}

// suggestedStores fills entry from the store's lore-org.yaml sg the way
// org import (and so a member's join) builds it: own and repos patterns,
// every store it lists (the store at url, with sg's folder, when it lists
// none) and the client-repo defaults. org create on a store that already
// has the file uses it too (B2-19), so the admin who starts a team on an
// existing org gets the same entry as a member who joins it.
func suggestedStores(entry *orgconf.Org, sg orgcfg.Suggestion, url string) {
	entry.Own, entry.Repos, entry.Stores = sg.Doc.Own, sg.Doc.Repos, nil
	for _, s := range sg.Doc.Stores {
		entry.Stores = append(entry.Stores, orgconf.Store{URL: s.URL, Name: s.Name, Branch: s.Branch, OrgFolder: s.OrgFolder})
	}
	if len(entry.Stores) == 0 {
		entry.Stores = []orgconf.Store{{URL: url, OrgFolder: sg.Folder}}
	}
	if len(sg.Doc.Defaults.ClientRepos) > 0 {
		entry.Defaults.ClientRepos = sg.Doc.Defaults.ClientRepos
	}
}

// Import implements opsorgcmd.Host.
func (h *standaloneOrg) Import(ctx context.Context, in opsorgcmd.ImportIn, yes bool) (opsorgcmd.Report, error) {
	cfg, err := h.loadOrgConfig()
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	typed := canonicalTyped(ctx, in.URL) // brief 3.7: the entry names the canonical url
	in.URL = typed.URL
	g := &orgcfg.GitLinks{Home: h.home, Config: cfg, Now: h.wall(), Route: typed.withRule(h.router())}
	sg, err := g.ReadSuggestion(ctx, in.URL, in.Branch)
	if err != nil {
		return opsorgcmd.Report{}, orgFileError(err, in.URL)
	}
	entry := orgconf.Org{Name: sg.Doc.Name}
	if in.As != "" {
		entry.Name = in.As
	}
	if entry.Name == "" {
		return opsorgcmd.Report{}, errs.New(errs.InvalidInput, "the store's lore-org.yaml names no org; pass --as <name>")
	}
	if cfg.FindOrg(entry.Name) != nil {
		return opsorgcmd.Report{}, errs.New(errs.InvalidInput, "an org entry named "+entry.Name+" already exists in ~/.lore/config.yaml").
			WithHint("pass --as <another name>, or edit the existing entry")
	}
	suggestedStores(&entry, sg, in.URL)
	entry.Identity = orgconf.OrgIdentity{Key: in.Key, Name: in.Name, Handle: in.Handle}
	if ierr := needIdentity(entry); ierr != nil {
		return opsorgcmd.Report{}, ierr
	}
	lines, err := orgEntryLines("This adds to ~/.lore/config.yaml:", entry)
	if err != nil {
		return opsorgcmd.Report{}, err
	}
	if n := typed.Note(); n != "" {
		lines = append(lines, "  "+n)
	}
	if !yes {
		ask := askImport
		if len(riskyEntry(entry)) > 0 {
			ask = askRisky
		}
		return opsorgcmd.Report{Lines: lines, Ask: ask}, nil
	}
	if err := recordAccess(h.home, typed); err != nil { // confirmed: this machine keeps reaching the store
		return opsorgcmd.Report{Lines: lines}, err
	}
	if sg.Version != "" {
		src := sg.Source() // SOURCE_OF_TRUTH row 9: the entry copies this lore-org.yaml
		entry.Source = &src
	}
	if err := h.addOrgEntry(entry); err != nil {
		return opsorgcmd.Report{Lines: lines}, err
	}
	lines = append(lines, "✓ added org entry "+entry.Name)
	if in.Admit != nil {
		// setup join (owner item 19): the member or invite check reads the
		// roster before anything is written to the org's store; a refusal
		// takes the entry out again and registers nobody.
		if aerr := in.Admit(ctx, entry.Name); aerr != nil {
			if rerr := writeOrgConfig(h.home, func(root *yaml.Node) error { deleteOrgEntry(root, entry.Name); return nil }); rerr != nil {
				return opsorgcmd.Report{Lines: lines}, errors.Join(aerr, rerr)
			}
			return opsorgcmd.Report{}, aerr
		}
	}
	if in.SkipRegister { // an added member is on the roster already
		return opsorgcmd.Report{Lines: lines}, nil
	}
	folder := sg.Folder
	if sc := entry.OrgFolderStore(); sc != nil {
		folder = sc.OrgFolder
	}
	line, rerr := h.reg.register(ctx, entry.Name, folder, h.wall())
	if rerr != nil {
		// BUG-T3: a half-done import must not exit 0. The entry stays (it
		// is valid on its own); the cause's code says what failed.
		return opsorgcmd.Report{Lines: lines}, errs.Wrap(errs.CodeOf(rerr), "the org entry "+entry.Name+" is added to ~/.lore/config.yaml, but registering on its roster failed: "+rerr.Error(), rerr).
			WithHint("run `LORE_ORG=" + entry.Name + " lore actor register` in one of " + entry.Name + "'s repos or outside any repo (BUG-T3)")
	}
	if line != "" {
		lines = append(lines, line)
	}
	return opsorgcmd.Report{Lines: lines}, nil
}

// Rename implements opsorgcmd.Host: the label change of PROV 11.5.
func (h *standaloneOrg) Rename(ctx context.Context, in opsorgcmd.RenameIn) (opsorgcmd.RenameOut, error) {
	cfg, err := h.loadOrgConfig()
	if err != nil {
		return opsorgcmd.RenameOut{}, err
	}
	o := cfg.FindOrg(in.Old)
	if o == nil {
		return opsorgcmd.RenameOut{}, errs.New(errs.NotFound, fmt.Sprintf("no org entry named %q", in.Old)).
			WithHint(fmt.Sprintf("entries: %v", cfg.OrgNames()))
	}
	out := opsorgcmd.RenameOut{From: in.Old, To: in.New, ID: h.reg.orgFolderID(ctx, o), Config: configPath(h.home)}
	err = writeOrgConfig(h.home, func(root *yaml.Node) error {
		return renameOrgNode(root, in.Old, in.New, out.ID, &out)
	})
	if err != nil {
		if c := errs.CodeOf(err); c != errs.InvalidInput && c != errs.ConfigInvalid {
			return opsorgcmd.RenameOut{}, err // not found, or the write failed
		}
		return opsorgcmd.RenameOut{}, errs.Wrap(errs.InvalidInput, "org rename refused: "+err.Error(), err)
	}
	out.Lines = orgRenameLines(out)
	return out, nil
}

// orgFolderID is the prj_ id of o's org folder when this repo attaches it
// ("<org>/<org folder>"), else "": the id is recorded the next time the
// rename runs where the org is attached, and the label change is local.
func (r orgRegistrar) orgFolderID(ctx context.Context, o *orgconf.Org) string {
	sc := o.OrgFolderStore()
	if sc == nil || gitTop(ctx, r.dir()) == "" {
		return ""
	}
	app, err := New(StandaloneOptions(r.repoDir, r.home, r.workDir, r.version))
	if err != nil {
		return ""
	}
	defer func() { _ = app.Close() }() // read only
	if _, err := app.state(ctx); err != nil {
		return ""
	}
	ref := app.DescribeSource(store.Source(o.Name + orgconf.RefSep + sc.OrgFolder))
	if ref.FolderID.IsZero() {
		return ""
	}
	return ref.FolderID.String()
}

// dir is the folder the binary runs in.
func (r orgRegistrar) dir() string {
	if r.workDir != "" {
		return r.workDir
	}
	return r.repoDir
}

// orgRenameLines is the human output of `lore org rename` (v0.1.17).
func orgRenameLines(o opsorgcmd.RenameOut) []string {
	lines := []string{fmt.Sprintf("✓ org entry %s is now %s (this machine)", o.From, o.To)}
	switch {
	case o.IDRecorded:
		lines = append(lines, fmt.Sprintf("  id %s recorded (the org folder's project id; it never changes)", o.ID))
	case o.ID == "":
		lines = append(lines, "  id not recorded: this repo does not attach the org; run it inside one of its repos to record it")
	}
	if o.RepoOrgs > 0 {
		lines = append(lines, fmt.Sprintf("  %d project choice(s) (lore org use) moved to %s", o.RepoOrgs, o.To))
	}
	if o.Defaults > 0 {
		lines = append(lines, fmt.Sprintf("  %d defaults.client_repos target(s) moved to %s", o.Defaults, o.To))
	}
	return append(lines, "  rows are unchanged; store rows that name the old label (routing.client_repos) and other machines keep it until edited there",
		"  written to "+o.Config)
}

// renameOrgNode renames entry oldName to newName in the config tree,
// records id (refusing an entry whose id differs: another org) and moves
// the entry's defaults.client_repos targets and every repo_orgs choice.
func renameOrgNode(root *yaml.Node, oldName, newName, id string, out *opsorgcmd.RenameOut) error {
	orgs := mapGet(root, configKeyOrgs)
	var entry *yaml.Node
	if orgs != nil && orgs.Kind == yaml.SequenceNode {
		for _, item := range orgs.Content {
			n := mapGet(item, configKeyName)
			if item.Kind != yaml.MappingNode || n == nil {
				continue
			}
			if n.Value == newName && newName != oldName {
				return errs.New(errs.InvalidInput, fmt.Sprintf("~/.lore/config.yaml already has an org entry named %q", newName))
			}
			if n.Value == oldName {
				entry = item
			}
		}
	}
	if entry == nil {
		return errs.New(errs.NotFound, fmt.Sprintf("no org entry named %q", oldName))
	}
	mapGet(entry, configKeyName).Value = newName
	if id != "" {
		switch cur := mapGet(entry, orgKeyID); {
		case cur == nil:
			mapSet(entry, orgKeyID, scalar(id))
			out.IDRecorded = true
		case cur.Value != id:
			return errs.New(errs.InvalidInput, fmt.Sprintf("org entry %q has id %s, not %s: it is another org", oldName, cur.Value, id))
		}
	}
	if d := mapGet(entry, orgKeyDefaults); d != nil && d.Kind == yaml.MappingNode {
		if cr := mapGet(d, orgKeyClient); cr != nil && cr.Kind == yaml.MappingNode {
			for i := 1; i < len(cr.Content); i += 2 {
				v := cr.Content[i]
				org, folder, has := strings.Cut(v.Value, orgconf.RefSep)
				if v.Kind != yaml.ScalarNode || org != oldName {
					continue
				}
				v.Value = newName
				if has {
					v.Value += orgconf.RefSep + folder
				}
				out.Defaults++
			}
		}
	}
	if m := mapGet(root, configKeyRepoOrgs); m != nil && m.Kind == yaml.MappingNode {
		for i := 1; i < len(m.Content); i += 2 {
			v := m.Content[i]
			switch v.Kind {
			case yaml.ScalarNode:
				if v.Value == oldName {
					v.Value = newName
					out.RepoOrgs++
				}
			case yaml.MappingNode:
				if o := mapGet(v, orgKeyOrg); o != nil && o.Value == oldName {
					o.Value = newName
					out.RepoOrgs++
				}
			case yaml.DocumentNode, yaml.SequenceNode, yaml.AliasNode:
				// not a repo_orgs value lore writes (a name or an {org, ...} map): left as it is
			}
		}
	}
	return nil
}

// appendOrgNode adds entry under orgs: (created when absent).
func appendOrgNode(root *yaml.Node, entry orgconf.Org) error {
	orgs := mapGet(root, configKeyOrgs)
	if orgs == nil || orgs.Kind != yaml.SequenceNode {
		orgs = &yaml.Node{Kind: yaml.SequenceNode}
		mapSet(root, configKeyOrgs, orgs)
	}
	item := &yaml.Node{}
	if err := item.Encode(entry); err != nil {
		return errs.Wrap(errs.Internal, "wire: encode the entry", err)
	}
	orgs.Content = append(orgs.Content, item)
	return nil
}

// addOrgEntry writes entry into ~/.lore/config.yaml (comments and the rest
// of the document kept), only when the whole file still validates.
func (h *standaloneOrg) addOrgEntry(entry orgconf.Org) error {
	return writeOrgConfig(h.home, func(root *yaml.Node) error { return appendOrgNode(root, entry) })
}

// writeOrgConfig applies edit to config.yaml and writes the result when
// it validates (orgConfigWith), under the config.yaml lock every other
// edit takes (lockMachineConfig): the read, the edit and the atomic write
// are one step, so an org add or rename running beside a setup save, a
// lore config set or another org command never writes over that edit
// with its older copy of the file.
func writeOrgConfig(home string, edit func(root *yaml.Node) error) (err error) {
	release, err := lockMachineConfig(home)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	b, err := orgConfigWith(home, edit)
	if err != nil {
		return err
	}
	return writeMachineConfigFile(configPath(home), b, configFileMode)
}

// orgConfigWith returns config.yaml with edit applied, refusing a result
// that does not validate (nothing is written here: the caller writes, or
// only checks). The same parse every command runs decides.
func orgConfigWith(home string, edit func(root *yaml.Node) error) ([]byte, error) {
	var out []byte
	err := editMachineConfigBytes(home, edit, func(b []byte) error {
		if _, perr := orgconf.Parse(b); perr != nil {
			return errs.Wrap(errs.ConfigInvalid, "the entry does not validate; nothing changed: "+perr.Error(), perr)
		}
		out = b
		return nil
	})
	return out, err
}

// orgFileError maps a lore-org.yaml read or write error onto a lore code.
func orgFileError(err error, url string) error {
	switch {
	case errors.Is(err, orgcfg.ErrOrgFileExists):
		return errs.New(errs.InvalidInput, err.Error()).
			WithHint("that store already suggests an org: adopt it with `lore org import " + url + "`")
	case errors.Is(err, orgcfg.ErrNoOrgFile):
		return errs.New(errs.InvalidInput, err.Error()).
			WithHint("ask the team to commit <org_folder>/lore-org.yaml, or add the entry to ~/.lore/config.yaml by hand")
	}
	return linkError(err)
}

// editMachineConfigBytes loads config.yaml as a node tree (a missing file
// starts empty), applies edit and hands the encoded result to use; it
// writes nothing itself.
func editMachineConfigBytes(home string, edit func(root *yaml.Node) error, use func([]byte) error) error {
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	b, err := readConfigRaw(home)
	switch {
	case err != nil:
		return err
	case b == nil:
	default:
		if err = yamlfmt.Decode(b, doc); err != nil {
			return errs.Wrap(errs.ConfigInvalid, "wire: ~/.lore/"+ConfigFile+" is not valid YAML", err)
		}
	}
	if len(doc.Content) == 0 {
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return errs.New(errs.ConfigInvalid, "wire: ~/.lore/"+ConfigFile+" is not a mapping")
	}
	if err = edit(root); err != nil {
		return err
	}
	// Written like every other config.yaml edit: the file's own indent and
	// block style for nested lists and mappings (logic/yamlfmt).
	out, err := yamlfmt.EncodeLike(doc, b)
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode "+ConfigFile, err)
	}
	return use(out)
}
