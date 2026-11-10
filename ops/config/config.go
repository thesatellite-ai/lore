// Package config defines `lore config get|set|unset|list|check|fmt` (layer L5,
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
//	config unset <key>        read ─► remove the node (comments, order of the rest
//	                          kept; an emptied parent mapping goes too) ─► refuse
//	                          only when the removal breaks a valid file ─► write
//	config check              Schema.CheckConfig (every problem at once)
//	config fmt                read ─► a list or mapping on one flow line? ─► block
//	                          style, comments and order kept ─► write (fmt.go)
//
// The file schema (which keys lore reads, their validation) is the org
// configuration's, supplied by the composition root through Port.
package config

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore config …`).
const (
	OpGet   = "config.get"
	OpSet   = "config.set"
	OpUnset = "config.unset"
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
	// Read is the bytes ReadConfigFile found (nil when absent); a
	// read-modify-write keeps it as read and replaces only Raw, so
	// WriteConfigFile can refuse (E_STALE) when the file changed between
	// the read and the write instead of overwriting another edit.
	Read []byte
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

// UnsetIn is the input of config.unset.
type UnsetIn struct {
	Key    string `json:"key" arg:"0" desc:"a dotted path, e.g. personal.store or identity"`
	DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"show what would be removed; write nothing"`
}

