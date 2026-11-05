// org_import_cmd.go — `lore org import <url>` (§5.2, ADR TEAM-031): adopt
// the org entry a team suggests in its store (<org_folder>/.lore/
// lore-org.yaml: stores, patterns, defaults — never an identity), add it to
// ~/.lore/config.yaml with YOUR identity, and register you on the roster in
// the same step. It shows the entry first, refuses a name already used,
// pins the store URLs as written in the store, and asks before accepting a
// `repo` default or a wildcard owner pattern.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/stores"

	"github.com/thesatellite-ai/lore/model/errs"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// runOrgImport is org.import: read the store's suggestion, print the
// entry, and (confirmed) add it and join the roster. It returns the
// question when it stopped unconfirmed (nothing written).
func runOrgImport(ctx context.Context, w *bytes.Buffer, in opsorgcmd.ImportIn, yes bool) (string, error) {
	url, branch := in.URL, in.Branch
	cfg, err := loadTeamConfig(ctx)
	if err != nil {
		return "", err
	}
	m, err := newStoreManager(cfg, nil)
	if err != nil {
		return "", err
	}
	st, err := m.Open(ctx, url)
	if err != nil {
		return "", storeCLIError(err, "open the store")
	}
	fr, err := st.Fetch(ctx, true)
	if err != nil {
		return "", storeCLIError(err, "fetch the store")
	}
	if fr.Offline {
		return "", storeCLIError(fmt.Errorf("%w: %s", stores.ErrOffline, st.Norm), "fetch the store")
	}
	if branch == "" {
		branch = st.DefaultBranch()
	}
	if checkErr := st.Check(ctx, branch); checkErr != nil {
		return "", storeCLIError(checkErr, "check the store")
	}
	sugg, orgFolder, err := readSuggestion(ctx, st, branch)
	if err != nil {
		return "", err
	}
	entry := lorecfg.Org{Name: sugg.Name, Own: sugg.Own, Repos: sugg.Repos}
	if in.As != "" {
		entry.Name = in.As
	}
	if entry.Name == "" {
		return "", errcodes.New(errcodes.InvalidInput, "the store's lore-org.yaml names no org; pass --as <name>")
	}
	if cfg.FindOrg(entry.Name) != nil {
		return "", errcodes.New(errcodes.InvalidInput, "an org entry named "+entry.Name+" already exists in ~/.lore/config.yaml").
			WithHint("pass --as <another name>, or edit the existing entry")
	}
	for _, s := range sugg.Stores {
		entry.Stores = append(entry.Stores, lorecfg.Store{URL: s.URL, Branch: s.Branch, OrgFolder: s.OrgFolder})
	}
	if len(entry.Stores) == 0 {
		entry.Stores = []lorecfg.Store{{URL: url, OrgFolder: orgFolder}}
	}
	if len(sugg.Defaults.ClientRepos) > 0 {
		entry.Defaults.ClientRepos = sugg.Defaults.ClientRepos
	}
	entry.Identity = lorecfg.OrgIdentity{Key: in.Key, Name: in.Name, Handle: in.Handle}
	if entry.Identity.Key == "" || entry.Identity.Name == "" {
		return "", errcodes.New(errcodes.InvalidInput, "your identity in "+entry.Name+" is required: --key human:<your email there> --name \"Your Name\" [--handle h]")
	}
	body, err := yaml.Marshal(entry)
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "encode the entry").WithCause(err)
	}
	fmt.Fprintf(w, "This adds to ~/.lore/config.yaml:\n\n%s\n", indentBlock(string(body)))
	risky := riskyImport(entry)
	for _, r := range risky {
		fmt.Fprintln(w, "  note: "+r)
	}
	if !yes {
		if len(risky) > 0 {
			return "Accept the notes above and add it?", nil
		}
		return "Add it?", nil
	}
	home, err := loreHome()
	if err != nil {
		return "", err
	}
	if err := appendOrgEntry(ctx, home, entry); err != nil {
		return "", err
	}
	fmt.Fprintf(w, "✓ added org entry %s\n", entry.Name)
	if err := registerAfterImport(ctx, entry.Name); err != nil {
		// BUG-T3: a half-done import must not exit 0. The entry stays
		// (it is valid on its own); the error keeps the cause's code
		// so the exit status says what failed.
		return "", importRegisterErr(entry.Name, err)
	}
	return "", nil
}

