package config

// fmt.go — `lore config fmt` (config.fmt): rewrite ~/.lore/config.yaml in
// block style when a list or mapping in it holds lists or mappings on one
// flow line (`setups: [{name: me, …}]`).
//
// Why: yaml.v3 writes every child of a flow node in flow style, so a file
// once saved as `setups: []` and then appended to kept the whole setups
// list on one line. Since b6a39e32 every write repairs it (yamlfmt.Block),
// but a file nobody writes again stays that way, and a read must never
// rewrite it. doctor finds such a file (wire DoctorConfigStyle) and names
// this command; `lore doctor --fix` runs the same Format.
//
//	read ─► yamlfmt.FlowKeys (nothing flow-nested: nothing written)
//	     ─► yamlfmt.EncodeLike (comments, key order, indent and line
//	        endings kept) ─► the schema must accept the result as it
//	        accepted the file ─► WriteConfigFile (atomic, E_STALE when the
//	        file changed since the read)

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/logic/yamlfmt"
	"github.com/thesatellite-ai/lore/model/errs"
)

// OpFmt is `lore config fmt`.
const OpFmt = "config.fmt"

// FmtIn is the input of config.fmt.
type FmtIn struct {
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"name the keys written on one line; write nothing"`
}

// FmtOut is what config.fmt found and did.
type FmtOut struct {
	Path string `json:"path"`
	// Keys are the dotted keys whose lists or mappings were on one flow
	// line (empty: the file already reads in block style).
	Keys []string `json:"keys"`
	// Written is true when the file was rewritten.
	Written bool `json:"written"`
	DryRun  bool `json:"dry_run,omitempty"`
}

// Text is the result line.
func (o FmtOut) Text() string {
	switch {
	case len(o.Keys) == 0:
		return fmt.Sprintf("✓ %s is already in block style; nothing written\n", o.Path)
	case o.DryRun:
		return fmt.Sprintf("would rewrite %s in block style (%s on one line); nothing written (--dry-run)\n", o.Path, strings.Join(o.Keys, ", "))
	}
	return fmt.Sprintf("✓ rewrote %s in block style (%s); comments and key order kept\n", o.Path, strings.Join(o.Keys, ", "))
}

// FlowKeys are the keys of f written in flow style that block style would
// spread over lines (yamlfmt.FlowKeys); nil for an absent or empty file.
// It reads only.
func FlowKeys(f File) ([]string, error) {
	if !f.Exists {
		return nil, nil
	}
	doc, err := parseDoc(f.Path, f.Raw)
	if err != nil {
		return nil, err
	}
	return yamlfmt.FlowKeys(doc), nil
}

// Format is config.fmt: it rewrites the file in block style when FlowKeys
// finds a key on one flow line, else writes nothing. A rewrite the schema
// would refuse while it accepts the file as it is (it never should: only
// the layout changes) is refused and nothing is written.
func Format(ctx context.Context, p Port, dryRun bool) (FmtOut, error) {
	f, err := p.ReadConfigFile(ctx)
	if err != nil {
		return FmtOut{}, err
	}
	out := FmtOut{Path: f.Path, Keys: []string{}, DryRun: dryRun}
	keys, err := FlowKeys(f)
	if err != nil || len(keys) == 0 {
		return out, err
	}
	out.Keys = keys
	if dryRun {
		return out, nil
	}
	doc, err := parseDoc(f.Path, f.Raw)
	if err != nil {
		return FmtOut{}, err
	}
	raw, err := encodeDoc(doc, f.Raw)
	if err != nil {
		return FmtOut{}, errs.Wrap(errs.Internal, "encode "+f.Path, err)
	}
	if err := refuseReformat(ctx, p, f, raw); err != nil {
		return FmtOut{}, err
	}
	f.Raw = raw
	if err := p.WriteConfigFile(ctx, f); err != nil {
		return FmtOut{}, err
	}
	out.Written = true
	return out, nil
}

// refuseReformat is E_CONFIG_INVALID when the schema refuses raw (the file
// in block style) while it accepts f as read: a rewrite that changed more
// than the layout is never written.
func refuseReformat(ctx context.Context, p Port, f File, raw []byte) error {
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
		return nil // already invalid: the layout is not what breaks it
	}
	return errs.Wrap(errs.ConfigInvalid, "the block-style rewrite of "+f.Path+" does not validate", bad).
		WithHint("nothing written; `lore config check` shows the rules, or spread the one-line lists over lines by hand")
}
