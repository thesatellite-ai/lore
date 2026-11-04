package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// unreachableURL is a git store url nothing listens on (connection refused
// at once), so a store with no local copy cannot be fetched.
const unreachableURL = "http://127.0.0.1:1/northwind.git"

// TestUnreachableOrgStore: an org store (Options.OrgStores) with no local
// copy that cannot be reached no longer fails the open. Reads work; each
// write fails closed with v0.1.17's code for its situation: no target is
// E_LINK_UNKNOWN, --to repo in a client repo (or an unknown one: hook
// error) is E_LEAK_BLOCKED, --to repo in an internal repo writes, --to the
// org store itself is its E_STORE_UNREACHABLE. A committed stores.yaml
// entry that cannot be reached still fails the open.
func TestUnreachableOrgStore(t *testing.T) {
	org := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "northwind", URL: unreachableURL, Folder: "acme"}}, nil
	}
	for _, c := range []struct {
		name     string
		isClient func(context.Context) (bool, error)
		repoCode errs.Code // "" = the --to repo write succeeds
	}{
		{"client", func(context.Context) (bool, error) { return true, nil }, errs.LeakBlocked},
		{"unknown", func(context.Context) (bool, error) { return false, errors.New("hook down") }, errs.LeakBlocked},
		{"internal", func(context.Context) (bool, error) { return false, nil }, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo := gitRepo(t)
			a := openWith(t, repo, Options{OrgStores: org, RepoIsClient: c.isClient})
			withProject(t, a)
			if _, err := run(t, a, opsmemory.OpList, `{}`, false); err != nil {
				t.Fatalf("a read must work offline: %v", err)
			}
			if _, err := run(t, a, opsmemory.OpAdd, `{"body":"where does this go"}`, false); !errs.Has(err, errs.LinkUnknown) {
				t.Fatalf("no target: want E_LINK_UNKNOWN, got %v", err)
			}
			_, err := run(t, a, opsmemory.OpAdd, `{"body":"for the repo","to":"repo"}`, false)
			if c.repoCode == "" {
				if err != nil {
					t.Fatalf("--to repo in an internal repo: %v", err)
				}
			} else if !errs.Has(err, c.repoCode) {
				t.Fatalf("--to repo: want %s, got %v", c.repoCode, err)
			}
			if _, err = run(t, a, opsmemory.OpAdd, `{"body":"x","to":"northwind"}`, false); !errs.Has(err, errs.StoreUnreachable) {
				t.Fatalf("--to northwind: want E_STORE_UNREACHABLE, got %v", err)
			}
		})
	}

	repo := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	yaml := "stores:\n  - name: northwind\n    url: " + unreachableURL + "\n"
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(attach.FileName)), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	a := openWith(t, repo, Options{})
	if _, err := a.Engine(background()); !errs.Has(err, errs.StoreUnreachable) {
		t.Fatalf("a committed unreachable store: want E_STORE_UNREACHABLE at open, got %v", err)
	}
}
