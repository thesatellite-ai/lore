package doctor

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/op"
)

// accessEnv records the access its store checks ran under.
type accessEnv struct {
	got  op.Access
	seen bool
}

func (e *accessEnv) DoctorHost(context.Context) (Host, bool) { return nil, false }

func (e *accessEnv) DoctorChecks(ctx context.Context, _ bool) []CheckRun {
	e.got, e.seen = engine.AccessOf(ctx)
	return nil
}

func (e *accessEnv) BinaryVersion() string { return "test" }

// TestPlainDoctorChecksAreReads (devco D-441): a plain `lore doctor` runs
// its store checks as a read, so a check that names a store folder's
// project finds no row there and makes none (in a store clone's root a
// plain doctor committed a nameless project row).
func TestPlainDoctorChecksAreReads(t *testing.T) {
	t.Parallel()
	env := &accessEnv{}
	if _, err := Run(context.Background(), env, RunIn{}); err != nil {
		t.Fatal(err)
	}
	if !env.seen || env.got != op.AccessRead {
		t.Fatalf("plain doctor's checks ran with access %v (set %v), want a read", env.got, env.seen)
	}
}
