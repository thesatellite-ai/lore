package wire_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestAddCommandsNeverReadStdinUnasked pins INTEG-QUESTIONS-3: an add
// command that read stdin by itself when its body was missing hung an agent
// harness whose stdin is an open pipe nobody writes. Stdin is read only
// behind an explicit flag (task split --from -, vars set --set-stdin,
// context --hook-stdin). Every registered *.add operation, run with no
// input while stdin is such a pipe, answers (a refusal is fine) instead of
// waiting. Not parallel: it swaps os.Stdin for the run.
func TestAddCommandsNeverReadStdinUnasked(t *testing.T) {
	h := opstest.New(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = w.Close()
		_ = r.Close()
	})
	n := 0
	for _, e := range h.App.Registry().Entries() {
		name := e.Spec().Name
		if !strings.HasSuffix(name, ".add") {
			continue
		}
		n++
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = h.Run(name, `{}`)
		}()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatalf("%s with no input waited on stdin (an open pipe nobody writes)", name)
		}
	}
	if n < 10 {
		t.Fatalf("only %d add operations found; the registry walk is broken", n)
	}
}
