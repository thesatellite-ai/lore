// config_yaml_cmd.go — `lore config get|set|list` on ~/.lore/config.yaml
// (CFG12, CLI_REFERENCE 3.22). v0.1.17 edited the DB-level dbconfig table
// with the same spelling; 0.1.18 keeps the spelling and changes the
// target: these commands never touch a database or a policy row.
//
// A key is a dotted path through the YAML mappings (`live.autopush`,
// `lease.ttl`); a numeric part indexes a list (`orgs.0.name`). `set` keeps
// the file's comments and order (it edits the node tree), refuses a key
// lore does not read (E_INVALID_INPUT) and a result that does not
// validate (E_CONFIG_INVALID), and writes atomically with the file's mode.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// configKeySep splits a dotted config key.
const configKeySep = "."

// configFileName is ~/.lore/config.yaml's base name.
const configFileName = lorecfg.TeamFileBase + lorecfg.ExtYAML

// configFileMode is the mode of a config.yaml lore creates (it may hold
// identities and store URLs: owner only).
const configFileMode fs.FileMode = 0o600

// configPath is ~/.lore/config.yaml.
func configPath() (string, error) {
	dir, err := identity.Dir()
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "resolve HOME").WithCause(err)
	}
	return filepath.Join(dir, configFileName), nil
}

// readConfigDoc reads config.yaml as a node tree whose top level is a
// mapping (an empty mapping when the file is absent or empty), with the
// file's mode.
func readConfigDoc(path string) (*yaml.Node, fs.FileMode, error) {
	perm := configFileMode
	raw, err := os.ReadFile(filepath.Clean(path))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		raw = nil
	case err != nil:
		return nil, 0, errcodes.New(errcodes.Internal, "read "+path).WithCause(err)
	default:
		if info, serr := os.Stat(path); serr == nil {
			perm = info.Mode().Perm()
		}
	}
	doc := &yaml.Node{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, doc); err != nil {
			return nil, 0, errcodes.New(errcodes.ConfigInvalid, path+" does not parse").WithCause(err).
				WithHint("run `lore config check` for the line")
		}
	}
	if doc.Kind == 0 {
		doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, 0, errcodes.New(errcodes.ConfigInvalid, path+": the top level must be a mapping")
	}
	return doc, perm, nil
}

// configChild returns the child node of n named part (a mapping key or a
// list index), or nil.
func configChild(n *yaml.Node, part string) *yaml.Node {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == part {
				return n.Content[i+1]
			}
		}
	case yaml.SequenceNode:
		if i, err := strconv.Atoi(part); err == nil && i >= 0 && i < len(n.Content) {
			return n.Content[i]
		}
	}
	return nil
}

// configKeyParts splits and checks a dotted key.
func configKeyParts(key string) ([]string, error) {
	parts := strings.Split(strings.TrimSpace(key), configKeySep)
	for _, p := range parts {
		if p == "" {
			return nil, errcodes.New(errcodes.InvalidInput, fmt.Sprintf("config key %q is not a dotted path", key)).
				WithHint("for example live.autopush or lease.ttl")
		}
	}
	return parts, nil
}

// configScalarText renders a node for get/list: a scalar's value, any
// other node as YAML.
func configScalarText(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	b, err := yaml.Marshal(n)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

// configFlatten lists every scalar leaf of n as dotted key → value.
func configFlatten(prefix string, n *yaml.Node, out map[string]string) {
	join := func(k string) string {
		if prefix == "" {
			return k
		}
		return prefix + configKeySep + k
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			configFlatten(join(n.Content[i].Value), n.Content[i+1], out)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			configFlatten(join(strconv.Itoa(i)), c, out)
		}
	default:
		out[prefix] = n.Value
	}
}

// configUnknownKey reports the first key of raw that lorecfg.Config does
// not read ("" when every key is known).
func configUnknownKey(raw []byte) string {
	for _, w := range unknownKeyWarnings(raw) {
		return w
	}
	return ""
}

func newConfigGetCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "get <key>", Short: "Get a key of ~/.lore/config.yaml (a dotted path, e.g. live.autopush)",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			parts, err := configKeyParts(args[0])
			if err != nil {
				return err
			}
			doc, _, err := readConfigDoc(path)
			if err != nil {
				return err
			}
			n := doc.Content[0]
			for _, p := range parts {
				if n = configChild(n, p); n == nil {
					return errcodes.New(errcodes.NotFound, fmt.Sprintf("config key %q not found", args[0])).
						WithHint("`lore config list` shows the keys set in " + path)
				}
			}
			if jsonOut { // CLI_REFERENCE 3.22
				printJSON(constants.KindConfigGet, configEntry{Key: args[0], Value: configScalarText(n)}, 1)
				return nil
			}
			fmt.Println(configScalarText(n))
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newConfigSetCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "set <key> <value>", Short: "Set a key of ~/.lore/config.yaml (keeps comments and order)",
		Example: `  lore config set live.autopush false
  lore config set lease.ttl 45m`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			key, val := args[0], args[1]
			parts, err := configKeyParts(key)
			if err != nil {
				return err
			}
			doc, perm, err := readConfigDoc(path)
			if err != nil {
				return err
			}
			if err := configSetNode(doc.Content[0], parts, val); err != nil {
				return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("config key %q: %v", key, err))
			}
			var buf bytes.Buffer
			enc := yaml.NewEncoder(&buf)
			enc.SetIndent(2)
			if err := enc.Encode(doc); err != nil {
				return errcodes.New(errcodes.Internal, "encode "+path).WithCause(err)
			}
			if err := enc.Close(); err != nil {
				return errcodes.New(errcodes.Internal, "encode "+path).WithCause(err)
			}
			if w := configUnknownKey(buf.Bytes()); w != "" {
				return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("config key %q is not a key lore reads (%s)", key, w)).
					WithHint("nothing written; team rules are set with `lore policy set`")
			}
			var check lorecfg.Config
			if err := yaml.Unmarshal(buf.Bytes(), &check); err != nil {
				return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("config key %q: bad value %q", key, val)).WithCause(err).
					WithHint("nothing written")
			}
			if err := check.Validate(); err != nil {
				return errcodes.New(errcodes.ConfigInvalid, "refusing to write "+path).WithCause(err).WithHint("nothing written")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return errcodes.New(errcodes.Internal, "create "+filepath.Dir(path)).WithCause(err)
			}
			if err := atomicWriteFile(path, buf.Bytes(), perm); err != nil {
				return errcodes.New(errcodes.Internal, "write "+path).WithCause(err)
			}
			if jsonOut { // CLI_REFERENCE 3.22
				printJSON(constants.KindConfigSet, configEntry{Key: key, Value: val}, 1)
				return nil
			}
			fmt.Printf("✓ %s = %s (%s)\n", key, val, redactIfQuiet(path))
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// configSetNode sets the scalar at parts under mapping m, creating the
// mappings on the way; val is parsed as a YAML scalar (so true, 30m and
// 3 keep their types). A list or a mapping in the way of a scalar, or a
// scalar in the way of a deeper key, is refused.
func configSetNode(m *yaml.Node, parts []string, val string) error {
	var parsed yaml.Node
	if err := yaml.Unmarshal([]byte(val), &parsed); err != nil || len(parsed.Content) != 1 || parsed.Content[0].Kind != yaml.ScalarNode {
		parsed = yaml.Node{Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: val}}}
	}
	leaf := parsed.Content[0]
	n := m
	for i, p := range parts {
		last := i == len(parts)-1
		next := configChild(n, p)
		switch {
		case next == nil && n.Kind == yaml.MappingNode:
			if last {
				n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p}, leaf)
				return nil
			}
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p}, next)
		case next == nil:
			return fmt.Errorf("%q does not exist under a list or a value", p)
		case last && next.Kind != yaml.ScalarNode:
			return fmt.Errorf("%q holds a list or a mapping; set one of its keys", p)
		case last:
			// Keep the old value's comments (head, inline, foot).
			head, line, foot := next.HeadComment, next.LineComment, next.FootComment
			*next = *leaf
			next.HeadComment, next.LineComment, next.FootComment = head, line, foot
			return nil
		}
		n = next
	}
	return nil
}

// configEntry is one key of `lore config list --json`.
type configEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func newConfigListCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "list", Short: "List every key set in ~/.lore/config.yaml",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			doc, _, err := readConfigDoc(path)
			if err != nil {
				return err
			}
			flat := map[string]string{}
			configFlatten("", doc.Content[0], flat)
			keys := make([]string, 0, len(flat))
			for k := range flat {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if jsonOut {
				rows := make([]configEntry, 0, len(keys))
				for _, k := range keys {
					rows = append(rows, configEntry{Key: k, Value: flat[k]})
				}
				printJSON(constants.KindConfigList, rows, len(rows))
				return nil
			}
			if len(keys) == 0 {
				fmt.Println("(no config keys)")
				return nil
			}
			for _, k := range keys {
				fmt.Printf("%s=%s\n", k, flat[k])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}
