package wire

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/osrename"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
	"github.com/thesatellite-ai/lore/logic/yamlfmt"
	"github.com/thesatellite-ai/lore/model/errs"
)

// ~/.lore/config.yaml: the one settings home about "me" (STORE_SERVER_SPEC
// 8). The core reads and writes only the keys it owns today: `identity:`
// (CFG9), `orgs[].identity` (read) and `personal.store` (the store `--to me`
// names). Every other key belongs to the v0.1.17 code and is kept byte for
// byte on a write: edits go through the YAML node tree, never a re-marshal
// of a struct that would drop what it does not know.
const (
	// ConfigFile is the file's name under the lore home.
	ConfigFile = "config.yaml"
	// configKeyIdentity, configKeyPersonal, configKeyStore are the keys the
	// core writes.
	configKeyIdentity = "identity"
	configKeyPersonal = "personal"
	configKeyStore    = "store"
	// PersonalLocalV017 is v0.1.17's `personal.store: local` (its own local
	// personal store layout, not a 0.1.18 store url): not attached as `me`.
	PersonalLocalV017 = "local"
	// configFileMode keeps config.yaml private (it names identities).
	configFileMode = 0o600
)

// machineConfig is what the core reads from config.yaml.
type machineConfig struct {
	identity.Config `yaml:",inline"`
	Personal        struct {
		Store string `yaml:"store,omitempty"`
	} `yaml:"personal,omitempty"`
}

// configPath is <home>/config.yaml.
func configPath(home string) string { return filepath.Join(home, ConfigFile) }

// readMachineConfig reads config.yaml; a missing file is the zero config.
// Unknown keys are ignored (they are v0.1.17's).
func readMachineConfig(home string) (machineConfig, error) {
	var c machineConfig
	b, err := readConfigRaw(home)
	if err != nil {
		return c, err
	}
	if b == nil {
		return c, nil
	}
	if err := yamlerr.Decode(b, &c, yamlerr.Lenient); err != nil {
		return c, configYAMLErr(err)
	}
	return c, nil
}

// personalURL is the 0.1.18 url of the personal store, "" when none is set
// or the value is v0.1.17's `local`.
func (c machineConfig) personalURL() string {
	s := strings.TrimSpace(c.Personal.Store)
	if s == "" || s == PersonalLocalV017 {
		return ""
	}
	return s
}

// editMachineConfig loads config.yaml as a node tree, lets edit change it,
// and writes it back atomically (mode 0600). A missing file starts empty.
func editMachineConfig(home string, edit func(root *yaml.Node) error) (err error) {
	release, err := lockMachineConfig(home)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	path := configPath(home)
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
	// The file as an edit that changes nothing would write it: such an
	// edit (a refused command, a name not found, a value already set)
	// leaves the file byte for byte, never restyled (machines
	// TestCampaignSetup_BadInput: setup disable of an unknown name
	// rewrote a flow-style file in block style).
	var pristine []byte
	if len(b) > 0 {
		pristine, _ = yamlfmt.EncodeLike(doc, b)
	}
	if err = edit(root); err != nil {
		return err
	}
	// The file's own indent (2 when it shows none) and line endings, and an
	// emptied file as its comments or nothing, never `{}` (logic/yamlfmt).
	out, err := yamlfmt.EncodeLike(doc, b)
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode "+ConfigFile, err)
	}
	if pristine != nil && bytes.Equal(out, pristine) {
		return nil
	}
	return writeMachineConfigFile(path, out, configFileMode)
}

// configLockFile is the lock beside config.yaml that serialises its
// read-modify-write cycles across processes (the setup page and a CLI, two
// agents): without it two edits at once each write their own copy and one
// is lost.
const configLockFile = ConfigFile + ".lock"

// configLockWait is how long an edit waits for another process's edit.
// An edit holds the lock for one read, edit and atomic write.
const configLockWait = 10 * time.Second

// lockMachineConfig takes the config.yaml lock (home is made owner-only
// first, as writeFileAtomic would).
func lockMachineConfig(home string) (func() error, error) {
	if err := os.MkdirAll(home, cacheDirMode); err != nil {
		return nil, errs.FileIO("create the lore home", home, err)
	}
	release, err := rowfs.LockFile(context.Background(), filepath.Join(home, configLockFile), configLockWait)
	if errs.Has(err, errs.StoreLocked) {
		return nil, errs.Wrap(errs.StoreLocked, "another lore process kept ~/.lore/"+ConfigFile+" locked for "+configLockWait.String(), err).
			WithHint("retry when the other lore command (or the setup page's save) has finished")
	}
	return release, err
}

