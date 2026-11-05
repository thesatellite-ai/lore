package config

// Dotted-path access to the YAML node tree of ~/.lore/config.yaml: a key
// is a path through mappings (`live.autopush`), a numeric part indexes a
// list (`orgs.0.name`). Pure helpers: no file access.

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/model/errs"
)

// keySep splits a dotted config key.
const keySep = "."

// yamlIndent is the indent `config set` writes the file with (the v0.1.17
// writer's, so a set never reflows a hand-written file more than needed).
const yamlIndent = 2

// tagStr and tagMap are the YAML tags of a new key and a new mapping.
const (
	tagStr = "!!str"
	tagMap = "!!map"
)

// keyParts splits and checks a dotted key: no empty part.
func keyParts(key string) ([]string, error) {
	parts := strings.Split(strings.TrimSpace(key), keySep)
	for _, p := range parts {
		if p == "" {
			return nil, errs.New(errs.InvalidInput, fmt.Sprintf("config key %q is not a dotted path", key)).
				WithHint("for example live.autopush or lease.ttl")
		}
	}
	return parts, nil
}

// parseDoc reads raw as a node tree whose top level is a mapping (an empty
// mapping when raw is empty). path names the file in errors.
func parseDoc(path string, raw []byte) (*yaml.Node, error) {
	doc := &yaml.Node{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, doc); err != nil {
			return nil, errs.Wrap(errs.ConfigInvalid, path+" does not parse", err).
				WithHint("run `lore config check` for the line")
		}
	}
	if doc.Kind == 0 {
		doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: tagMap}}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errs.New(errs.ConfigInvalid, path+": the top level must be a mapping")
	}
	return doc, nil
}

// encodeDoc writes doc back as YAML with the file's indent.
func encodeDoc(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// child returns the child of n named part (a mapping key or a list
// index), or nil.
func child(n *yaml.Node, part string) *yaml.Node {
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
	case yaml.DocumentNode, yaml.ScalarNode, yaml.AliasNode:
		// no named children
	}
	return nil
}

// scalarText renders a node for get/list: a scalar's value, any other
// node as YAML.
func scalarText(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	b, err := yaml.Marshal(n)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

// flatten lists every scalar leaf of n as dotted key → value.
func flatten(prefix string, n *yaml.Node, out map[string]string) {
	join := func(k string) string {
		if prefix == "" {
			return k
		}
		return prefix + keySep + k
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			flatten(join(n.Content[i].Value), n.Content[i+1], out)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			flatten(join(strconv.Itoa(i)), c, out)
		}
	case yaml.DocumentNode, yaml.ScalarNode, yaml.AliasNode:
		out[prefix] = n.Value
	}
}

// setNode sets the scalar at parts under mapping m, creating mappings on
// the way; val is parsed as a YAML scalar (so true, 30m and 3 keep their
// types). A list or a mapping in the way of a scalar, or a scalar in the
// way of a deeper key, is refused. The old value's comments are kept.
func setNode(m *yaml.Node, parts []string, val string) error {
	var parsed yaml.Node
	if err := yaml.Unmarshal([]byte(val), &parsed); err != nil || len(parsed.Content) != 1 || parsed.Content[0].Kind != yaml.ScalarNode {
		parsed = yaml.Node{Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: tagStr, Value: val}}}
	}
	leaf := parsed.Content[0]
	n := m
	for i, p := range parts {
		last := i == len(parts)-1
		next := child(n, p)
		switch {
		case next == nil && n.Kind == yaml.MappingNode:
			if last {
				n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: p}, leaf)
				return nil
			}
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: p}, next)
		case next == nil:
			return fmt.Errorf("%q does not exist under a list or a value", p)
		case last && next.Kind != yaml.ScalarNode:
			return fmt.Errorf("%q holds a list or a mapping; set one of its keys", p)
		case last:
			head, line, foot := next.HeadComment, next.LineComment, next.FootComment
			*next = *leaf
			next.HeadComment, next.LineComment, next.FootComment = head, line, foot
			return nil
		}
		n = next
	}
	return nil
}
