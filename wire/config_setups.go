package wire

// config_setups.go — the onboarding keys of ~/.lore/config.yaml
// (ONBOARDING_DESIGN 3.1, 3.2, 7.3): `setups:`, `repo_setups:`,
// `setup.idle_minutes`, `setup.open_browser`, `doctor.scan_depth`,
// `doctor.probe_timeout_s`.
//
//	ReadSetupsConfig ─▶ lenient decode (like orgs) ─▶ defaults ─▶ onboard.Config
//	ValidateSetupsConfig ─▶ onboard.Validate + ValidateSettings ─▶ E_CONFIG_INVALID naming each entry
//	PutSetup / RemoveSetup / SetRepoSetup ─▶ editMachineConfig (node tree:
//	   every other key and every unknown key inside an entry kept byte for byte)
//
// Validation runs on the CANDIDATE file before a write, so a write never
// leaves config.yaml with an invalid setups list.

import (
	"bytes"
	"errors"
	"io/fs"
	neturl "net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/osrename"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
	"github.com/thesatellite-ai/lore/logic/yamlfmt"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// Config keys of the onboarding blocks.
const (
	configKeySetups     = "setups"
	configKeyRepoSetups = "repo_setups"
)

// setupsDoc is the part of config.yaml onboarding reads. The 7.3 ints and
// bool are pointers so an absent key takes its default.
type setupsDoc struct {
	Setups     []onboard.SetupEntry `yaml:"setups,omitempty"`
	RepoSetups map[string]string    `yaml:"repo_setups,omitempty"`
	Setup      struct {
		IdleMinutes *int  `yaml:"idle_minutes,omitempty"`
		OpenBrowser *bool `yaml:"open_browser,omitempty"`
	} `yaml:"setup,omitempty"`
	Doctor struct {
		ScanDepth     *int `yaml:"scan_depth,omitempty"`
		ProbeTimeoutS *int `yaml:"probe_timeout_s,omitempty"`
	} `yaml:"doctor,omitempty"`
	// Orgs and Context are read only to validate org: and tools: names,
	// and for the identity a member or admin setup uses (OrgIdentities).
	Orgs []struct {
		Name     string                `yaml:"name"`
		Identity onboard.SetupIdentity `yaml:"identity,omitempty"`
		// Stores: the org's stores; the first one's url is the team store
		// a team step shows when opened for edit (OrgStores).
		Stores []struct {
			URL string `yaml:"url"`
		} `yaml:"stores,omitempty"`
	} `yaml:"orgs,omitempty"`
	Context struct {
		Profiles []toolprofile.Profile `yaml:"profiles,omitempty"`
	} `yaml:"context,omitempty"`
	// Trash is trash.setups (lore setup remove; onboard.TrashedSetup).
	Trash struct {
		Setups []onboard.TrashedSetup `yaml:"setups,omitempty"`
		// Purged are the purge tombstones (onboard.Config.Purged).
		Purged []onboard.PurgedSetup `yaml:"purged,omitempty"`
	} `yaml:"trash,omitempty"`
	// Identity is the top-level identity: (onboard.Config.TopIdentity).
	Identity onboard.SetupIdentity `yaml:"identity,omitempty"`
	// Personal is read for personal.store (onboard.Config.SharedStore).
	Personal struct {
		Store string `yaml:"store,omitempty"`
	} `yaml:"personal,omitempty"`
}

// decodeSetupsDoc decodes raw leniently (unknown keys ignored, like orgs).
func decodeSetupsDoc(raw []byte) (setupsDoc, error) {
	var d setupsDoc
	if err := yamlerr.Decode(raw, &d, yamlerr.Lenient); err != nil {
		return d, configYAMLErr(err)
	}
	return d, nil
}

// config is the onboard.Config of d, defaults applied.
func (d setupsDoc) config() onboard.Config {
	s := onboard.DefaultSettings()
	if d.Setup.IdleMinutes != nil {
		s.IdleMinutes = *d.Setup.IdleMinutes
	}
	if d.Setup.OpenBrowser != nil {
		s.OpenBrowser = *d.Setup.OpenBrowser
	}
	if d.Doctor.ScanDepth != nil {
		s.ScanDepth = *d.Doctor.ScanDepth
	}
	if d.Doctor.ProbeTimeoutS != nil {
		s.ProbeTimeoutS = *d.Doctor.ProbeTimeoutS
	}
	var orgIDs map[string]onboard.SetupIdentity
	var orgStores map[string]string
	for _, o := range d.Orgs {
		if len(o.Stores) > 0 && strings.TrimSpace(o.Stores[0].URL) != "" {
			if orgStores == nil {
				orgStores = map[string]string{}
			}
			orgStores[o.Name] = strings.TrimSpace(o.Stores[0].URL)
		}
		if !o.Identity.IsZero() {
			if orgIDs == nil {
				orgIDs = map[string]onboard.SetupIdentity{}
			}
			orgIDs[o.Name] = o.Identity
		}
	}
	return onboard.Config{OrgIdentities: orgIDs, OrgStores: orgStores, Setups: d.Setups, RepoSetups: d.RepoSetups, Settings: s, SharedStore: strings.TrimSpace(d.Personal.Store), TopIdentity: d.Identity, Trash: d.Trash.Setups, Purged: d.Trash.Purged}
}

// problems is every 3.2 / 7.3 rule d breaks. ~ in a folder glob is the
// user's home (os.UserHomeDir, as setup resolution expands it), never the
// lore home: the lore home only locates config.yaml.
func (d setupsDoc) problems() []string {
	k := onboard.Known{Home: globHome()}
	for _, o := range d.Orgs {
		k.Orgs = append(k.Orgs, o.Name)
	}
	reg, err := toolprofile.New(d.Context.Profiles)
	if err != nil {
		// context.profiles is broken on its own; check the setups against
		// the built-ins so this reports only its own problems.
		reg, _ = toolprofile.New(nil) // built-ins only: always valid
	}
	if reg != nil {
		k.Tools = reg.Names()
	}
	c := d.config()
	out := onboard.Validate(c.Setups, c.RepoSetups, k)
	return append(out, onboard.ValidateSettings(c.Settings)...)
}

// readConfigRaw is config.yaml's bytes, nil when the file is absent. It is
// the one read of <home>/config.yaml: every reader goes through it, so a
// home that cannot hold the file (errs.FileIO: ~/.lore a file, or not
// writable) is reported the same way everywhere, with the path and the fix.
func readConfigRaw(home string) ([]byte, error) {
	// osrename.ReadFile: on Windows a read races another lore's rename of a
	// new config.yaml over the old one; that refusal lasts a moment.
	b, err := osrename.ReadFile(filepath.Clean(configPath(home)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.FileIO("read", configPath(home), err)
	}
	return b, nil
}

// ReadSetupsConfig reads the onboarding keys of <home>/config.yaml with
// their defaults (a missing file: no setups, every default). It does not
// validate; callers that act on a setup call ValidateSetupsConfig first.
func ReadSetupsConfig(home string) (onboard.Config, error) {
	raw, err := readConfigRaw(home)
	if err != nil || raw == nil {
		return onboard.Config{Settings: onboard.DefaultSettings()}, err
	}
	d, err := decodeSetupsDoc(raw)
	if err != nil {
		return onboard.Config{Settings: onboard.DefaultSettings()}, err
	}
	c := d.config()
	c.MigratedIdentityKey = migratedIdentityKey(home)
	return c, nil
}

// migratedIdentityKey is the stable_key of <home>/identity.yaml.migrated,
// the v0.1.17 identity a migration moved into config.yaml ("" when there
// is none or it does not read): onboard.Leftovers counts the top identity
// as a migration's leftover only when it is that one.
func migratedIdentityKey(home string) string {
	b, err := os.ReadFile(filepath.Join(home, identity.LegacyFile+LegacyMigratedSuffix))
	if err != nil {
		return ""
	}
	var f struct {
		StableKey string `yaml:"stable_key"`
	}
	if yaml.Unmarshal(b, &f) != nil {
		return ""
	}
	return strings.TrimSpace(f.StableKey)
}

// ValidateSetupsConfig reads and validates the onboarding keys: every
// broken rule in one E_CONFIG_INVALID, each naming its entry.
func ValidateSetupsConfig(home string) (onboard.Config, error) {
	raw, err := readConfigRaw(home)
	if err != nil || raw == nil {
		return onboard.Config{Settings: onboard.DefaultSettings()}, err
	}
	c, err := validateSetupsRaw(raw)
	c.MigratedIdentityKey = migratedIdentityKey(home)
	return c, err
}

// validateSetupsRaw is ValidateSetupsConfig over raw bytes (also used on
// a candidate file before a write).
func validateSetupsRaw(raw []byte) (onboard.Config, error) {
	d, err := decodeSetupsDoc(raw)
	if err != nil {
		return onboard.Config{Settings: onboard.DefaultSettings()}, err
	}
	if p := d.problems(); len(p) > 0 {
		return d.config(), setupsInvalid(p)
	}
	return d.config(), nil
}

// setupsInvalid is the E_CONFIG_INVALID of a list of problems.
func setupsInvalid(problems []string) error {
	return errs.New(errs.ConfigInvalid, "~/.lore/"+ConfigFile+": "+strings.Join(problems, "; ")).
		WithHint("fix the entries named above (lore setup edit <name>, or edit ~/.lore/" + ConfigFile + "), then lore config check")
}

// editSetups runs edit on the node tree, then validates the candidate
// file before anything is written (a refused edit writes nothing).
func editSetups(home string, edit func(root *yaml.Node) error) error {
	return editMachineConfig(home, func(root *yaml.Node) error {
		if err := edit(root); err != nil {
			return err
		}
		doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
		raw, err := yaml.Marshal(doc)
		if err != nil {
			return errs.Wrap(errs.Internal, "wire: encode "+ConfigFile, err)
		}
		_, err = validateSetupsRaw(raw)
		return err
	})
}

// setupsSeq is the `setups:` sequence node, created when absent.
func setupsSeq(root *yaml.Node) *yaml.Node {
	if s := mapGet(root, configKeySetups); s != nil && s.Kind == yaml.SequenceNode {
		return s
	}
	s := &yaml.Node{Kind: yaml.SequenceNode}
	mapSet(root, configKeySetups, s)
	return s
}

// setupIndex is the index of the entry named name in seq, -1 when absent.
func setupIndex(seq *yaml.Node, name string) int {
	for i, m := range seq.Content {
		if m.Kind == yaml.MappingNode {
			if n := mapGet(m, configKeyName); n != nil && n.Value == name {
				return i
			}
		}
	}
	return -1
}

// PutSetup adds e to `setups:`, or replaces the entry of the same name;
// when e is the default, every other entry stops being one.
// On replace, the keys lore knows are rewritten from e and every other key
// of the old entry (one lore does not read) is kept as it was. The whole
// candidate list is validated first (E_CONFIG_INVALID, nothing written).
// created is true when the entry is new.
func PutSetup(home string, e onboard.SetupEntry) (created bool, err error) {
	if e.Name == "" {
		return false, errs.New(errs.InvalidInput, "setup: a setup needs a name").WithHint("pass a name, e.g. personal")
	}
	var fresh yaml.Node
	if err = fresh.Encode(e); err != nil {
		return false, errs.Wrap(errs.Internal, "wire: encode setup "+e.Name, err)
	}
	plainBools(&fresh)
	err = editSetups(home, func(root *yaml.Node) error {
		seq := setupsSeq(root)
		if i := setupIndex(seq, e.Name); i < 0 {
			created = true
			seq.Content = append(seq.Content, &fresh)
		} else {
			seq.Content[i] = mergeSetupNode(seq.Content[i], &fresh)
		}
		// The default moves here, under the lock, against the file as it
		// is now (see putSetups), never from an earlier read.
		if e.Default {
			clearOtherDefaults(seq, e.Name)
		}
		return nil
	})
	return created, err
}

// PatchSetup writes into setup e.Name only the keys after changes from
// before, against config.yaml as it is under the config lock; every other
// key keeps what the file holds now. An edit read from an earlier copy of
// the file (`lore setup edit main --note …`) then never writes back a key
// another process changed in between (machines
// TestCampaignSetup_ConcurrentMixed: an identity set beside a setup edit
// of the same setup was lost, because PutSetup rewrote every key from the
// edit's stale copy). A setup removed meanwhile is E_NOT_FOUND; after
// marked default clears the others, as PutSetup does.
func PatchSetup(home string, before, after onboard.SetupEntry) error {
	var bn, an yaml.Node
	if err := bn.Encode(before); err != nil {
		return errs.Wrap(errs.Internal, "wire: encode setup "+before.Name, err)
	}
	if err := an.Encode(after); err != nil {
		return errs.Wrap(errs.Internal, "wire: encode setup "+after.Name, err)
	}
	plainBools(&bn)
	plainBools(&an)
	return editSetups(home, func(root *yaml.Node) error {
		seq := setupsSeq(root)
		i := setupIndex(seq, after.Name)
		if i < 0 {
			return errs.New(errs.NotFound, "setup "+strconv.Quote(after.Name)+" is no longer in ~/.lore/"+ConfigFile+" (another lore command removed it)").
				WithHint("lore setup list shows the setups")
		}
		cur := seq.Content[i]
		for _, k := range knownSetupKeys {
			b, a := mapGet(&bn, k), mapGet(&an, k)
			if sameYAML(b, a) {
				continue
			}
			if a == nil {
				mapDelete(cur, k)
				continue
			}
			if old := mapGet(cur, k); old != nil {
				keepComments(old, a)
			}
			mapSet(cur, k, a)
		}
		if after.Default {
			clearOtherDefaults(seq, after.Name)
		}
		return nil
	})
}

// sameYAML reports whether two values (nil = absent) encode the same.
func sameYAML(x, y *yaml.Node) bool {
	if x == nil || y == nil {
		return x == nil && y == nil
	}
	bx, ex := yaml.Marshal(x)
	by, ey := yaml.Marshal(y)
	return ex == nil && ey == nil && bytes.Equal(bx, by)
}

// knownSetupKeys are the entry keys SetupEntry owns; on replace they come
// from the new entry (absent there = removed), every other key is kept.
var knownSetupKeys = []string{"name", "kind", "org", "default", "folders", "identity", "github", "personal_store", "tools", "secrets", "live", "note", "disabled", "disabled_at"}

// mergeSetupNode is fresh plus the keys of old lore does not own, in old's
// order first. The person's comments stay where they wrote them: the
// entry's own (the `# the main one` line above `- name:`), and on a key
// lore rewrites, the comments of its old value (`kind: solo # mine`).
func mergeSetupNode(old, fresh *yaml.Node) *yaml.Node {
	out := &yaml.Node{Kind: yaml.MappingNode, Style: old.Style,
		HeadComment: old.HeadComment, LineComment: old.LineComment, FootComment: old.FootComment}
	for i := 0; i+1 < len(old.Content); i += 2 {
		k := old.Content[i].Value
		owned := false
		for _, o := range knownSetupKeys {
			if o == k {
				owned = true
				break
			}
		}
		if !owned {
			out.Content = append(out.Content, old.Content[i], old.Content[i+1])
			continue
		}
		if v := mapGet(fresh, k); v != nil {
			keepComments(old.Content[i+1], v)
			out.Content = append(out.Content, old.Content[i], v)
		}
	}
	for i := 0; i+1 < len(fresh.Content); i += 2 {
		if mapGet(out, fresh.Content[i].Value) == nil {
			out.Content = append(out.Content, fresh.Content[i], fresh.Content[i+1])
		}
	}
	return out
}

// keepComments puts the comments of old (a value lore replaces) on its
// replacement v, where v has none of its own.
func keepComments(old, v *yaml.Node) {
	if v.HeadComment == "" {
		v.HeadComment = old.HeadComment
	}
	if v.LineComment == "" {
		v.LineComment = old.LineComment
	}
	if v.FootComment == "" {
		v.FootComment = old.FootComment
	}
	// A nested block (identity:, github:) keeps the comments on its own
	// keys too (key: human:… # me), key by key.
	if old.Kind != yaml.MappingNode || v.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(v.Content); i += 2 {
		for j := 0; j+1 < len(old.Content); j += 2 {
			if old.Content[j].Value == v.Content[i].Value {
				keepComments(old.Content[j], v.Content[i])
				keepComments(old.Content[j+1], v.Content[i+1])
			}
		}
	}
}

// RemoveSetup deletes the entry named name and every repo_setups pin to
// it; found is false when there was none. It never touches stores or
// orgs[] (7.1 setup.remove).
func RemoveSetup(home, name string) (found bool, err error) {
	err = editSetups(home, func(root *yaml.Node) error {
		seq := mapGet(root, configKeySetups)
		if seq == nil || seq.Kind != yaml.SequenceNode {
			return nil
		}
		i := setupIndex(seq, name)
		if i < 0 {
			return nil
		}
		found = true
		seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
		if pins := mapGet(root, configKeyRepoSetups); pins != nil && pins.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(pins.Content); {
				if pins.Content[j+1].Value == name {
					pins.Content = append(pins.Content[:j], pins.Content[j+2:]...)
					continue
				}
				j += 2
			}
		}
		// The last setup gone: no `setups: []` / `repo_setups: {}` left
		// behind (a flow node the next add would append into).
		yamlfmt.DropEmpty(root, configKeySetups)
		yamlfmt.DropEmpty(root, configKeyRepoSetups)
		return nil
	})
	return found, err
}

// SetRepoSetup pins repoID to setup name in repo_setups (`lore setup use
// --here`); name "" removes the pin. A name that is not a setup is
// E_CONFIG_INVALID (validated with the rest).
func SetRepoSetup(home, repoID, name string) error {
	if repoID == "" {
		return errs.New(errs.InvalidInput, "setup use --here: this folder has no repo id").WithHint("run it inside a git repo")
	}
	return editSetups(home, func(root *yaml.Node) error {
		if name == "" {
			if pins := mapGet(root, configKeyRepoSetups); pins != nil && pins.Kind == yaml.MappingNode {
				mapDelete(pins, repoID)
			}
			yamlfmt.DropEmpty(root, configKeyRepoSetups)
			return nil
		}
		mapSet(mapChild(root, configKeyRepoSetups), repoID, scalar(name))
		return nil
	})
}

// setupNotFound is E_SETUP_NOT_FOUND for name among setups (7.2). The
// message names the setups this machine has, so a typo is fixed without a
// second command; with none, the hint is the guided setup that makes one
// (INTEG-MIGREST-5: an unknown --setup / X-Lore-Setup is refused even when
// no setups exist, never ignored).
func setupNotFound(name string, setups []onboard.SetupEntry) error {
	if len(setups) == 0 {
		return errs.New(errs.SetupNotFound, "no setup named "+strconv.Quote(name)+"; this machine has no setups").
			WithHint("lore setup makes one; drop --setup to run without one")
	}
	names := make([]string, len(setups))
	for i, e := range setups {
		names[i] = e.Name
	}
	return errs.New(errs.SetupNotFound, "no setup named "+strconv.Quote(name)+"; the setups on this machine: "+strings.Join(names, ", ")).
		WithHint("lore setup list")
}

// SetupByName returns the validated entry named name, E_SETUP_NOT_FOUND
// when there is none.
func SetupByName(home, name string) (onboard.SetupEntry, error) {
	c, err := ValidateSetupsConfig(home)
	if err != nil {
		return onboard.SetupEntry{}, err
	}
	e, ok := c.Setup(name)
	if !ok {
		return onboard.SetupEntry{}, setupNotFound(name, c.Setups)
	}
	return e, nil
}

// optBoolKeys are the SetupEntry keys of type onboard.OptBool: written as
// a plain YAML bool (autopush: true), not the quoted string the encoder
// gives a string type (a MarshalYAML would need `any`, which the noany
// guard refuses).
var optBoolKeys = map[string]bool{"autopush": true}

// plainBools retags every OptBool scalar under n as !!bool.
func plainBools(n *yaml.Node) {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			v := n.Content[i+1]
			if optBoolKeys[n.Content[i].Value] && v.Kind == yaml.ScalarNode {
				v.Tag, v.Style = "!!bool", 0
			}
		}
	}
	for _, c := range n.Content {
		plainBools(c)
	}
}