// mapGet returns the value node of key in mapping m, nil when absent.
func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapSet sets key in mapping m to v (replacing, or appending at the end).
func mapSet(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
}

// mapChild returns the mapping under key, creating it.
func mapChild(m *yaml.Node, key string) *yaml.Node {
	if c := mapGet(m, key); c != nil && c.Kind == yaml.MappingNode {
		return c
	}
	c := &yaml.Node{Kind: yaml.MappingNode}
	mapSet(m, key, c)
	return c
}

// scalar is a plain string node.
func scalar(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: s, Tag: "!!str"} }

// setPersonalStore records url as `personal.store`.
func setPersonalStore(home, url string) error {
	return editMachineConfig(home, func(root *yaml.Node) error {
		mapSet(mapChild(root, configKeyPersonal), configKeyStore, scalar(url))
		return nil
	})
}

// setTopIdentity writes the top-level `identity:` block.
func setTopIdentity(home string, e identity.Entry) error {
	return editMachineConfig(home, func(root *yaml.Node) error {
		n := &yaml.Node{Kind: yaml.MappingNode}
		mapSet(n, "key", scalar(e.Key))
		if e.Name != "" {
			mapSet(n, "name", scalar(e.Name))
		}
		if e.Handle != "" {
			mapSet(n, "handle", scalar(e.Handle))
		}
		mapSet(root, configKeyIdentity, n)
		return nil
	})
}

// configKeyOrgs / configKeyName address one org entry (`orgs[].name`).
const (
	configKeyOrgs = "orgs"
	configKeyName = "name"
	// LegacyMigratedSuffix is appended to a v0.1.17 identity.yaml once
	// `lore identity set|unset` has moved who you are into config.yaml
	// (CFG9): the file is kept, renamed, so nothing a person wrote is lost
	// and no resolver reads it again.
	LegacyMigratedSuffix = ".migrated"
)

// SetIdentity writes the top-level `identity:` of ~/.lore/config.yaml
// (CFG9: config.yaml is the only home of who you are) and moves a v0.1.17
// identity.yaml aside (LegacyMigratedSuffix). It returns the config path
// and the migrated file's new path ("" when there was none).
//
//	lore identity set ─► SetIdentity ─► setTopIdentity (node edit, 0600)
//	                                  └► retireLegacyIdentity (rename)
func SetIdentity(home string, e identity.Entry) (path, migrated string, err error) {
	if err = setTopIdentity(home, e); err != nil {
		return "", "", err
	}
	migrated, err = retireLegacyIdentity(home)
	return configPath(home), migrated, err
}

// UnsetIdentity clears the top-level `identity:` (org == "") or
// `orgs[].identity` of the org named org, and moves a v0.1.17 identity.yaml
// aside on the top-level form so the chain does not fall back to it. found
// is false when there was nothing to clear; an unknown org is E_NOT_FOUND.
func UnsetIdentity(home, org string) (found bool, migrated string, err error) {
	if err = editMachineConfig(home, func(root *yaml.Node) error {
		target := root
		if org != "" {
			target = orgEntry(root, org)
			if target == nil {
				return errs.New(errs.NotFound, "identity unset: ~/.lore/"+ConfigFile+" has no org "+strconv.Quote(org)).
					WithHint("lore config list shows the org entries")
			}
		}
		found = mapDelete(target, configKeyIdentity)
		return nil
	}); err != nil {
		return false, "", err
	}
	if org == "" {
		migrated, err = retireLegacyIdentity(home)
		found = found || migrated != ""
	}
	return found, migrated, err
}

// orgEntry is the mapping of `orgs[]` whose name is org, nil when absent.
func orgEntry(root *yaml.Node, org string) *yaml.Node {
	seq := mapGet(root, configKeyOrgs)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	for _, m := range seq.Content {
		if m.Kind == yaml.MappingNode {
			if n := mapGet(m, configKeyName); n != nil && n.Value == org {
				return m
			}
		}
	}
	return nil
}

