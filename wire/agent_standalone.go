package wire

// agent_standalone.go — the `lore agent start` host of cmd/lore
// (ops/agent.Host, P3-CMDLORE-6): who the caller is, from the same
// identity chain the core writes with. The --label mode registers through
// the App's roster (wire/roster_ops.go). The --tool session hook is not
// recorded on cmd/lore yet (it prints nothing and never fails, AGENT_IDENTITY
// 6.4); the machine sessions host is p3-local's.

import (
	"context"

	opsagent "github.com/thesatellite-ai/lore/ops/agent"
	"github.com/thesatellite-ai/lore/schema"
)

// standaloneAgent implements opsagent.Host for cmd/lore.
type standaloneAgent struct{ opts Options }

var _ opsagent.Host = (*standaloneAgent)(nil)

// CallerIsAgent implements opsagent.Host (detected or LORE_ACTOR=agent:…).
func (h *standaloneAgent) CallerIsAgent(ctx context.Context) (bool, error) {
	id, err := resolveIdentity(ctx, h.opts)
	if err != nil {
		return false, err
	}
	return id.Kind == schema.ActorKindAgent, nil
}

// CallerLabel implements opsagent.Host: @handle, else the display name.
func (h *standaloneAgent) CallerLabel(ctx context.Context) (string, error) {
	id, err := resolveIdentity(ctx, h.opts)
	if err != nil {
		return "", err
	}
	if id.Handle != "" {
		return rosterHandleMark + id.Handle, nil
	}
	return id.DisplayName, nil
}

// RecordHook implements opsagent.Host: a no-op on cmd/lore for now (the
// hook must never fail a tool's session start).
func (*standaloneAgent) RecordHook(context.Context, opsagent.HookIn) {}
