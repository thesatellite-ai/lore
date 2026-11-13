package renderhost

// init.go — the tool-file part of `lore init` (TOOL NEUTRAL, Q-R2-4): wire
// the AI tools this repo already uses (their files exist), else AGENTS.md,
// pointing each at .lore/LORE.md, and record the chosen profiles as the
// repo's context.targets policy row. Moved from the old binary's
// wireToolFiles so cmd/lore's init does the same.

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Field names of a policies row (schema.Builtin) init reads and updates.
const (
	policyFieldKey   = "key"
	policyFieldValue = "value"
)

// WireToolFiles stitches the lore pointer block into each context target of
// the repo at root (values from --target, else the repo's context.targets
// row, else toolprofile.Resolve), prints the chosen targets to w and records
// them as the repo's context.targets row. Init never asks which tools to
// wire (Q-R2-4): it prints what it chose and why, and --target overrides it.
func (h *Host) WireToolFiles(ctx context.Context, w *strings.Builder, root string, values []string) error {
	pick, err := h.resolveTargets(ctx, root, values, func(ctx context.Context) ([]string, bool, error) {
		return h.contextTargetsOf(ctx, h.app)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s context targets: %s (%s)\n", h.decor.muted("·"), describeTargets(pick.targets), initTargetReason(len(values) > 0, pick.sel.Reason))
	out := filepath.Join(root, filepath.FromSlash(toolprofile.CanonicalOutput))
	for _, t := range pick.targets {
		if err := h.stitchPointer(w, root, t, out); err != nil {
			return err
		}
	}
	if len(values) == 0 && pick.sel.Reason == toolprofile.ReasonSetting {
		return nil // the row already says it (a re-init over shared data)
	}
	var names []string
	for _, t := range pick.targets {
		if t.known && !slices.Contains(names, t.profile.Name) {
			names = append(names, t.profile.Name)
		} else if !t.known {
			fmt.Fprintf(w, "%s %s is not a tool profile, so it is wired but not saved in %s\n", h.decor.muted("·"), t.file, policy.KeyContextTargets)
		}
	}
	if len(names) == 0 {
		return nil
	}
	if err := h.writeContextTargets(ctx, names); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s saved as %s; change with: lore policy set --key %s --value <profiles> --yes\n", h.decor.muted("·"), policy.KeyContextTargets, policy.KeyContextTargets)
	return nil
}

// addContextTargets is render --add-target: the repo's effective targets
// (its context.targets row, else the detected tool files) plus add, saved
// as the context.targets row, so the render that follows wires them and
// every later plain render keeps them. Every value must be a tool profile
// (a file name is refused: the row holds profiles only); a profile already
// in the targets is left alone and nothing is written when none is new.
func (h *Host) addContextTargets(ctx context.Context, w *strings.Builder, root string, add []string) error {
	reg, err := h.loadToolRegistry(ctx)
	if err != nil {
		return err
	}
	if err := reg.CheckNames(add); err != nil {
		return err
	}
	pick, err := h.resolveTargets(ctx, root, nil, h.coreContextTargets)
	if err != nil {
		return err
	}
	var names []string
	for _, t := range pick.targets {
		if t.known && !slices.Contains(names, t.profile.Name) {
			names = append(names, t.profile.Name)
		}
	}
	var added []string
	for _, a := range add {
		p, _ := reg.Lookup(a)
		if !slices.Contains(names, p.Name) {
			names = append(names, p.Name)
			added = append(added, p.Name)
		}
	}
	if len(added) == 0 {
		fmt.Fprintf(w, "%s %s already in %s\n", h.decor.muted("·"), strings.Join(add, ", "), policy.KeyContextTargets)
		return nil
	}
	if err := h.writeContextTargets(ctx, names); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s added %s to %s (now %s)\n", h.decor.muted("·"), strings.Join(added, ", "), policy.KeyContextTargets, strings.Join(names, ", "))
	return nil
}

// initTargetReason says in words why init chose its targets.
func initTargetReason(fromFlag bool, r toolprofile.Reason) string {
	if fromFlag {
		return "from --target"
	}
	return describeReason(r)
}

// writeContextTargets sets the repo's context.targets policy row to names
// (updating the live row when there is one).
func (h *Host) writeContextTargets(ctx context.Context, names []string) error {
	fields, err := settings.ContextTargetsFields(names)
	if err != nil {
		return err
	}
	rows, err := h.app.List(ctx, store.SourceRepo, schema.TablePolicies)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if k, ok := r.Fields[policyFieldKey].(value.Text); ok && !r.Deleted && k.String() == string(policy.KeyContextTargets) {
			_, err = h.app.Update(ctx, engine.Ref{Store: store.SourceRepo, Table: schema.TablePolicies, ID: r.ID}, map[string]value.Value{policyFieldValue: fields[policyFieldValue]})
			return err
		}
	}
	_, err = h.app.Create(ctx, store.SourceRepo, schema.TablePolicies, fields)
	return err
}
