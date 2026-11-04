// api_runner.go — the per-request lifecycle of `lore api`.
//
// Why it exists: the CLI runs one command per process, and the live
// session of a repo (live_wire.go: the per-repo processlock, fetch +
// integrate, the end-of-command push and landing) lives exactly as long as
// that command. `lore api` serves for hours in one process, so the session
// it opened on its first request held the repo's live lock for the whole
// server life: every CLI command in the same repo waited liveLockTimeout
// and then ran with live rows off.
//
// apiRunner makes each REST request one "command" around the SAME runner
// the CLI mounts (coreRunner, spec 9.2): requests run one at a time (the
// live session table is per process and not safe for concurrent use, as in
// a CLI process), and after each one the live work is finished exactly as
// at the end of a CLI command (push, land, release the lock) and the
// session table is reset, so the next request prepares afresh and a CLI
// command between two requests takes the lock normally.
package main

import (
	"context"
	"sync"

	"github.com/thesatellite-ai/lore/op"
)

// apiRunner serves REST requests through inner, one at a time, ending each
// request's live session like the end of a CLI command.
type apiRunner struct {
	inner op.Runner
	mu    sync.Mutex
}

var _ op.Runner = (*apiRunner)(nil)

// Run implements op.Runner.
func (r *apiRunner) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, err := r.inner.Run(ctx, e, d, c)
	ferr := endLiveCommand(ctx)
	if err == nil && ferr != nil {
		return op.Result{}, ferr
	}
	return res, err
}

// endLiveCommand finishes every live session of this process (push, land,
// release the per-repo lock: finishLive) and forgets them, so the next
// operation in the same process starts a new session the way a new CLI
// process would. It returns finishLive's refusal (a leak-blocked live
// write), which must fail the operation that wrote it.
func endLiveCommand(ctx context.Context) error {
	err := finishLive(ctx)
	clear(liveSessions)
	return err
}
