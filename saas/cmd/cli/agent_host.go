// agent_host.go — the old binary's opsagent.Host (PASS3 ONE-PATH,
// P3-RUNS-6): `lore agent start` is the operation agent.start; the
// --label mode registers through the App's roster (opsinfra.Roster), this
// host answers who the caller is and runs the --tool session hook.
package main

import (
	"context"

	opsagent "github.com/thesatellite-ai/lore/ops/agent"
)

// agentHost is wire.SessionHosts.Agent.
type agentHost struct{}

var _ opsagent.Host = agentHost{}

// CallerIsAgent implements opsagent.Host (the coordination world's
// identity: detected or LORE_ACTOR=agent:…).
func (agentHost) CallerIsAgent(ctx context.Context) (bool, error) {
	w, err := openCoordWorld(ctx)
	if err != nil {
		return false, asCoreError(err)
	}
	return w.IsAgent(), nil
}

// CallerLabel implements opsagent.Host: @handle, else the display name.
func (agentHost) CallerLabel(ctx context.Context) (string, error) {
	w, err := openCoordWorld(ctx)
	if err != nil {
		return "", asCoreError(err)
	}
	return w.Caller().Label, nil
}

// RecordHook implements opsagent.Host: v0.1.17's --tool mode, which
// prints at most one stderr line and never fails (AGENT_IDENTITY 6.4).
func (agentHost) RecordHook(ctx context.Context, in opsagent.HookIn) {
	runAgentHook(ctx, in.Tool, in.Session, in.Model, in.Stdin)
}
