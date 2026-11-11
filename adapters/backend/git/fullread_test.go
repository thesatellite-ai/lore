package git

// The full-read cache (Backend.full): a clone-mode Read with no commit
// cursor at an unchanged HEAD answers without listing or reading the tree
// again, a commit makes the next one read again, and every answer is the
// caller's own copy.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/tests/fakeexec"
)

// countGit points b at a git wrapper that appends each subcommand line to
// a log, and returns a func that reports how many lines contain sub.
func countGit(t *testing.T, b *Backend) func(sub string) int {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	wrapper := filepath.Join(dir, "git")
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\nexec '" + b.git.bin + "' \"$@\"\n"
	b.git.bin = fakeexec.Write(t, wrapper, script, 0o700) // git.exe on Windows
	return func(sub string) int {
		raw, err := os.ReadFile(log)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(raw), sub)
	}
}

func TestFullReadAtOneCommitListsTheTreeOnce(t *testing.T) {
	ctx := context.Background()
	b := clone(t, newRemote(t, true))
	g := newGen(t)
	first := g.row(newID(t), "first", hlc.HLC{})
	write(t, b, store.Change{Row: first})
	calls := countGit(t, b)

	rows, cur, err := b.Read(ctx, "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("first read: %v %v", rows, err)
	}
	rows[0].Doc[0] = 'X' // the caller's copy: the cache must not share it
	again, cur2, err := b.Read(ctx, "")
	if err != nil || len(again) != 1 || cur2 != cur {
		t.Fatalf("second read: %v %v %v", again, cur2, err)
	}
	if again[0].Doc[0] == 'X' {
		t.Fatal("the cached read handed out the buffer a caller changed")
	}
	again[0].Doc[0] = 'Y' // a cached answer is a copy too
	third, _, err := b.Read(ctx, "")
	if err != nil || len(third) != 1 || third[0].Doc[0] == 'Y' {
		t.Fatalf("a cached answer shares its buffer with the cache: %v %v", third, err)
	}
	if n := calls("ls-tree"); n != 1 {
		t.Fatalf("two full reads at one commit listed the tree %d times, want 1", n)
	}

	second := g.row(newID(t), "second", hlc.HLC{})
	write(t, b, store.Change{Row: second}) // a commit: HEAD moves
	after, cur3, err := b.Read(ctx, "")
	if err != nil || len(after) != 2 || cur3 == cur {
		t.Fatalf("read after a commit: %d rows at %s (was %s): %v", len(after), cur3, cur, err)
	}
	if n := calls("ls-tree"); n != 2 {
		t.Fatalf("the read after a commit did not list the tree again (%d lists)", n)
	}
}