// readSuggestion reads lore-org.yaml from the org folder the store's
// marker names (or "org", then the root project).
func readSuggestion(ctx context.Context, st *stores.Store, branch string) (stores.OrgFileDefaults, string, error) {
	tip, ok, err := st.Tip(ctx, branch)
	if err != nil {
		return stores.OrgFileDefaults{}, "", storeCLIError(err, "read the store")
	}
	if !ok {
		return stores.OrgFileDefaults{}, "", storeCLIError(fmt.Errorf("%w: %s has no branch %s", stores.ErrNoBranch, st.Norm, branch), "read the store")
	}
	candidates := []string{defaultMarkerOrgFolder, stores.RootFolder}
	if b, found, err := st.Repo.ReadFile(ctx, tip, stores.MarkerFile); err == nil && found {
		if mk, err := stores.ParseMarker(b); err == nil && mk.OrgFolder != "" {
			candidates = append([]string{mk.OrgFolder}, candidates...)
		}
	}
	for _, folder := range candidates {
		f, found, err := st.ReadOrgFile(ctx, branch, folder)
		if err != nil {
			return stores.OrgFileDefaults{}, "", storeCLIError(err, "read lore-org.yaml")
		}
		if found {
			return f, folder, nil
		}
	}
	return stores.OrgFileDefaults{}, "", errcodes.New(errcodes.InvalidInput, "this store has no lore-org.yaml suggesting an org entry").
		WithHint("ask the team to commit <org_folder>/.lore/lore-org.yaml, or add the entry to ~/.lore/config.yaml by hand")
}

// riskyImport lists what the user must accept explicitly: a default that
// writes into client repos, and owner wildcards that match whole hosts.
func riskyImport(o lorecfg.Org) []string {
	var out []string
	for kind, where := range o.Defaults.ClientRepos {
		if where == stores.DefaultRepo {
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

func indentBlock(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}

// appendOrgEntry adds one entry under orgs: in <home>/config.yaml, keeping
// the rest of the document (comments included), then re-validates the
// whole file and restores the old bytes if it no longer validates.
func appendOrgEntry(ctx context.Context, home string, entry lorecfg.Org) error {
	path := filepath.Join(home, lorecfg.TeamFileBase+lorecfg.ExtYAML)
	old, err := os.ReadFile(filepath.Clean(path))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errcodes.New(errcodes.Internal, "read config.yaml").WithCause(err)
	}
	var doc yaml.Node
	if len(old) > 0 {
		if unmarshalErr := yaml.Unmarshal(old, &doc); unmarshalErr != nil {
			return errcodes.New(errcodes.ConfigInvalid, "~/.lore/config.yaml does not parse").WithCause(unmarshalErr)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	var orgs *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "orgs" {
			orgs = root.Content[i+1]
		}
	}
	if orgs == nil {
		orgs = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "orgs"}, orgs)
	}
	var item yaml.Node
	if encodeErr := item.Encode(entry); encodeErr != nil {
		return errcodes.New(errcodes.Internal, "encode the entry").WithCause(encodeErr)
	}
	orgs.Content = append(orgs.Content, &item)
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return errcodes.New(errcodes.Internal, "encode config.yaml").WithCause(err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return errcodes.New(errcodes.Internal, "create ~/.lore").WithCause(err)
	}
	if err := atomicWriteFile(path, out, 0o600); err != nil {
		return errcodes.New(errcodes.Internal, "write config.yaml").WithCause(err)
	}
	if _, _, err := lorecfg.LoadTeam(ctx, home, lorecfg.Options{}); err != nil {
		if len(old) > 0 {
			_ = atomicWriteFile(path, old, 0o600) // best effort: put the user's file back
		} else {
			_ = os.Remove(path) // best effort: we created it
		}
		return errcodes.New(errcodes.ConfigInvalid, "the imported entry does not validate; nothing changed").WithCause(err)
	}
	return nil
}

// importRegisterErr is the error of an import whose entry was added but
// whose roster registration failed: the cause's own code (fail closed:
// E_INTERNAL for an uncoded one), a message saying the entry stays, and the
// command that finishes the job.
func importRegisterErr(entryName string, cause error) error {
	code := errcodes.Code(errs.CodeOf(cause))
	var ce *errcodes.CLIError
	if errors.As(cause, &ce) {
		code = ce.Code
	}
	return errcodes.New(code, "the org entry "+entryName+" is added to ~/.lore/config.yaml, but registering on its roster failed: "+redactIfQuiet(cause.Error())).
		WithHint("run `LORE_ORG=" + entryName + " lore actor register` in one of " + entryName + "'s repos or outside any repo (BUG-T3)")
}

// registerAfterImport registers the user on the new entry's roster
// (TEAM-031), with LORE_ORG pinning resolution to that entry.
func registerAfterImport(ctx context.Context, entryName string) error {
	prev, had := os.LookupEnv(envOrg)
	if err := os.Setenv(envOrg, entryName); err != nil {
		return err
	}
	defer func() {
		if had {
			_ = os.Setenv(envOrg, prev) // restoring the caller's environment
		} else {
			_ = os.Unsetenv(envOrg) // restoring the caller's environment
		}
	}()
	out, err := (cliRoster{}).ActorRegister(ctx, opsinfra.ActorRegisterIn{})
	if err != nil {
		return err
	}
	fmt.Print(out.Text()) // the `lore actor register` line, as before
	return nil
}