// globHome is the folder ~ names in setups[].folders: the user's home.
// When it cannot be found, "/" stands in, so a ~/ glob still counts as
// absolute (it is: it names the home, whatever that turns out to be).
func globHome() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return string(filepath.Separator)
}

// setPersonalStoreFollowed records url as personal.store and, in the same
// write, makes every setup whose own personal_store is the default local
// folder <home>/stores/me follow it ("inherit"): a person has one personal
// store `me` (decided 2026-11-09), so `lore store init me <git url>` moves
// it for the setups that were on the local one instead of leaving them
// there, where their own url would win over personal.store. It returns
// those setups' names. A url that is that folder itself changes no setup.
func setPersonalStoreFollowed(home, url string) ([]string, error) {
	local := filepath.Join(home, personalStoresDir, opsstores.PersonalName)
	isLocal := func(u string) bool {
		pu, err := neturl.Parse(strings.TrimSpace(u))
		return err == nil && pu.Scheme == fileuri.Scheme && filepath.Clean(fileuri.OSPath(pu.Path)) == local
	}
	if isLocal(url) {
		return nil, setPersonalStore(home, url)
	}
	var moved []string
	err := editSetups(home, func(root *yaml.Node) error {
		moved = nil
		mapSet(mapChild(root, configKeyPersonal), configKeyStore, scalar(url))
		seq := mapGet(root, configKeySetups)
		if seq == nil || seq.Kind != yaml.SequenceNode {
			return nil
		}
		for _, m := range seq.Content {
			if m.Kind != yaml.MappingNode {
				continue
			}
			ps := mapGet(m, "personal_store")
			if ps == nil || !isLocal(ps.Value) {
				continue
			}
			mapSet(m, "personal_store", scalar(onboard.PersonalStoreInherit))
			if n := mapGet(m, configKeyName); n != nil {
				moved = append(moved, n.Value)
			}
		}
		return nil
	})
	return moved, err
}
