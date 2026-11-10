package host

// The v0.1.17 gitsetup TestAdd (saas/pkg/aicoder/gitsetup gitsetup_test.go),
// ported before phase E onto the pre-commit hook (precommit.go): new and
// modified row files are staged, a DELETED row file is staged as a
// deletion (never an error), and nothing outside .lore/data is staged.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

func TestPreCommitStagesNewModifiedAndDeletedRows(t *testing.T) {
	f := newLiveFixture(t)
	f.s.release()
	top := f.s.top
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(top, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	staged := func() string {
		t.Helper()
		out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: top, Args: []string{"diff", "--cached", "--name-status"}})
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	const a, b, other = ".lore/data/memories/mem_1.json", ".lore/data/memories/mem_2.json", "other.txt"
	write(a, `{"a":1}`+"\n")
	write(b, `{"b":1}`+"\n")
	write(other, "x\n")
	t.Chdir(top)
	if err := f.s.h.preCommit(context.Background()); err != nil {
		t.Fatalf("pre-commit, new rows: %v", err)
	}
	if got, want := staged(), "A\t"+a+"\nA\t"+b; got != want {
		t.Fatalf("staged %q, want %q (other.txt never staged)", got, want)
	}
	gitT(t, top, "commit", "-q", "-m", "rows")
	if err := os.Remove(filepath.Join(top, filepath.FromSlash(a))); err != nil {
		t.Fatal(err)
	}
	write(b, `{"b":2}`+"\n")
	if err := f.s.h.preCommit(context.Background()); err != nil {
		t.Fatalf("pre-commit, a deleted row: %v", err)
	}
	if got, want := staged(), "D\t"+a+"\nM\t"+b; got != want {
		t.Fatalf("staged %q, want %q", got, want)
	}
}
