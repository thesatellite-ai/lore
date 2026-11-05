// session_setup.go — `lore setup --agent-hooks [--dry-run] [--remove]
// [--yes]` (AGENT_IDENTITY_DESIGN 6.4): the agent-hooks half runs on
// wire/renderhost (SetupHost, moved there so cmd/lore serves it too); the
// lore.db schema half stays here (setupHost.UpdateSchema, setup_cmd.go).
package main

import (
	"context"

	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// setupHost is the machinery behind the setup.run operation (ops/setup,
// wire.ContextHosts.Setup; PASS3 ONE-PATH, P3-MISC-4).
type setupHost struct{}

// PlanAgentHooks implements opssetup.Host (renderhost.SetupHost).
func (setupHost) PlanAgentHooks(ctx context.Context, remove, promptSearch bool) ([]opssetup.HookPlan, error) {
	return rh().Setup(nil).PlanAgentHooks(ctx, remove, promptSearch)
}

// WriteAgentHooks implements opssetup.Host (renderhost.SetupHost).
func (setupHost) WriteAgentHooks(ctx context.Context, plans []opssetup.HookPlan, remove bool) error {
	return rh().Setup(nil).WriteAgentHooks(ctx, plans, remove)
}
