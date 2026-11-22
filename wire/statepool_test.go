package wire

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/store"
)

// TestStatePoolKeepsAnOpenOnlyWhileNothingChanged (devco:D-890): the read
// server hands a kept store-only open to the next call only while every
// file it was built from is unchanged and it is under 30 s old. A ref that
// moved in a store clone (a sync's fast-forward), a commit to a folder
// cache, a changed config.yaml or repo .lore entry, or age each make the
// next call open afresh (the refresh from git runs again); an open that
// wrote is never kept, and with reuse off nothing is. Not parallel: the
// pool is process-wide.
func TestStatePoolKeepsAnOpenOnlyWhileNothingChanged(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".lore")
	repo := t.TempDir()
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ref := filepath.Join(home, "stores", "_local", "me", ".git", "refs", "heads", "main")
	cache := filepath.Join(home, "cache", "me", "notes.db")
	write(filepath.Join(home, "config.yaml"), "personal:\n  store: x\n")
	write(filepath.Join(home, "stores", "_local", "me", ".git", "HEAD"), "ref: refs/heads/main\n")
	write(ref, "1111111111111111111111111111111111111111\n")
	write(cache, "cache v1")
	write(filepath.Join(repo, ".lore", "config.yaml"), "a: 1\n")

	if putState(&state{poolKey: "k", openedAt: time.Now()}, home, repo) {
		t.Fatal("a state was kept while reuse is off (a one-shot process)")
	}
	stop := EnableStateReuse()
	defer stop()
	keep := func() *state {
		t.Helper()
		st := &state{poolKey: "k", openedAt: time.Now()}
		if !putState(st, home, repo) {
			t.Fatal("an open that wrote nothing was not kept")
		}
		return st
	}
	st := keep()
	if got := takeState("k", home, repo); got != st {
		t.Fatal("an unchanged lore home did not hand back the kept open")
	}
	for _, c := range []struct {
		what   string
		change func()
	}{
		{"a ref moved in a store clone (a sync's fast-forward)", func() { write(ref, "2222222222222222222222222222222222222222\n") }},
		{"a folder cache was committed to", func() { write(cache, "cache v2, longer") }},
		{"config.yaml changed", func() { write(filepath.Join(home, "config.yaml"), "personal:\n  store: y2\n") }},
		{"the repo's .lore changed", func() { write(filepath.Join(repo, ".lore", "config.yaml"), "a: 22\n") }},
	} {
		keep()
		c.change()
		if got := takeState("k", home, repo); got != nil {
			t.Errorf("%s: the kept open was handed out; the next call must open afresh", c.what)
		}
	}
	old := &state{poolKey: "k", openedAt: time.Now().Add(-stateMaxAge - time.Second)}
	if !putState(old, home, repo) {
		t.Fatal("an old open was not kept")
	}
	if got := takeState("k", home, repo); got != nil {
		t.Errorf("an open older than %s was handed out (it would delay a fetch_ttl fetch)", stateMaxAge)
	}
	if putState(&state{poolKey: "k", openedAt: time.Now(), wroteTo: map[store.Source]bool{"me/notes": true}}, home, repo) {
		t.Error("an open that wrote was kept")
	}
	if putState(&state{openedAt: time.Now()}, home, repo) {
		t.Error("an open with no key (not a store-only open) was kept")
	}
}
