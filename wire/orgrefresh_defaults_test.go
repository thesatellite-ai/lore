package wire

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestOrgRefreshCopiesDefaults (owner report, 0.1.18-dev): `lore org
// refresh` copied stores, own and repos from lore-org.yaml into the org
// entry of ~/.lore/config.yaml but not defaults, so `lore config show`
// kept the team's old defaults.client_repos. defaults now behaves as the
// other copied fields: added, replaced when the file changes them, and
// removed when the file drops them.
func TestOrgRefreshCopiesDefaults(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	h := orgHostAt(t, w.repo, mira)
	if _, err := h.Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	w.push(t, mira, orgTestStoreURL)
	clone := filepath.Join(w.root, "notes-edit")
	orgGit(t, w.root, "clone", "-q", orgTestStoreURL, clone)
	orgFile := filepath.Join(clone, "org", "lore-org.yaml")
	base, err := os.ReadFile(orgFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(base), "defaults:") {
		t.Fatalf("lore-org.yaml of a new org already has defaults:\n%s", base)
	}
	// publish writes lore-org.yaml with defaults (none when extra is "")
	// and pushes it, as a teammate changing the team's file would.
	publish := func(extra, msg string) {
		t.Helper()
		if err := os.WriteFile(orgFile, append(append([]byte{}, base...), extra...), 0o600); err != nil {
			t.Fatal(err)
		}
		orgGit(t, clone, "commit", "-q", "-am", msg)
		orgGit(t, clone, "push", "-q", "origin", "main")
	}
	entryDefaults := func() map[string]string {
		t.Helper()
		cfg, _, err := orgcfg.LoadConfig(mira)
		if err != nil {
			t.Fatal(err)
		}
		o := cfg.FindOrg("agency")
		if o == nil {
			t.Fatal("no org entry agency")
		}
		return o.Defaults.ClientRepos
	}
	refresh := func(step string) {
		t.Helper()
		rep, err := h.Refresh(ctx, opsorgcmd.RefreshIn{Org: "agency"})
		if err != nil {
			t.Fatalf("%s: refresh: %v", step, err)
		}
		if !strings.Contains(strings.Join(rep.Lines, "\n"), "✓ org entry agency now copies") {
			t.Fatalf("%s: refresh wrote nothing: %v", step, rep.Lines)
		}
	}

	publish("defaults:\n  client_repos:\n    task: repo\n", "add defaults")
	refresh("added")
	if got, want := entryDefaults(), map[string]string{"task": "repo"}; !maps.Equal(got, want) {
		t.Fatalf("added: entry defaults %v, want %v\n%s", got, want, readConfig(t, mira))
	}

	publish("defaults:\n  client_repos:\n    task: agency\n    memory: repo\n", "change defaults")
	refresh("changed")
	if got, want := entryDefaults(), map[string]string{"task": "agency", "memory": "repo"}; !maps.Equal(got, want) {
		t.Fatalf("changed: entry defaults %v, want %v\n%s", got, want, readConfig(t, mira))
	}

	publish("", "drop defaults")
	refresh("removed")
	if got := entryDefaults(); len(got) != 0 {
		t.Fatalf("removed: entry defaults %v, want none\n%s", got, readConfig(t, mira))
	}
	if cfg := readConfig(t, mira); strings.Contains(cfg, "defaults:") {
		t.Fatalf("removed: config.yaml still has a defaults key:\n%s", cfg)
	}
}
