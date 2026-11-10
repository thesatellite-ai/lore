package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// storesYAMLPath is the committed stores file of a repo.
func storesYAMLPath(repo string) string {
	return filepath.Join(repo, ".lore", "stores.yaml")
}

// TestCFG7ConfirmMatrix: store init/add in a client repo writes the store
// url into the committed stores.yaml the client reads, so it answers
// E_NEEDS_CONFIRM without --yes and writes nothing. Matrix: op (init, add)
// × RepoIsClient (nil = no org concept, true, false, error = fail closed)
// × yes; plus `me` (never written into stores.yaml) and an internal repo,
// which never ask.
func TestCFG7ConfirmMatrix(t *testing.T) {
	t.Parallel()
	client := func(context.Context) (bool, error) { return true, nil }
	internal := func(context.Context) (bool, error) { return false, nil }
	failing := func(context.Context) (bool, error) { return false, errors.New("hook down") }
	hooks := []struct {
		name string
		hook func(context.Context) (bool, error)
		asks bool
	}{
		{"no hook", nil, false},
		{"client", client, true},
		{"internal", internal, false},
		{"failing hook is client", failing, true},
	}
	for _, opName := range []string{opsstores.OpInit, opsstores.OpAdd} {
		for _, h := range hooks {
			for _, yes := range []bool{false, true} {
				name := opName + "/" + h.name
				if yes {
					name += "/yes"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					repo := gitRepo(t)
					storeDir := t.TempDir()
					if opName == opsstores.OpAdd {
						// add needs an existing store: make it in another repo
						// with no hook (nothing asked there).
						mk := openWith(t, gitRepo(t), Options{})
						if _, err := run(t, mk, opsstores.OpInit, `{"name":"agency","url":"`+fileURL(storeDir)+`"}`, false); err != nil {
							t.Fatalf("make the store: %v", err)
						}
					}
					a := openWith(t, repo, Options{RepoIsClient: h.hook})
					_, err := run(t, a, opName, `{"name":"agency","url":"`+fileURL(storeDir)+`"}`, yes)
					wantAsk := h.asks && !yes
					if wantAsk {
						if !errs.Has(err, errs.NeedsConfirm) {
							t.Fatalf("want E_NEEDS_CONFIRM, got %v", err)
						}
						var e *errs.Error
						if !errors.As(err, &e) || e.Question == "" || !strings.Contains(e.Hint, "--yes") {
							t.Fatalf("the refusal must ask a question and name --yes: %+v", e)
						}
						if _, serr := os.Stat(storesYAMLPath(repo)); !errors.Is(serr, os.ErrNotExist) {
							t.Fatalf("a refused %s wrote stores.yaml (%v)", opName, serr)
						}
						return
					}
					if err != nil {
						t.Fatalf("want success, got %v", err)
					}
					b, rerr := os.ReadFile(storesYAMLPath(repo))
					if rerr != nil || !strings.Contains(string(b), "agency") {
						t.Fatalf("stores.yaml has no agency entry: %q %v", b, rerr)
					}
				})
			}
		}
	}
}

// TestCFG7NeverAsksForMe: `lore store init me` never writes stores.yaml
// (the personal store lives in ~/.lore/config.yaml), so even a client repo
// with a failing hook does not ask.
func TestCFG7NeverAsksForMe(t *testing.T) {
	t.Parallel()
	for _, hook := range []func(context.Context) (bool, error){
		func(context.Context) (bool, error) { return true, nil },
		func(context.Context) (bool, error) { return false, errors.New("hook down") },
	} {
		repo := gitRepo(t)
		a := openWith(t, repo, Options{RepoIsClient: hook})
		if _, err := run(t, a, opsstores.OpInit, `{"name":"me","url":"`+fileURL(t.TempDir())+`"}`, false); err != nil {
			t.Fatalf("store init me in a client repo: %v", err)
		}
		if _, err := os.Stat(storesYAMLPath(repo)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("store init me wrote stores.yaml (%v)", err)
		}
	}
}

