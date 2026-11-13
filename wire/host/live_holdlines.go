package host

// live_holdlines.go — the order of a CLI command's output (v0.1.17): its
// own result first, then the live lines its run's end printed
//
//	op ─▶ next ─▶ (deferred) liveRun.finish: push ─▶ lines HELD
//	surface prints the result ─▶ cmd/lore: Live.FlushHeld ─▶ held lines
//
// Only with LiveConfig.HoldFinishLines, and never for a `lore api` request
// (a server run has no "after the command" to flush at).

import (
	"context"
	"io"
	"os"

	"github.com/thesatellite-ai/lore/wire"
)

// holdLines starts holding line() output for the run's finish and returns
// the function that stops it (a no-op when nothing is held).
func (h *liveHost) holdLines(ctx context.Context) func() {
	if !h.cfg.HoldFinishLines || wire.IsRemoteRequest(ctx) {
		return func() {}
	}
	h.mu.Lock()
	h.holding = true
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		h.holding = false
		h.mu.Unlock()
	}
}

// FlushHeld prints the held live lines, in order, and forgets them. cmd/lore
// calls it once the command printed its result (or its error).
func (l *Live) FlushHeld() {
	h := l.h
	h.mu.Lock()
	defer h.mu.Unlock()
	w := h.cfg.Stderr
	if w == nil {
		w = os.Stderr
	}
	for _, text := range h.held {
		_, _ = io.WriteString(w, text+"\n") // as line(): a lost status line is not a lost write
	}
	h.held = nil
}
