package wire

import (
	"strings"
	"testing"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/store"
)

// B-3: `lore whoami` (and every command's CallerAgent, `lore session list`,
// `lore setup list`) asks only who the caller is and lists rows straight
// from their files, so the open behind it must not build the repo's cache:
// the first whoami in a 5,000-row repo took 5 s doing that. The open is
// rowState: no refresh, no format read. The next full state call on the
// same App runs both before any cache read, so a query still sees every
// row.
func TestRowStateBuildsNoCacheUntilStateNeedsIt(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	writer := newTestApp(t, repo)
	withProject(t, writer)
	if _, err := run(t, writer, opsmemory.OpAdd, `{"text":"retry the gateway twice"}`, false); err != nil {
		t.Fatal(err)
	}

	a := newTestAppHome(t, repo, t.TempDir()) // a machine that never read this repo: no cache yet
	ctx := background()
	if _, err := a.Who(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sessions(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := a.IdentityContactLine(ctx); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	st := a.st
	a.mu.Unlock()
	if st == nil || !st.refreshPending {
		t.Fatal("Who/Sessions/IdentityContactLine opened the App with its cache refresh done; want it left to the first state call")
	}
	if cur, err := st.caches.Cursor(ctx, store.SourceRepo); err != nil || cur != "" {
		t.Fatalf("repo cache cursor after the row-only reads = %q, %v; want empty (nothing built)", cur, err)
	}

	out, err := run(t, a, opsmemory.OpList, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "retry the gateway twice") {
		t.Fatalf("memory list after the row-only open = %s; want the memory (the deferred refresh must run first)", out)
	}
	if st.refreshPending {
		t.Fatal("refreshPending still set after a full state call")
	}
	if cur, err := st.caches.Cursor(ctx, store.SourceRepo); err != nil || cur == "" {
		t.Fatalf("repo cache cursor after the full state call = %q, %v; want the refreshed cursor", cur, err)
	}
}
