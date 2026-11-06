package wire

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/store"
)

// countingBackend counts the writes that pass through it.
type countingBackend struct {
	store.Backend
	writes *atomic.Int64
}

func (c countingBackend) Write(ctx context.Context, cs []store.Change) error {
	c.writes.Add(int64(len(cs)))
	return c.Backend.Write(ctx, cs)
}

// unwrappingBackend is countingBackend with Unwrap.
type unwrappingBackend struct{ countingBackend }

func (u unwrappingBackend) Unwrap() store.Backend { return u.Backend }

// TestWrapBackend: every store's writes go through the wrapper; the
// natural-id namespace still comes from the concrete backend's marker
// (a wrapped store writes the same tag id as an unwrapped one); a wrapper
// without Unwrap, or a nil one, is refused at open (fail closed: hiding
// the marker would silently change row ids); a nil hook changes nothing.
func TestWrapBackend(t *testing.T) {
	t.Parallel()
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	yaml := "stores:\n  - name: agency\n    url: " + url + "\n"
	var writes atomic.Int64
	seen := map[store.Source]string{}
	wrap := func(_ context.Context, name store.Source, folder string, b store.Backend) store.Backend {
		seen[name] = folder
		return unwrappingBackend{countingBackend{Backend: b, writes: &writes}}
	}
	repo := gitRepo(t)
	writeStoresYAML(t, repo, yaml)
	a := openWith(t, repo, Options{WrapBackend: wrap, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, a)
	if _, err := run(t, a, opsmemory.OpAdd, `{"body":"through the wrapper","to":"agency"}`, false); err != nil {
		t.Fatal(err)
	}
	if writes.Load() < 2 || len(seen) != 2 {
		t.Fatalf("writes %d, wrapped %v", writes.Load(), seen)
	}
	st, err := a.state(background())
	if err != nil {
		t.Fatal(err)
	}
	if _, isWrapped := st.byName["agency"].(unwrappingBackend); !isWrapped {
		t.Fatal("the engine's agency backend is not the wrapper")
	}
	att := engine.Attached{Name: "agency", Backend: st.byName["agency"]}
	if ns := keyNamespace(att); ns != folderStore {
		t.Fatalf("namespace through the wrapper = %q, want the marker's %s", ns, folderStore)
	}

	for name, bad := range map[string]func(context.Context, store.Source, string, store.Backend) store.Backend{
		"no Unwrap": func(_ context.Context, _ store.Source, _ string, b store.Backend) store.Backend {
			return countingBackend{Backend: b, writes: &writes}
		},
		"nil": func(context.Context, store.Source, string, store.Backend) store.Backend { return nil },
	} {
		r := gitRepo(t)
		b := openWith(t, r, Options{WrapBackend: bad})
		if _, err := b.Engine(background()); !errs.Has(err, errs.ConfigInvalid) {
			t.Errorf("%s wrapper: %v, want E_CONFIG_INVALID", name, err)
		}
	}
	same := openWith(t, gitRepo(t), Options{WrapBackend: func(_ context.Context, _ store.Source, _ string, b store.Backend) store.Backend { return b }})
	if _, err := same.Engine(background()); err != nil {
		t.Fatalf("identity wrapper: %v", err)
	}
}