// TestCFG7OrgStoreURLAsks (INTEG-CFG7-1): in an internal repo (not a
// client), store init/add/set-url of a url that is an org store's still
// asks, because stores.yaml is committed and everyone who can read the
// repo sees the url: E_NEEDS_CONFIRM saying so and naming --yes, nothing
// written; --yes writes. The org store is known from the repo's org
// (OrgStores, any spelling of the url) or from any configured org
// (OrgSigners). A plain store url (no org has it) never asks, as before.
// set-url runs the same check (opsstores.WritesStoresFile).
func TestCFG7OrgStoreURLAsks(t *testing.T) {
	t.Parallel()
	internal := func(context.Context) (bool, error) { return false, nil }
	orgDir := t.TempDir()
	mk := openWith(t, gitRepo(t), Options{})
	if _, err := run(t, mk, opsstores.OpInit, `{"name":"agency","url":"`+fileURL(orgDir)+`"}`, false); err != nil {
		t.Fatalf("make the org store: %v", err)
	}
	plainDir := t.TempDir()
	if _, err := run(t, mk, opsstores.OpInit, `{"name":"golang","url":"`+fileURL(plainDir)+`"}`, false); err != nil {
		t.Fatalf("make the plain store: %v", err)
	}
	viaOrgStores := Options{RepoIsClient: internal, OrgStores: func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "agency", URL: fileURL(orgDir), Folder: "acme"}}, nil
	}}
	viaSigners := Options{RepoIsClient: internal, OrgSigners: func(context.Context) ([]OrgSigner, error) {
		return []OrgSigner{{StoreURLs: []string{fileURL(orgDir)}, Name: "Agency Bot"}}, nil
	}}
	// an org configured without an identity yet: only OrgStoreURLs knows
	// it, spelled with a trailing slash
	viaConfigured := Options{RepoIsClient: internal, OrgStoreURLs: func(context.Context) ([]OrgStoreURL, error) {
		return []OrgStoreURL{{Org: "northwind", URL: fileURL(orgDir) + "/"}}, nil
	}}
	for _, c := range []struct {
		name    string
		opts    Options
		url     string
		asks    bool
		orgName string
	}{
		{"org store via the repo's org", viaOrgStores, fileURL(orgDir), true, "agency"},
		{"org store via a configured org", viaSigners, fileURL(orgDir), true, ""},
		{"org store via an org without identity", viaConfigured, fileURL(orgDir), true, "northwind"},
		{"org store spelled with a trailing slash", viaOrgStores, fileURL(orgDir) + "/", true, "agency"},
		{"plain store, org without identity configured", viaConfigured, fileURL(plainDir), false, ""},
		{"plain store, org configured", viaOrgStores, fileURL(plainDir), false, ""},
		{"plain store, no org hooks", Options{}, fileURL(plainDir), false, ""},
	} {
		for _, yes := range []bool{false, true} {
			name := c.name
			if yes {
				name += "/yes"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				repo := gitRepo(t)
				a := openWith(t, repo, c.opts)
				_, err := run(t, a, opsstores.OpAdd, `{"name":"tips","url":"`+c.url+`"}`, yes)
				if c.asks && !yes {
					var e *errs.Error
					if !errors.As(err, &e) || e.Code != errs.NeedsConfirm {
						t.Fatalf("want E_NEEDS_CONFIRM, got %v", err)
					}
					if !strings.Contains(e.Message, "visible to everyone who can read this repo") || !strings.Contains(e.Hint, "--yes") || e.Question == "" {
						t.Fatalf("the refusal must say the url is committed and visible, ask, and name --yes: %+v", e)
					}
					if c.orgName != "" && !strings.Contains(e.Message, "org "+c.orgName) {
						t.Fatalf("the refusal must name org %s: %q", c.orgName, e.Message)
					}
					if _, serr := os.Stat(storesYAMLPath(repo)); !errors.Is(serr, os.ErrNotExist) {
						t.Fatalf("a refused store add wrote stores.yaml (%v)", serr)
					}
					return
				}
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
				b, rerr := os.ReadFile(storesYAMLPath(repo))
				if rerr != nil || !strings.Contains(string(b), "tips") {
					t.Fatalf("stores.yaml has no tips entry: %q %v", b, rerr)
				}
			})
		}
	}
}

