package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine/attach"
)

// TestRosterOfOrgJudgesTheCallerAsThatOrgsSelf pins devco D-694 (the
// write guard's roster of another org): a person who is in a second org
// under another address writes there from a repo of the first org. The
// guard reads the second org's roster (RosterOfOrg); its Caller was the
// command's first-org identity, which that roster does not hold, so an
// admin of the second org was refused as a non-admin and told to ask
// themselves. The Caller is now the identity the second org's entry names.
func TestRosterOfOrgJudgesTheCallerAsThatOrgsSelf(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1") // a person, whatever tool runs the test
	ctx := background()
	betaURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	home := t.TempDir()
	cfg := "orgs:\n" +
		"  - name: acme\n    identity:\n      key: human:lee@acme.example.test\n      name: Lee Lead\n" +
		"  - name: beta\n    identity:\n      key: human:lee@beta.example.test\n      name: Lee Lead\n"
	// beta's org folder store (the config refuses a file:// url; the
	// OrgStores hook attaches it as the full open does)
	stores := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "beta/org", URL: betaURL, Folder: "org"}}, nil
	}
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	orgHook := func(name, key string) func(context.Context) (*identity.Org, error) {
		return func(context.Context) (*identity.Org, error) {
			return &identity.Org{Name: name, Identity: identity.Entry{Key: key, Name: "Lee Lead"}}, nil
		}
	}
	callerOf := func(org func(context.Context) (*identity.Org, error)) string {
		t.Helper()
		a, err := New(Options{RepoDir: t.TempDir(), Home: home, Org: org, OrgStores: stores, Wall: time.Now})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = a.Close() }()
		r, done, ok, err := a.RosterOfOrg(ctx, "beta")
		if err != nil || !ok {
			t.Fatalf("roster of beta: ok %v err %v", ok, err)
		}
		defer done()
		if r.Caller().IsZero() {
			t.Fatal("roster of beta has no caller")
		}
		return r.Caller().String()
	}
	want := callerOf(orgHook("beta", "human:lee@beta.example.test")) // a command in beta's own repo
	got := callerOf(orgHook("acme", "human:lee@acme.example.test"))  // the guard's read from acme's repo
	if got != want {
		t.Fatalf("from acme's repo the roster of beta judged caller %s, want beta's own identity %s (the person's beta address)", got, want)
	}
}
