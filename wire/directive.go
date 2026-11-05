package wire

// directive.go — the file side of `lore directive install`
// (ops/directive.Manager): which tool files a repo uses and the read /
// write of one file under the repo root. Ported from v0.1.17's
// resolveContextTargets + installDirective
// (saas/cmd/cli/render.go, directive.go), same rules (PASS3 ONE-PATH).

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	opsdirective "github.com/thesatellite-ai/lore/ops/directive"
	"github.com/thesatellite-ai/lore/store"
)

// directiveFileMode is the mode of a tool file directive install creates
// (v0.1.17 cliFileMode); an existing file keeps its own.
const directiveFileMode fs.FileMode = 0o600

// directiveDirMode is the mode of a parent folder it creates (cliDirMode).
const directiveDirMode fs.FileMode = 0o750

var _ opsdirective.Manager = (*App)(nil)

// toolRegistry is the built-in tool profiles plus the user's own from
// config.yaml (context.profiles); TOOL NEUTRAL + no-hardwire.
func (a *App) toolRegistry(ctx context.Context) (*toolprofile.Registry, error) {
	user, err := a.ToolProfiles(ctx)
	if err != nil {
		return nil, err
	}
	reg, err := toolprofile.New(user)
	if err != nil {
		return nil, errs.Wrap(errs.ConfigInvalid, "context.profiles in ~/.lore/"+ConfigFile, err).
			WithHint("run `lore config check` for every problem with its location")
	}
	return reg, nil
}

// repoProbe reads pointer files under a repo root for toolprofile.Resolve.
type repoProbe struct{ root string }

// State implements toolprofile.Probe.
func (p repoProbe) State(rel string) toolprofile.FileState {
	data, err := os.ReadFile(filepath.Join(p.root, filepath.FromSlash(rel)))
	switch {
	case err != nil:
		return toolprofile.FileMissing
	case toolprofile.HasPointer(string(data)):
		return toolprofile.FileWired
	default:
		return toolprofile.FilePresent
	}
}

// contextTargetsSetting reads the repo's context.targets row (Q-R2-3). A
// broken policy row fails closed (E_CONFIG_INVALID); a core that cannot
// open at all falls back to the tool files with a note, as v0.1.17.
func (a *App) contextTargetsSetting(ctx context.Context) (names []string, ok bool, note string, err error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		if errs.Has(err, errs.ConfigInvalid) {
			return nil, false, "", err
		}
		return nil, false, "WARN: could not read the context.targets setting (" + err.Error() + "); using the repo's tool files", nil
	}
	set, err := eng.Policy(store.SourceRepo)
	if err != nil {
		return nil, false, "", err
	}
	names, ok = set.ContextTargets()
	return names, ok, "", nil
}

// DirectiveTargets implements opsdirective.Manager (v0.1.17 resolveTargets
// + printTargetPick).
func (a *App) DirectiveTargets(ctx context.Context, values []string) (opsdirective.Pick, error) {
	reg, err := a.toolRegistry(ctx)
	if err != nil {
		return opsdirective.Pick{}, err
	}
	root := a.opts.RepoDir
	var pick opsdirective.Pick
	if len(values) > 0 {
		for _, v := range values {
			if p, ok := reg.Lookup(v); ok {
				pick.Targets = append(pick.Targets, opsdirective.Target{File: p.PointerFile, Profile: p})
				continue
			}
			rel := v
			if filepath.IsAbs(v) {
				if r, rerr := filepath.Rel(root, v); rerr == nil && !strings.HasPrefix(r, "..") {
					rel = r
				}
			}
			if p, ok := reg.ByPointerFile(filepath.ToSlash(rel)); ok {
				pick.Targets = append(pick.Targets, opsdirective.Target{File: p.PointerFile, Profile: p})
				continue
			}
			// Any other file, wired with an @import pointer as v0.1.17's
			// --target was.
			pick.Targets = append(pick.Targets, opsdirective.Target{
				File:    v,
				Profile: toolprofile.Profile{Name: v, PointerFile: filepath.ToSlash(v), Import: toolprofile.ImportAt},
			})
		}
		return pick, nil
	}
	names, ok, note, err := a.contextTargetsSetting(ctx)
	if err != nil {
		return opsdirective.Pick{}, err
	}
	if note != "" {
		pick.Notes = append(pick.Notes, note)
	}
	var sel toolprofile.Selection
	if ok {
		// Q-R2-3: an unknown profile in the row is refused, never skipped.
		sel, err = reg.Select(names, repoProbe{root: root})
		if err != nil {
			return opsdirective.Pick{}, errs.Wrap(errs.InvalidInput, "the repo's "+string(policy.KeyContextTargets)+" setting", err).
				WithHint("change it with: lore policy set " + string(policy.KeyContextTargets) + " <profiles> --yes, or add the profile under context.profiles in ~/.lore/config.yaml")
		}
	} else {
		sel = reg.Resolve(repoProbe{root: root})
	}
	for _, p := range sel.Profiles {
		pick.Targets = append(pick.Targets, opsdirective.Target{File: p.PointerFile, Profile: p})
	}
	pick.Notes = append(pick.Notes, targetPickNotes(sel, pick.Targets)...)
	return pick, nil
}

