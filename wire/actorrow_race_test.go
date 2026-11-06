package wire

// actorrow_race_test.go — t-e2e5 (7c706afb): two processes making the same
// person's first write into one shared store both stage that person's actor
// row; the later commit loses with store.ErrStale for its whole batch, and
// withActorRows runs it once more instead of failing the user's write.
// Run this one with -race.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/op"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// actorRaceRounds is how many fresh shared stores the test races on.
const actorRaceRounds = 3

// actorRaceWait bounds the barrier: past it a commit goes ahead alone, so a
// change that stops staging the actor row cannot hang the test.
const actorRaceWait = 5 * time.Second

// commitBarrier holds each App's first commit that carries an actor row
// until both Apps reached theirs: both have then read "no actor row yet" and
// staged one, which is the interleaving two processes hit for real (side
// effect 32). Left to the scheduler, one App usually finishes first and the
// losing-commit path never runs.
type commitBarrier struct {
	mu      sync.Mutex
	arrived int
	all     chan struct{}
	parties int
}

func newCommitBarrier(parties int) *commitBarrier {
	return &commitBarrier{all: make(chan struct{}), parties: parties}
}

func (c *commitBarrier) wait() {
	c.mu.Lock()
	c.arrived++
	if c.arrived == c.parties {
		close(c.all)
	}
	c.mu.Unlock()
	select {
	case <-c.all:
	case <-time.After(actorRaceWait):
	}
}

// barrierBackend gates the first actor-row commit of one App on barrier.
type barrierBackend struct {
	store.Backend
	barrier *commitBarrier
	once    sync.Once
}

func (b *barrierBackend) Write(ctx context.Context, changes []store.Change) error {
	for _, ch := range changes {
		if string(ch.Row.Table) == schema.TableActors {
			b.once.Do(b.barrier.wait)
			break
		}
	}
	return b.Backend.Write(ctx, changes)
}

// Unwrap implements Unwrapper (a WrapBackend wrapper must name what it wraps).
func (b *barrierBackend) Unwrap() store.Backend { return b.Backend }

// TestConcurrentFirstWriteOneActorRow: two Apps (two clones of the same
// person, each with its own repo, cache and engine) attach one folder
// store and make their first write into it at the same moment. Both writes
// succeed, both memories are in the store, and the store holds exactly one
// actor row for that person. A barrier on the store makes both Apps stage
// the actor row before either commits, so one commit always loses.
func TestConcurrentFirstWriteOneActorRow(t *testing.T) {
	t.Parallel()
	who, err := identity.ActorID(testEmail)
	if err != nil {
		t.Fatal(err)
	}
	for round := range actorRaceRounds {
		kb := folderStoreAt(t, fmt.Sprintf("sto_0e45506e64357c94a87e70ef1c6b%04x", 0x100+round), "internal")
		apps := make([]*App, 2)
		barrier := newCommitBarrier(len(apps))
		internal := Options{
			RepoIsClient: func(context.Context) (bool, error) { return false, nil },
			WrapBackend: func(_ context.Context, name store.Source, _ string, b store.Backend) store.Backend {
				if name != "kb" {
					return b
				}
				return &barrierBackend{Backend: b, barrier: barrier}
			},
		}
		for i := range apps {
			repo := gitRepo(t)
			writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+kb+"\n")
			apps[i] = openWith(t, repo, internal)
			withProject(t, apps[i])
		}
		entry, ok := apps[0].Registry().Lookup(opsmemory.OpAdd)
		if !ok {
			t.Fatalf("no op %s", opsmemory.OpAdd)
		}
		start := make(chan struct{})
		errsOut := make([]error, len(apps))
		var wg sync.WaitGroup
		for i, a := range apps {
			wg.Add(1)
			go func() {
				defer wg.Done()
				body := fmt.Sprintf(`{"body":"round %d clone %d","to":"kb"}`, round, i)
				<-start
				_, errsOut[i] = a.Run(background(), entry, op.JSONDecoder{Body: []byte(body)}, op.Caller{Yes: true})
			}()
		}
		close(start)
		wg.Wait()
		for i, werr := range errsOut {
			if werr != nil {
				t.Fatalf("round %d: clone %d's first write failed: %v", round, i, werr)
			}
		}
		dir := filepath.FromSlash(strings.TrimPrefix(kb, "file://"))
		actors, rerr := os.ReadDir(filepath.Join(dir, ".lore", "data", schema.TableActors))
		if rerr != nil || len(actors) != 1 || actors[0].Name() != who.String()+".json" {
			t.Fatalf("round %d: actor rows = %v, %v; want exactly %s.json", round, actors, rerr, who)
		}
		mems, merr := os.ReadDir(filepath.Join(dir, ".lore", "data", schema.TableMemories))
		if merr != nil || len(mems) != len(apps) {
			t.Fatalf("round %d: memories = %v, %v; want %d", round, mems, merr, len(apps))
		}
	}
}
