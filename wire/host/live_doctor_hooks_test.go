package host

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/wire"
)

// hookLines are the doctor lines about the ride-along hook blocks.
func hookLines(lines []string) []string {
	return slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return !strings.Contains(l, "hook block") })
}

// TestLiveDoctorFixInstallsRideHooks (owner item 37): a repo missing the
// post-commit and pre-push blocks is a warning that a read leaves as it is;
// the fixing run (lore fix, doctor --fix) installs both and says so; the
// re-check then has no warning about them, only the note that they are in.
func TestLiveDoctorFixInstallsRideHooks(t *testing.T) {
	f := newLiveFixture(t)
	dir, err := f.s.hooksDir(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{hookPostCommit, hookPrePush} {
		if err := os.Remove(filepath.Join(dir, h)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	read, err := liveDoctor{h: f.s.h}.Base(f.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if w := hookLines(read.Warnings); len(w) != 1 || !strings.Contains(w[0], "run `lore fix`") {
		t.Fatalf("a read: warnings %q, want the missing-block warning pointing at lore fix", read.Warnings)
	}
	if got := missingRideHooks(dir); len(got) != 2 {
		t.Fatalf("a read installed hook blocks: still missing %v", got)
	}
	fixing, err := liveDoctor{h: f.s.h}.Base(f.ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if w := hookLines(fixing.Warnings); len(w) != 1 || !strings.Contains(w[0], "installed now in "+dir) {
		t.Fatalf("the fixing run: warnings %q, want one line saying it installed them", fixing.Warnings)
	}
	if got := missingRideHooks(dir); len(got) != 0 {
		t.Fatalf("after the fixing run still missing %v", got)
	}
	after, err := liveDoctor{h: f.s.h}.Base(f.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if w := hookLines(after.Warnings); len(w) != 0 {
		t.Fatalf("the re-check still warns: %q", w)
	}
	if n := hookLines(after.Notes); len(n) != 1 || !strings.Contains(n[0], "installed") {
		t.Fatalf("the re-check notes %q, want the installed note", after.Notes)
	}
}

// TestLiveDoctorClientHooksInWorkTree: in a client repo whose
// core.hooksPath is a folder of its work tree, the fix installs nothing
// and the warning says why, never pointing at lore fix.
func TestLiveDoctorClientHooksInWorkTree(t *testing.T) {
	f := newLiveFixture(t)
	for _, kv := range [][2]string{{wire.ClientRepoGitKey, "true"}, {"core.hooksPath", ".githooks"}} {
		if _, err := f.s.repo.Run(f.ctx, gitref.Invocation{Args: []string{"config", "--local", kv[0], kv[1]}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, fix := range []bool{false, true} {
		base, err := liveDoctor{h: f.s.h}.Base(f.ctx, fix)
		if err != nil {
			t.Fatal(err)
		}
		w := hookLines(base.Warnings)
		if len(w) != 1 || strings.Contains(w[0], "lore fix") || !strings.Contains(w[0], "client repo") {
			t.Fatalf("fix=%v: warnings %q, want one naming the client repo and not lore fix", fix, base.Warnings)
		}
	}
	if _, err := os.Stat(filepath.Join(f.s.top, ".githooks", hookPostCommit)); !os.IsNotExist(err) {
		t.Fatalf("a hook was written into the client's work tree: %v", err)
	}
}
