package host

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire"
)

// TestHoldFinishLines: with HoldFinishLines a CLI run's end-of-run lines
// wait for FlushHeld (so they follow the command's result, v0.1.17
// order); lines outside a finish print at once; a `lore api` request
// never holds; without the option nothing is held.
func TestHoldFinishLines(t *testing.T) {
	cases := []struct {
		name   string
		hold   bool
		remote bool
		before string // printed before FlushHeld
		after  string // printed by FlushHeld
	}{
		{name: "cli run, held", hold: true, before: "outside\n", after: "lore live: (live, pushed)\n"},
		{name: "api request, never held", hold: true, remote: true, before: "outside\nlore live: (live, pushed)\n"},
		{name: "option off", before: "outside\nlore live: (live, pushed)\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out strings.Builder
			l := NewLive(LiveConfig{Stderr: &out, HoldFinishLines: c.hold})
			ctx := context.Background()
			if c.remote {
				ctx = wire.RemoteRequest(ctx)
			}
			l.h.line("outside")
			r := &liveRun{h: l.h, sessions: map[string]*liveSession{}}
			stop := l.h.holdLines(ctx)
			l.h.line("lore live: (live, pushed)")
			stop()
			r.finish(ctx) // no sessions: holds and releases around nothing
			if got := out.String(); got != c.before {
				t.Fatalf("before flush %q, want %q", got, c.before)
			}
			l.FlushHeld()
			if got := strings.TrimPrefix(out.String(), c.before); got != c.after {
				t.Fatalf("flushed %q, want %q", got, c.after)
			}
			l.FlushHeld()
			if got := strings.TrimPrefix(out.String(), c.before+c.after); got != "" {
				t.Fatalf("a second flush printed %q again", got)
			}
		})
	}
}
