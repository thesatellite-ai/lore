package wire

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/identity"
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
	b, err := os.ReadFile(filepath.Clean(configPath(home)))
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, errs.Wrap(errs.Internal, "wire: read "+ConfigFile, err)
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, errs.Wrap(errs.ConfigInvalid, "wire: ~/.lore/"+ConfigFile+" is not valid YAML", err)
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
func editMachineConfig(home string, edit func(root *yaml.Node) error) error {
	path := configPath(home)
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	b, err := os.ReadFile(filepath.Clean(path))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return errs.Wrap(errs.Internal, "wire: read "+ConfigFile, err)
	default:
		if err = yaml.Unmarshal(b, doc); err != nil {
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
	out, err := yaml.Marshal(doc)
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode "+ConfigFile, err)
	}
	return writeFileAtomic(path, out, configFileMode)
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

// writeFileAtomic writes b to path through a temporary file and a rename.
func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, cacheDirMode); err != nil {
		return errs.Wrap(errs.Internal, "wire: create "+dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: write "+filepath.Base(path), err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(b)
	if werr == nil {
		werr = tmp.Sync()
	}
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(name, mode)); err != nil {
		_ = os.Remove(name) // best effort: the write error is what matters
		return errs.Wrap(errs.Internal, "wire: write "+filepath.Base(path), err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name) // best effort
		return errs.Wrap(errs.Internal, "wire: write "+filepath.Base(path), err)
	}
	return nil
}
