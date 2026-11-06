package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
