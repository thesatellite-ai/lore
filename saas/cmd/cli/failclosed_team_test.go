package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/stores"
)

// TestIsNoTeamCoordination pins which coordination failures may fall back
// to lore.db (fixed rule 6): only the three documented "no team here"
// refusals, wrapped or not; every other failure (store unreachable, link
// status unknown, offline, a plain error) stops the command.
func TestIsNoTeamCoordination(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"not a git repo", errcodes.New(errcodes.Unsupported, "coordination needs a git repository"), true},
		{"no live rows", errRowsNotWired(), true},
		{"no roster actor", errcodes.New(errcodes.IdentityFailed, "coordination needs an agent actor"), true},
		{"wrapped no live rows", fmt.Errorf("open: %w", errRowsNotWired()), true},
		{"link unknown", errcodes.New(errcodes.LinkUnknown, "cannot tell"), false},
		{"offline", errcodes.New(errcodes.Offline, "store unreachable"), false},
		{"config invalid", errcodes.New(errcodes.ConfigInvalid, "bad config"), false},
		{"not a store", errcodes.New(errcodes.NotAStore, "x"), false},
		{"internal", errcodes.New(errcodes.Internal, "open lore.db"), false},
		{"plain error", errors.New("disk on fire"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNoTeamCoordination(tc.err); got != tc.want {
				t.Fatalf("isNoTeamCoordination(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestPlainRowFallbackRefusesOtherErrors: an error that is not a no-team
// refusal is returned as is, before any situation is read (so nothing can
// be written through lore.db).
func TestPlainRowFallbackRefusesOtherErrors(t *testing.T) {
	for _, in := range []error{
		errcodes.New(errcodes.LinkUnknown, "cannot tell"),
		errcodes.New(errcodes.Offline, "unreachable"),
		errors.New("store clone corrupt"),
	} {
		err := plainRowFallback(t.Context(), in)
		if err == nil {
			t.Fatalf("%v: fallback allowed; it must be refused", in)
		}
		if !strings.Contains(err.Error(), in.Error()) {
			t.Fatalf("%v: refusal must carry the cause, got %v", in, err)
		}
	}
}

// TestBadRowsError: undecodable live rows are an error naming every path
// (sorted), never "no such row".
func TestBadRowsError(t *testing.T) {
	if err := badRowsError(nil); err != nil {
		t.Fatalf("no bad rows: %v", err)
	}
	if err := badRowsError(map[string]error{}); err != nil {
		t.Fatalf("empty bad rows: %v", err)
	}
	cause := errors.New("unexpected end of JSON")
	err := badRowsError(map[string]error{"acme/tasks/tsk_2.json": errors.New("x"), "acme/tasks/tsk_1.json": cause})
	if err == nil {
		t.Fatal("bad rows must be an error")
	}
	if !strings.Contains(err.Error(), "acme/tasks/tsk_1.json, acme/tasks/tsk_2.json") {
		t.Fatalf("error must name every bad path, sorted: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("error must wrap the first cause: %v", err)
	}
}

// TestRemoveStoreWorktreesFailsOnBadURL: a URL lore cannot place is an
// error, never "removed" (store forget would otherwise print ✓ forgot).
func TestRemoveStoreWorktreesFailsOnBadURL(t *testing.T) {
	m := &stores.Manager{Layout: stores.Layout{Home: t.TempDir()}}
	if err := removeStoreWorktrees(m, "https://example.com/../x"); err == nil {
		t.Fatal("a bad store URL must fail")
	}
}

// TestRemoveStoreWorktreesRemoves: the happy path still removes the store's
// worktrees folder.
func TestRemoveStoreWorktreesRemoves(t *testing.T) {
	home := t.TempDir()
	m := &stores.Manager{Layout: stores.Layout{Home: home}}
	const url = "https://example.com/agency/notes"
	dir, err := m.Layout.WorktreeDir(url, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeStoreWorktrees(m, url); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(dir)); !os.IsNotExist(err) {
		t.Fatalf("worktrees must be gone: %v", err)
	}
}
