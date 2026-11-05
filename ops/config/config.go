// Package config defines `lore config get|set|list|check` (layer L5,
// CFG12, CLI_REFERENCE 3.22): the keys of this machine's
// ~/.lore/config.yaml. These operations never touch a store, a database
// or a policy row (team rules are `lore policy set`), so they are Local
// (op.Spec.Local, P3-CFG-1): they work outside a repo and while the file
// is invalid.
//
//	config get <key>          Port.ReadConfigFile ─► walk the dotted key
//	config list               Port.ReadConfigFile ─► every scalar leaf, sorted
//	config set <key> <value>  read ─► edit the node tree (comments, order kept)
//	                          ─► Schema.ValidateConfig (unknown key: E_INVALID_INPUT,
//	                             wrong type: E_INVALID_INPUT, rule: E_CONFIG_INVALID)
//	                          ─► Port.WriteConfigFile (atomic, the file's mode)
//	config check              Schema.CheckConfig (every problem at once)
//
// The file schema (which keys lore reads, their validation) is the org
// configuration's, supplied by the composition root through Port.
package config

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore config …`).
const (
	OpGet   = "config.get"
	OpSet   = "config.set"
	OpList  = "config.list"
	OpCheck = "config.check"
)

// File is ~/.lore/config.yaml as read: its path, bytes and mode.
type File struct {
	Path string
	Raw  []byte
	// Exists is false when the file is absent (Raw is empty then).
	Exists bool
	// Mode is the file's permission bits (0600 for a new file).
	Mode uint32
}

// Verdict is what the config schema says about a candidate file.
type Verdict struct {
	// Unknown lists keys lore does not read, each with its line.
	Unknown []string
	// Decode is set when a value has the wrong type.
	Decode error
	// Invalid is set when the decoded file breaks a rule.
	Invalid error
}

// Port is what the config operations need; *wire.App implements it.
type Port interface {
	// ReadConfigFile reads ~/.lore/config.yaml (absent = empty, Exists false).
	ReadConfigFile(ctx context.Context) (File, error)
	// WriteConfigFile writes f.Raw to f.Path atomically with f.Mode.
	WriteConfigFile(ctx context.Context, f File) error
	// ValidateConfig checks a candidate file against the schema.
	ValidateConfig(ctx context.Context, raw []byte) (Verdict, error)
	// CheckConfig validates the file on disk and lists every problem: an
	// invalid file is E_CONFIG_INVALID naming each problem.
	CheckConfig(ctx context.Context) (CheckOut, error)
}

// KeyIn names one key.
type KeyIn struct {
	Key string `json:"key" arg:"0" desc:"a dotted path, e.g. live.autopush or orgs.0.name"`
}

// SetIn is the input of config.set.
type SetIn struct {
	Key   string `json:"key" arg:"0" desc:"a dotted path, e.g. live.autopush"`
	Value string `json:"value" arg:"1" desc:"the value, read as a YAML scalar (true, 45m and 3 keep their types)"`
}

// Entry is one key and its value (config.get, and each row of config.list).
type Entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Text is the bare value, as v0.1.17's `config get` printed it.
func (e Entry) Text() string { return e.Value + "\n" }

// SetOut is the key written, its value and the file.
type SetOut struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Path  string `json:"path"`
}

// Text is the v0.1.17 confirmation line.
func (o SetOut) Text() string { return fmt.Sprintf("✓ %s = %s (%s)\n", o.Key, o.Value, o.Path) }

// ListIn is the (empty) input of config.list.
type ListIn struct{}

// ListOut is every key set in the file.
type ListOut struct {
	Entries []Entry `json:"entries"`
	Count   int     `json:"count"`
}

// Text prints key=value lines, as v0.1.17 did.
func (o ListOut) Text() string {
	if len(o.Entries) == 0 {
		return "(no config keys)\n"
	}
	var b strings.Builder
	for _, e := range o.Entries {
		b.WriteString(e.Key + "=" + e.Value + "\n")
	}
	return b.String()
}

// CheckIn is the (empty) input of config.check.
type CheckIn struct{}

// CheckOut is a valid (or absent) config file: its org entries and the
// warnings (unknown keys, live kinds that are not synced tables).
type CheckOut struct {
	Path     string   `json:"path"`
	Exists   bool     `json:"exists"`
	Orgs     []string `json:"orgs"`
	Warnings []string `json:"warnings"`
}

// Text is the v0.1.17 human form of `lore config check`.
func (o CheckOut) Text() string {
	var b strings.Builder
	if !o.Exists {
		fmt.Fprintf(&b, "✓ %s does not exist: no orgs configured (every repo resolves to no org)\n", o.Path)
		return b.String()
	}
	suffix := "ies"
	if len(o.Orgs) == 1 {
		suffix = "y"
	}
	fmt.Fprintf(&b, "✓ %s is valid: %d org entr%s (%s)\n", o.Path, len(o.Orgs), suffix, strings.Join(o.Orgs, ", "))
	for _, w := range o.Warnings {
		b.WriteString("  warning: " + w + "\n")
	}
	return b.String()
}

// readErrors are the codes reading the file can return.
var readErrors = []errs.Code{errs.InvalidInput, errs.NotFound, errs.ConfigInvalid, errs.Unsupported, errs.Internal}

// Ops returns the config operations bound to p.
func Ops(p Port) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpGet, Summary: "Get a key of ~/.lore/config.yaml (a dotted path, e.g. live.autopush)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors, Local: true,
			Examples: []op.Example{{Title: "read one key", CLI: "lore config get live.autopush", Want: ""}},
		}, func(ctx context.Context, in KeyIn) (Entry, error) {
			return get(ctx, p, in.Key)
		}),
		op.Define(op.Spec{
			Name: OpSet, Summary: "Set a key of ~/.lore/config.yaml (keeps comments and order)",
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: readErrors, Local: true,
			Examples: []op.Example{
				{Title: "turn autopush off", CLI: "lore config set live.autopush false", Want: "live.autopush"},
				{Title: "a longer lease", CLI: "lore config set lease.ttl 45m", Want: "lease.ttl"},
			},
		}, func(ctx context.Context, in SetIn) (SetOut, error) {
			return set(ctx, p, in)
		}),
		op.Define(op.Spec{
			Name: OpList, Summary: "List every key set in ~/.lore/config.yaml",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors, Local: true,
			Examples: []op.Example{{Title: "every key", CLI: "lore config list", Want: ""}},
		}, func(ctx context.Context, _ ListIn) (ListOut, error) {
			return list(ctx, p)
		}),
		op.Define(op.Spec{
			Name: OpCheck, Summary: "Validate ~/.lore/config.yaml (orgs, stores, live settings) and list every problem",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors, Local: true,
			Examples: []op.Example{{Title: "validate", CLI: "lore config check", Want: "✓"}},
		}, func(ctx context.Context, _ CheckIn) (CheckOut, error) {
			out, err := p.CheckConfig(ctx)
			if err != nil {
				return CheckOut{}, err
			}
			if out.Orgs == nil {
				out.Orgs = []string{}
			}
			if out.Warnings == nil {
				out.Warnings = []string{}
			}
			return out, nil
		}),
	}
}

// get walks the dotted key; a missing part is E_NOT_FOUND.
func get(ctx context.Context, p Port, key string) (Entry, error) {
	parts, err := keyParts(key)
	if err != nil {
		return Entry{}, err
	}
	f, err := p.ReadConfigFile(ctx)
	if err != nil {
		return Entry{}, err
	}
	doc, err := parseDoc(f.Path, f.Raw)
	if err != nil {
		return Entry{}, err
	}
	n := doc.Content[0]
	for _, part := range parts {
		if n = child(n, part); n == nil {
			return Entry{}, errs.New(errs.NotFound, fmt.Sprintf("config key %q not found", key)).
				WithHint("`lore config list` shows the keys set in " + f.Path)
		}
	}
	return Entry{Key: key, Value: scalarText(n)}, nil
}

// set edits one scalar and writes the file only when the result is a
// config lore reads and accepts (nothing written otherwise).
func set(ctx context.Context, p Port, in SetIn) (SetOut, error) {
	parts, err := keyParts(in.Key)
	if err != nil {
		return SetOut{}, err
	}
	f, err := p.ReadConfigFile(ctx)
	if err != nil {
		return SetOut{}, err
	}
	doc, err := parseDoc(f.Path, f.Raw)
	if err != nil {
		return SetOut{}, err
	}
	if serr := setNode(doc.Content[0], parts, in.Value); serr != nil {
		return SetOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("config key %q: %v", in.Key, serr))
	}
	raw, err := encodeDoc(doc)
	if err != nil {
		return SetOut{}, errs.Wrap(errs.Internal, "encode "+f.Path, err)
	}
	v, err := p.ValidateConfig(ctx, raw)
	if err != nil {
		return SetOut{}, err
	}
	switch {
	case len(v.Unknown) > 0:
		return SetOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("config key %q is not a key lore reads (%s)", in.Key, v.Unknown[0])).
			WithHint("nothing written; team rules are set with `lore policy set`")
	case v.Decode != nil:
		return SetOut{}, errs.Wrap(errs.InvalidInput, fmt.Sprintf("config key %q: bad value %q", in.Key, in.Value), v.Decode).
			WithHint("nothing written")
	case v.Invalid != nil:
		return SetOut{}, errs.Wrap(errs.ConfigInvalid, "refusing to write "+f.Path, v.Invalid).WithHint("nothing written")
	}
	f.Raw = raw
	if err := p.WriteConfigFile(ctx, f); err != nil {
		return SetOut{}, err
	}
	return SetOut{Key: in.Key, Value: in.Value, Path: f.Path}, nil
}

// list flattens every scalar leaf, sorted by key.
func list(ctx context.Context, p Port) (ListOut, error) {
	f, err := p.ReadConfigFile(ctx)
	if err != nil {
		return ListOut{}, err
	}
	doc, err := parseDoc(f.Path, f.Raw)
	if err != nil {
		return ListOut{}, err
	}
	flat := map[string]string{}
	flatten("", doc.Content[0], flat)
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ListOut{Entries: make([]Entry, 0, len(keys)), Count: len(keys)}
	for _, k := range keys {
		out.Entries = append(out.Entries, Entry{Key: k, Value: flat[k]})
	}
	return out, nil
}
