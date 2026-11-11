package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// orgYAML is a v0.1.17 lore-org.yaml with client routing defaults, other
// keys and a comment the migration must keep.
const orgYAML = `# the agency's org file
name: agency
defaults:
  client_repos:
    memory: repo
    decision: team
members:
  - ana
`

// routingRows counts the live routing.client_repos policy rows of the org
// folder and returns the first one's value text.
func routingRows(t *testing.T, a *App) (int, string) {
	t.Helper()
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := eng.List(background(), rosterStore, schema.TablePolicies)
	if err != nil {
		t.Fatal(err)
	}
	n, val := 0, ""
	for _, r := range rows {
		if k, ok := r.Fields[policyFieldKey].(value.Text); ok && !r.Deleted && k.String() == string(policy.KeyClientRoutes) {
			n++
			if val == "" {
				val = r.Fields[policyFieldValue].String()
			}
			if sc := r.Fields[policyFieldScope]; sc == nil || sc.String() != "org" {
				t.Fatalf("policy scope = %v, want the org folder", sc)
			}
		}
	}
	return n, val
}

// TestOrgDefaultsToPolicy (CFG7 step org_defaults_to_policy): the dry run
// reports the step and the one row it would write and changes nothing;
// the real run writes one routing.client_repos policy row in the org
// folder (items sorted kind=target), removes only `defaults` from
// lore-org.yaml (other keys stay) and keeps the original text as
// lore-org.yaml.migrated; a second run writes no second row and leaves
// the .migrated copy as the ORIGINAL text.
func TestOrgDefaultsToPolicy(t *testing.T) {
	t.Parallel()
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	dir := fileuri.LocalPath(orgURL)
	a := rosterApp(t, orgURL, false)
	st, err := a.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "org", rowfs.LoreDir, orgDefaultsFile)
	if err = os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte(orgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	s := attach.Store{Name: rosterStore, URL: orgURL, Folder: "org"}

	steps, n, err := a.orgDefaultsToPolicy(ctx, st, s, dir, true)
	if err != nil || n != 1 || len(steps) != 1 || !strings.Contains(steps[0], "decision=team,memory=repo") {
		t.Fatalf("dry run: %v %d %v", steps, n, err)
	}
	if got := string(mustRead(t, file)); got != orgYAML {
		t.Fatalf("the dry run changed the file:\n%s", got)
	}
	if c, _ := routingRows(t, a); c != 0 {
		t.Fatalf("the dry run wrote %d policy rows", c)
	}

	if _, n, err = a.orgDefaultsToPolicy(ctx, st, s, dir, false); err != nil || n != 1 {
		t.Fatalf("run: %d %v", n, err)
	}
	c, val := routingRows(t, a)
	if c != 1 || !strings.Contains(val, "decision=team") || !strings.Contains(val, "memory=repo") {
		t.Fatalf("policy rows = %d value %q", c, val)
	}
	rewritten := string(mustRead(t, file))
	if strings.Contains(rewritten, "defaults") || strings.Contains(rewritten, "client_repos") || !strings.Contains(rewritten, "name: agency") || !strings.Contains(rewritten, "- ana") {
		t.Fatalf("rewritten file:\n%s", rewritten)
	}
	if kept := string(mustRead(t, file+migratedSuffix)); kept != orgYAML {
		t.Fatalf(".migrated = %q, want the original text", kept)
	}

	steps, n, err = a.orgDefaultsToPolicy(ctx, st, s, dir, false)
	if err != nil || n != 0 || len(steps) != 0 {
		t.Fatalf("second run: %v %d %v, want nothing to do", steps, n, err)
	}
	if c, _ = routingRows(t, a); c != 1 {
		t.Fatalf("second run left %d policy rows", c)
	}
	if kept := string(mustRead(t, file+migratedSuffix)); kept != orgYAML {
		t.Fatal("the second run overwrote the .migrated copy")
	}
}

// TestOrgDefaultsToPolicyNothingToMove: no file, a file without
// client_repos, an empty client_repos, a folder escaping the clone and a
// store that is not attached each write nothing (the unattached store
// says so on a real run).
func TestOrgDefaultsToPolicyNothingToMove(t *testing.T) {
	t.Parallel()
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	dir := fileuri.LocalPath(orgURL)
	a := rosterApp(t, orgURL, false)
	st, err := a.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "org", rowfs.LoreDir, orgDefaultsFile)
	if err = os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		t.Fatal(err)
	}
	s := attach.Store{Name: rosterStore, URL: orgURL, Folder: "org"}
	for name, body := range map[string]string{
		"no file":            "",
		"no client_repos":    "name: agency\ndefaults:\n  other: 1\n",
		"empty client_repos": "defaults:\n  client_repos: {}\n",
		"not yaml":           "defaults: [unclosed\n",
	} {
		t.Run(name, func(t *testing.T) {
			_ = os.Remove(file) // test setup: start from no file
			if body != "" {
				if werr := os.WriteFile(file, []byte(body), 0o600); werr != nil {
					t.Fatal(werr)
				}
			}
			steps, n, rerr := a.orgDefaultsToPolicy(ctx, st, s, dir, false)
			if rerr != nil || n != 0 || len(steps) != 0 {
				t.Fatalf("%v %d %v, want nothing", steps, n, rerr)
			}
		})
	}
	if err = os.WriteFile(file, []byte(orgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	escape := attach.Store{Name: rosterStore, URL: orgURL, Folder: "../outside"}
	if steps, n, eerr := a.orgDefaultsToPolicy(ctx, st, escape, dir, false); eerr != nil || n != 0 || len(steps) != 0 {
		t.Fatalf("a folder outside the clone: %v %d %v", steps, n, eerr)
	}
	other := attach.Store{Name: "elsewhere", URL: orgURL, Folder: "org"}
	steps, n, err := a.orgDefaultsToPolicy(ctx, st, other, dir, false)
	if err != nil || n != 0 || len(steps) != 1 || !strings.Contains(steps[0], "not attached") {
		t.Fatalf("unattached store: %v %d %v", steps, n, err)
	}
	if got := string(mustRead(t, file)); got != orgYAML {
		t.Fatal("an unattached store's file was rewritten")
	}
	if c, _ := routingRows(t, a); c != 0 {
		t.Fatalf("%d policy rows written", c)
	}
}
