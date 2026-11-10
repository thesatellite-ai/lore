package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// testActor is a fixed actor id for tests (the 32 hex digits of an
// act_ id are arbitrary).
const testActor = "act_0e45506e64357c94a87e70ef1c6bbba9"

// newTestApp builds an unopened App rooted at dir, writing as testActor.
func newTestApp(t *testing.T, dir string) *App {
	t.Helper()
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{RepoDir: dir, Home: t.TempDir(), Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a
}

// background is the context of a test step.
func background() context.Context { return context.Background() }
