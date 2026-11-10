package settings

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// ToolProfiler is an optional Env upgrade: the user's own tool profiles
// (context.profiles of ~/.lore/config.yaml), added to the built-ins when a
// context.targets value is checked. An Env without it checks against the
// built-in profiles only, so a user profile name is refused rather than
// stored unchecked (fail closed).
type ToolProfiler interface {
	ToolProfiles(ctx context.Context) ([]toolprofile.Profile, error)
}

// toolRegistry is the profile registry the Env knows: built-ins plus the
// ToolProfiler's profiles.
func toolRegistry(ctx context.Context, env Env) (*toolprofile.Registry, error) {
	var user []toolprofile.Profile
	if tp, ok := env.(ToolProfiler); ok {
		ps, err := tp.ToolProfiles(ctx)
		if err != nil {
			return nil, err
		}
		user = ps
	}
	return toolprofile.New(user)
}

// checkContextTargets refuses a context.targets value naming a profile the
// registry does not know (E_INVALID_INPUT naming it).
//
// Q-R2-3: the row is what render wires, so a typo must be refused at the
// one command that writes it, never become a row that wires nothing.
func checkContextTargets(ctx context.Context, env Env, raw string) error {
	reg, err := toolRegistry(ctx, env)
	if err != nil {
		return err
	}
	return reg.CheckNames(splitList(raw))
}

// ContextTargetsFields are the fields of the repo-level context.targets
// policies row naming profiles, the same shape policy.set writes (key,
// value as a JSON text list, scope repo). `lore init` writes its chosen
// targets with them (Q-R2-3), so the row init writes and the row `lore
// policy set context.targets` edits are one row.
func ContextTargetsFields(profiles []string) (map[string]value.Value, error) {
	l, err := textList(profiles)
	if err != nil {
		return nil, err
	}
	jv, err := asJSON(l)
	if err != nil {
		return nil, err
	}
	fields := map[string]value.Value{pValue: jv}
	if err := optTexts(fields, map[string]string{pKey: string(policy.KeyContextTargets), pScope: scopeOf(store.SourceRepo)}); err != nil {
		return nil, err
	}
	return fields, nil
}