// mapDelete removes key from mapping m; false when it was absent.
func mapDelete(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			yamlfmt.KeepHeader(m, i)
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

// retireLegacyIdentity renames <home>/identity.yaml to
// identity.yaml.migrated (an existing .migrated is replaced); "" when there
// is no legacy file.
func retireLegacyIdentity(home string) (string, error) {
	from := filepath.Join(home, identity.LegacyFile)
	if _, err := os.Stat(from); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	to := from + LegacyMigratedSuffix
	if err := osrename.Rename(from, to); err != nil {
		return "", errs.Wrap(errs.Internal, "wire: move "+identity.LegacyFile+" aside", err)
	}
	return to, nil
}

// writeFileAtomic writes b to path through a temporary file and a rename.
func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, cacheDirMode); err != nil {
		return errs.FileIO("create the config folder", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return errs.FileIO("write", path, err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(b)
	if werr == nil {
		werr = tmp.Sync()
	}
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(name, mode)); err != nil {
		_ = os.Remove(name) // best effort: the write error is what matters
		return errs.FileIO("write", path, err)
	}
	if err := osrename.Rename(name, path); err != nil {
		_ = os.Remove(name) // best effort
		return errs.FileIO("write", path, err)
	}
	return nil
}

// agent_details values of an org entry (LORE_TEAM_SPEC 7.6, D8): where an
// agent's work details go for a client repo of the org.
const (
	// AgentDetailsInternal is the default: in a client repo an agent is
	// recorded as its owning person (Q11), its details stay internal.
	AgentDetailsInternal = "internal"
	// AgentDetailsClient is the opt-in: the client sees the agent as
	// itself (its own id and projection) in its repo.
	AgentDetailsClient = "client"
)

// orgAgentDetails is the part of config.yaml agentDetailsOf reads; a
// second decode of the same file, because `orgs` is already decoded inline
// by identity.Config, whose Org type does not carry the key.
type orgAgentDetails struct {
	Orgs []struct {
		Name         string `yaml:"name"`
		AgentDetails string `yaml:"agent_details,omitempty"`
	} `yaml:"orgs,omitempty"`
}

// agentDetailsOf returns the agent_details setting of the org entry named
// org in config.yaml: AgentDetailsInternal when the file, the entry or the
// key is absent (the decided default, the safer side), E_CONFIG_INVALID
// for a value outside the two (as v0.1.17's lorecfg validation refused
// it), so a typo never silently picks either.
func agentDetailsOf(home, org string) (string, error) {
	if org == "" {
		return AgentDetailsInternal, nil
	}
	b, err := readConfigRaw(home)
	if err != nil {
		return "", err
	}
	if b == nil {
		return AgentDetailsInternal, nil
	}
	var c orgAgentDetails
	if err = yamlerr.Decode(b, &c, yamlerr.Lenient); err != nil {
		return "", configYAMLErr(err)
	}
	for _, o := range c.Orgs {
		if o.Name != org {
			continue
		}
		switch o.AgentDetails {
		case "", AgentDetailsInternal:
			return AgentDetailsInternal, nil
		case AgentDetailsClient:
			return AgentDetailsClient, nil
		}
		return "", errs.New(errs.ConfigInvalid, "wire: orgs["+org+"].agent_details "+strconv.Quote(o.AgentDetails)+" must be "+AgentDetailsInternal+" or "+AgentDetailsClient).
			WithHint("fix it in ~/.lore/" + ConfigFile)
	}
	return AgentDetailsInternal, nil
}

// ToolProfiles implements settings.ToolProfiler: the user's own tool
// profiles from config.yaml (context.profiles). Q-R2-3: context.targets is
// checked against the registry the user really has, built-ins plus these.
// It decodes only `context:` on its own, so a malformed context.profiles
// fails this check alone, never the identity/personal reads every command
// needs (readMachineConfig).
func (a *App) ToolProfiles(context.Context) ([]toolprofile.Profile, error) {
	b, err := readConfigRaw(a.opts.Home)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	var c struct {
		Context struct {
			Profiles []toolprofile.Profile `yaml:"profiles,omitempty"`
		} `yaml:"context,omitempty"`
	}
	if err := yamlerr.Decode(b, &c, yamlerr.Lenient); err != nil { // line + key, never a Go type
		return nil, errs.New(errs.ConfigInvalid, "wire: "+ConfigFile+" context.profiles: "+err.Error()).
			WithHint("check it with `lore config check`")
	}
	return c.Context.Profiles, nil
}
