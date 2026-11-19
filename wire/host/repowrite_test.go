package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestRepoWriteRefusedLeavingTheWorkTree (D-502): the git wiring's guard
// refuses a hook or .gitattributes that leads out of the work tree (the
// file a link, or its folder a link) and lets everything else through: a
// file in the work tree, a link that stays in it, a hooks folder outside
// the work tree altogether (a hook manager's shared folder).
func TestRepoWriteRefusedLeavingTheWorkTree(t *testing.T) {
	top, outside := t.TempDir(), t.TempDir()
	mk := func(p string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ln := func(target, at string) {
		t.Helper()
		if err := os.Symlink(target, at); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join(outside, "hooks", "pre-push"))
	ln(filepath.Join(outside, "hooks"), filepath.Join(top, ".githooks"))
	ln(mk(filepath.Join(outside, "attrs")), filepath.Join(top, ".gitattributes"))
	mk(filepath.Join(top, "tools", "hooks", "commit-msg"))
	ln(filepath.Join("tools", "hooks"), filepath.Join(top, ".husky"))

	if err := hookRefused(top, filepath.Join(top, ".githooks"), hookPrePush); errs.CodeOf(err) != errs.Refused {
		t.Fatalf("hook in a folder linked out of the work tree: %v, want E_REFUSED", err)
	}
	if err := repoWriteRefused(top, filepath.Join(top, ".gitattributes")); errs.CodeOf(err) != errs.Refused {
		t.Fatalf(".gitattributes linked out of the work tree: %v, want E_REFUSED", err)
	}
	for _, ok := range []string{
		filepath.Join(top, ".husky", hookCommitMsg),      // a linked folder inside the work tree
		filepath.Join(top, ".git", "hooks", hookPrePush), // the clone's own hooks
		filepath.Join(outside, "hooks", hookPrePush),     // a shared hooks folder outside it
	} {
		if err := repoWriteRefused(top, ok); err != nil {
			t.Fatalf("%s: %v, want allowed", ok, err)
		}
	}
	if err := repoWriteRefused("", filepath.Join(top, ".gitattributes")); err != nil {
		t.Fatalf("no work tree known: %v, want nil", err)
	}
}
