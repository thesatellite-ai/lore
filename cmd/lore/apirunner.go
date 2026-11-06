package main

// apirunner.go — the runner `lore api` serves REST requests through: the
// CLI's own host runner (wire/host.Runner with every hook), never the bare
// App, as v0.1.17's api_runner.go did. Without the hooks a request skipped
// the live hook, and with it F16 on the branch rows of a client repo
// (wire/host leakCheckBranch): an edit, or a create of a table the
// engine's repo-write check does not route (a hotfix), wrote internal text
// into the client's .lore/data over REST while the CLI refused it
// (teame2e TestF16_EveryRepoWritePathIsChecked).
//
// Requests run one at a time: the live hook keeps one session per
// operation and the runner's State is per process, as in a CLI process;
// each request then ends its live session (push, land, release the lock)
// like the end of a CLI command. Each request's context is marked
// wire.RemoteRequest, so the audit never links its writes to the server
// process's own (terminal) session row (OV3).

import (
	"context"
	"sync"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/wire"
)

// apiRunner serves REST requests through inner, one at a time.
type apiRunner struct {
	inner op.Runner
	mu    sync.Mutex
}

var _ op.Runner = (*apiRunner)(nil)

// Run implements op.Runner.
func (r *apiRunner) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// OV3: the request's writes carry no session of the server process
	return r.inner.Run(wire.RemoteRequest(ctx), e, d, c)
}