// TestCFG7SetURLAsks (INTEG-CFG7-1): `store set-url` of an attached store
// to an org store's url (here a store moved to where an org without an
// identity keeps it, url spelled with a trailing slash) asks like add:
// E_NEEDS_CONFIRM naming --yes, stores.yaml unchanged; --yes writes the
// new url. A set-url to a plain url never asks.
func TestCFG7SetURLAsks(t *testing.T) {
	t.Parallel()
	internal := func(context.Context) (bool, error) { return false, nil }
	for _, c := range []struct {
		name string
		org  bool
	}{{"org store", true}, {"plain store", false}} {
		for _, yes := range []bool{false, true} {
			name := c.name
			if yes {
				name += "/yes"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				oldDir := filepath.Join(t.TempDir(), "kit")
				newDir := filepath.Join(t.TempDir(), "moved")
				opts := Options{RepoIsClient: internal}
				if c.org {
					opts.OrgStoreURLs = func(context.Context) ([]OrgStoreURL, error) {
						return []OrgStoreURL{{Org: "northwind", URL: fileURL(newDir)}}, nil
					}
				}
				repo := gitRepo(t)
				a := openWith(t, repo, opts)
				if _, err := run(t, a, opsstores.OpInit, `{"name":"kit","url":"`+fileURL(oldDir)+`"}`, false); err != nil {
					t.Fatalf("make the store: %v", err)
				}
				if err := os.Rename(oldDir, newDir); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(storesYAMLPath(repo))
				if err != nil {
					t.Fatal(err)
				}
				_, err = run(t, a, opsstores.OpSetURL, `{"name":"kit","url":"`+fileURL(newDir)+`/"}`, yes)
				after, rerr := os.ReadFile(storesYAMLPath(repo))
				if rerr != nil {
					t.Fatal(rerr)
				}
				if c.org && !yes {
					var e *errs.Error
					if !errors.As(err, &e) || e.Code != errs.NeedsConfirm || !strings.Contains(e.Message, "org northwind") || !strings.Contains(e.Hint, "--yes") {
						t.Fatalf("want E_NEEDS_CONFIRM naming org northwind and --yes, got %v", err)
					}
					if string(after) != string(before) {
						t.Fatalf("a refused set-url changed stores.yaml:\n%s", after)
					}
					return
				}
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
				if !strings.Contains(string(after), "moved") {
					t.Fatalf("stores.yaml does not hold the new url:\n%s", after)
				}
			})
		}
	}
}

// TestStoreKeySpellings: storeKey is one key for every spelling of one
// remote (scp form, ssh://, https with or without .git or a trailing
// slash, host case, a folder or branch query) and for a file:// path with
// or without a trailing slash; another repository or path is another key.
func TestStoreKeySpellings(t *testing.T) {
	t.Parallel()
	same := [][]string{
		{"git@example.com:acme/notes.git", "ssh://git@example.com/acme/notes", "https://example.com/acme/notes",
			"https://example.com/acme/notes/", "https://Example.COM/acme/notes.git", "https://example.com/acme/notes?folder=tips&branch=staging"},
		{"file:///srv/stores/notes", "file:///srv/stores/notes/", "file:///srv/stores/notes?folder=tips"},
	}
	keys := map[string]bool{}
	for _, group := range same {
		want, ok := storeKey(group[0])
		if !ok {
			t.Fatalf("storeKey(%q) failed", group[0])
		}
		for _, u := range group[1:] {
			if got, ok := storeKey(u); !ok || got != want {
				t.Errorf("storeKey(%q) = %q, %v; want %q (same store as %q)", u, got, ok, want, group[0])
			}
		}
		keys[want] = true
	}
	for _, u := range []string{"https://example.com/acme/other", "https://example.org/acme/notes", "file:///srv/stores/other"} {
		if k, ok := storeKey(u); !ok || keys[k] {
			t.Errorf("storeKey(%q) = %q, %v; want a key of its own", u, k, ok)
		}
	}
	if _, ok := storeKey("not a url"); ok {
		t.Error("an unparsable url has no key")
	}
}

// TestOrgStoreURLsListsEveryOrg (INTEG-CFG7-1): OrgResolution.OrgStoreURLs
// lists the stores of every configured org, with or without an identity
// (OrgSigners lists only the ones with one).
func TestOrgStoreURLsListsEveryOrg(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfg := "orgs:\n  - name: northwind\n    stores:\n      - name: notes\n        url: https://example.com/northwind/notes\n" +
		"  - name: globex\n    identity:\n      key: human:person@example.com\n    stores:\n      - name: notes\n        url: https://example.com/globex/notes\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewOrgResolution(home, t.TempDir(), nil)
	got, err := r.OrgStoreURLs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []OrgStoreURL{{Org: "northwind", URL: "https://example.com/northwind/notes"}, {Org: "globex", URL: "https://example.com/globex/notes"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("OrgStoreURLs = %+v, want %+v", got, want)
	}
	signers, err := r.OrgSigners(context.Background())
	if err != nil || len(signers) != 1 {
		t.Fatalf("OrgSigners = %+v, %v; want only the org with an identity", signers, err)
	}
}