// targetPickNotes are v0.1.17 printTargetPick's lines: how the targets
// were chosen (silent for an already-wired repo and for the settings row),
// each tool file found but not wired, and each wired file the setting
// does not name (DECISION P2-OLDCLI-2).
func targetPickNotes(sel toolprofile.Selection, targets []opsdirective.Target) []string {
	var out []string
	switch sel.Reason {
	case toolprofile.ReasonDetected, toolprofile.ReasonFallback:
		parts := make([]string, 0, len(targets))
		for _, t := range targets {
			if t.Profile.HasPointerFile() {
				parts = append(parts, t.Profile.Name+" ("+t.File+")")
			} else {
				parts = append(parts, t.Profile.Name)
			}
		}
		why := "detected in this repo; change with --target <profile>"
		if sel.Reason == toolprofile.ReasonFallback {
			why = "no AI tool file found; AGENTS.md is the cross-tool file, change with --target <profile>"
		}
		out = append(out, "· context targets: "+strings.Join(parts, ", ")+" ("+why+")")
	case toolprofile.ReasonWired, toolprofile.ReasonSetting:
	}
	key := string(policy.KeyContextTargets)
	for _, p := range sel.Unwired {
		hint := "lore render --target " + p.Name
		if sel.Reason == toolprofile.ReasonSetting {
			hint = "lore policy set " + key + " <profiles incl. " + p.Name + "> --yes"
		}
		out = append(out, "· "+p.PointerFile+" found but not wired to lore; add it with: "+hint)
	}
	for _, p := range sel.Unlisted {
		out = append(out, fmt.Sprintf("· %s carries lore's pointer but %s does not name it, so render leaves it as is; list it with: lore policy set %s <profiles incl. %s> --yes",
			p.PointerFile, key, key, p.Name))
	}
	return out
}

// EditRepoFile implements opsdirective.Manager. The write goes through an
// os.Root on the parent folder (P2-LINT-5: a final symlink leading outside
// it is refused), as v0.1.17's writeFileInDir.
func (a *App) EditRepoFile(_ context.Context, file string, edit opsdirective.Edit) (string, error) {
	target := file
	if !filepath.IsAbs(target) {
		target = filepath.Join(a.opts.RepoDir, filepath.FromSlash(file))
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", errs.Wrap(errs.BadPath, "resolve "+target, err)
	}
	existing, exists := "", true
	data, err := os.ReadFile(filepath.Clean(abs))
	switch {
	case err == nil:
		existing = string(data)
	case errors.Is(err, fs.ErrNotExist):
		exists = false
	default:
		return abs, errs.Wrap(errs.Internal, "read "+abs, err)
	}
	out, write := edit(existing, exists)
	if !write {
		return abs, nil
	}
	if err = os.MkdirAll(filepath.Dir(abs), directiveDirMode); err != nil {
		return abs, errs.Wrap(errs.Internal, "mkdir "+filepath.Dir(abs), err)
	}
	root, err := os.OpenRoot(filepath.Dir(abs))
	if err != nil {
		return abs, errs.Wrap(errs.Internal, "write "+abs, err)
	}
	if err = errors.Join(root.WriteFile(filepath.Base(abs), []byte(out), directiveFileMode), root.Close()); err != nil {
		return abs, errs.Wrap(errs.Internal, "write "+abs, err)
	}
	return abs, nil
}