// UnsetOut is the key removed, what it held (YAML for a mapping or a
// list) and the file.
type UnsetOut struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Path   string `json:"path"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// Text is the confirmation line (a preview under --dry-run).
func (o UnsetOut) Text() string {
	head := fmt.Sprintf("✓ removed %s", o.Key)
	if o.DryRun {
		head = fmt.Sprintf("would remove %s", o.Key)
	}
	tail := ""
	if o.DryRun {
		tail = "; nothing written (--dry-run)"
	}
	if strings.Contains(o.Value, "\n") { // a mapping or a list: its YAML below
		return fmt.Sprintf("%s from %s%s:\n    %s\n", head, o.Path, tail, strings.ReplaceAll(o.Value, "\n", "\n    "))
	}
	return fmt.Sprintf("%s (%s) from %s%s\n", head, o.Value, o.Path, tail)
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
	fmt.Fprintf(&b, "✓ %s is valid: %d org entr%s", o.Path, len(o.Orgs), suffix)
	if len(o.Orgs) > 0 { // no empty "()" when the file names no org
		fmt.Fprintf(&b, " (%s)", strings.Join(o.Orgs, ", "))
	}
	b.WriteString("\n")
	for _, w := range o.Warnings {
		b.WriteString("  warning: " + w + "\n")
	}
	return b.String()
}

// readErrors are the codes reading the file can return.
var readErrors = []errs.Code{errs.InvalidInput, errs.NotFound, errs.ConfigInvalid, errs.Unsupported, errs.Internal}

// setErrors adds what the locked write of `config set` refuses with: the
// file changed since it was read (E_STALE), or another process kept the
// config.yaml lock (E_STORE_LOCKED).
var setErrors = append(slices.Clone(readErrors), errs.Stale, errs.StoreLocked)

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
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: setErrors, Local: true,
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
		op.Define(op.Spec{
			Name: OpUnset, Summary: "Remove a key of ~/.lore/config.yaml (keeps comments and order); --dry-run shows what would go",
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: setErrors, Local: true,
			Examples: []op.Example{
				{Title: "preview removing the personal store", CLI: "lore config unset personal.store --dry-run", Want: ""},
				{Title: "remove the identity block", CLI: "lore config unset identity", Want: ""},
			},
		}, func(ctx context.Context, in UnsetIn) (UnsetOut, error) {
			return unset(ctx, p, in)
		}),
		op.Define(op.Spec{
			Name: OpFmt, Summary: "Rewrite ~/.lore/config.yaml in block style when a list or mapping sits on one line (keeps comments and order)",
			Permission: op.PermAdmin, Access: op.AccessRead, Errors: setErrors, Local: true,
			Examples: []op.Example{
				{Title: "preview", CLI: "lore config fmt --dry-run", Want: ""},
				{Title: "rewrite one-line lists over lines", CLI: "lore config fmt", Want: ""},
			},
		}, func(ctx context.Context, in FmtIn) (FmtOut, error) {
			return Format(ctx, p, in.DryRun)
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

// hintBadValue and hintBadPath are the next steps of a refused set: the
// value does not have the key's type (the message carries the decoder's
// line and key), or the path runs into a list, a mapping or a value.
const (
	hintBadValue = "nothing written; docs/reference/config.md lists every key with its type (a bool is true or false, a duration 45m or 2h)"
	hintBadPath  = "nothing written; `lore config list` shows the keys set now: set a scalar key, edit lists (orgs, stores) by hand"
)

// policiesKey is config.yaml's machine-level policy map (FA-AGENT-6).
const policiesKey = "policies"

// set edits one scalar and writes the file only when the result is a
// config lore reads and accepts (nothing written otherwise).
func set(ctx context.Context, p Port, in SetIn) (SetOut, error) {
	parts, err := keyParts(in.Key)
	if err != nil {
		return SetOut{}, err
	}
	// FA-AGENT-6: the machine level of policy is edited by hand only (a
	// policy key holds dots, so a dotted path cannot name it); refused by
	// name now that config check knows `policies:` (P3-CFG-6).
	if parts[0] == policiesKey {
		return SetOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("config key %q is a policy key; lore config set does not edit policies:", in.Key)).
			WithHint("nothing written; edit policies: in ~/.lore/config.yaml by hand (it only tightens), or set a team rule with `lore policy set`")
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
		return SetOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("config key %q: %v", in.Key, serr)).WithHint(hintBadPath)
	}
	raw, err := encodeDoc(doc, f.Raw)
	if err != nil {
		return SetOut{}, errs.Wrap(errs.Internal, "encode "+f.Path, err)
	}
	v, err := p.ValidateConfig(ctx, raw)
	if err != nil {
		return SetOut{}, err
	}
	added, err := newUnknown(ctx, p, f.Raw, v.Unknown)
	if err != nil {
		return SetOut{}, err
	}
	switch {
	case len(added) > 0:
		return SetOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("config key %q is not a key lore reads (%s)", in.Key, added[0])).
			WithHint("nothing written; team rules are set with `lore policy set`")
	case v.Decode != nil:
		return SetOut{}, errs.Wrap(errs.InvalidInput, fmt.Sprintf("config key %q: bad value %q (%v)", in.Key, in.Value, v.Decode), v.Decode).
			WithHint(hintBadValue)
	case v.Invalid != nil:
		return SetOut{}, errs.Wrap(errs.ConfigInvalid, "refusing to write "+f.Path+": "+v.Invalid.Error(), v.Invalid).WithHint("nothing written")
	}
	f.Raw = raw
	if err := p.WriteConfigFile(ctx, f); err != nil {
		return SetOut{}, err
	}
	return SetOut{Key: in.Key, Value: in.Value, Path: f.Path}, nil
}

// newUnknown is the unknown-key warnings of a candidate file (after) that
// the file as read (raw) did not already have. Why: a key the person wrote
// by hand (`my_notes:`) is kept and tolerated, so it must not refuse every
// later `config set` of a key lore does read; only the key being set can
// make the set an unknown one. Warnings are compared without their line,
// which the set itself may move.
func newUnknown(ctx context.Context, p Port, raw []byte, after []string) ([]string, error) {
	if len(after) == 0 {
		return nil, nil
	}
	before, err := p.ValidateConfig(ctx, raw)
	if err != nil {
		return nil, err
	}
	had := map[string]int{}
	for _, u := range before.Unknown {
		had[unknownLineRe.ReplaceAllString(u, "")]++
	}
	var added []string
	for _, u := range after {
		k := unknownLineRe.ReplaceAllString(u, "")
		if had[k] > 0 {
			had[k]--
			continue
		}
		added = append(added, u)
	}
	return added, nil
}

// unknownLineRe is the "line N: " part of an unknown-key warning.
var unknownLineRe = regexp.MustCompile(`line \d+: `)

// unset removes one key. A removal that turns a valid file invalid (orgs.0.name
// under a store) is refused; one that leaves an already invalid file no
// better is allowed, so unset can remove the very key that broke the file.
func unset(ctx context.Context, p Port, in UnsetIn) (UnsetOut, error) {
	parts, err := keyParts(in.Key)
	if err != nil {
		return UnsetOut{}, err
	}
	if parts[0] == policiesKey && len(parts) > 1 {
		return UnsetOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("config key %q is a policy key; lore config unset does not edit inside policies:", in.Key)).
			WithHint("nothing written; edit policies: in ~/.lore/config.yaml by hand, or remove the whole block with lore config unset policies")
	}
	f, err := p.ReadConfigFile(ctx)
	if err != nil {
		return UnsetOut{}, err
	}
	doc, err := parseDoc(f.Path, f.Raw)
	if err != nil {
		return UnsetOut{}, err
	}
	gone := unsetNode(doc.Content[0], parts)
	if gone == nil {
		return UnsetOut{}, errs.New(errs.NotFound, fmt.Sprintf("config key %q is not set in %s", in.Key, f.Path)).
			WithHint("nothing written; `lore config list` shows the keys set now")
	}
	out := UnsetOut{Key: in.Key, Value: scalarText(gone), Path: f.Path, DryRun: in.DryRun}
	raw, err := encodeDoc(doc, f.Raw)
	if err != nil {
		return UnsetOut{}, errs.Wrap(errs.Internal, "encode "+f.Path, err)
	}
	if err := refuseBreaking(ctx, p, f, raw, in.Key); err != nil {
		return UnsetOut{}, err
	}
	if in.DryRun {
		return out, nil
	}
	f.Raw = raw
	if err := p.WriteConfigFile(ctx, f); err != nil {
		return UnsetOut{}, err
	}
	return out, nil
}

// refuseBreaking is E_CONFIG_INVALID when raw (the file after removing
// key) breaks a rule that f, as read, did not break.
func refuseBreaking(ctx context.Context, p Port, f File, raw []byte, key string) error {
	after, err := p.ValidateConfig(ctx, raw)
	if err != nil {
		return err
	}
	bad := after.Decode
	if bad == nil {
		bad = after.Invalid
	}
	if bad == nil {
		return nil
	}
	before, err := p.ValidateConfig(ctx, f.Raw)
	if err != nil {
		return err
	}
	if before.Decode != nil || before.Invalid != nil {
		return nil // already invalid: the removal may be the fix
	}
	return errs.Wrap(errs.ConfigInvalid, fmt.Sprintf("removing config key %q would break %s", key, f.Path), bad).
		WithHint("nothing written; `lore config check` shows the rules")
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
