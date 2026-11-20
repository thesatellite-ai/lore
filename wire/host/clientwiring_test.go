package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

// clientRepoWithOldWiring writes into the fixture's repo what an older lore
// left in a repo it set up as an own project, before an org named it a
// client repo: row files, a .gitattributes block missing lines a newer lore
// adds, and a .gitignore whose rule hides a row file. All of it committed.
func clientRepoWithOldWiring(t *testing.T, f ltFixture) (attrs, gi []byte) {
	t.Helper()
	top := f.s.top
	if err := os.WriteFile(filepath.Join(top, ".lore", "data", "_meta.json"), []byte(`{"format": 2}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(top, ".lore", "data", "memories"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(top, ".lore", "data", "memories", "m1.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	attrs = []byte("# >>> lore-sync >>>\n" + strings.Join(gitAttributeLines[:2], "\n") + "\n# <<< lore-sync <<<\n")
	gi = []byte("node_modules\n.lore/data/memories/\n")
	if err := os.WriteFile(filepath.Join(top, gitAttributesFile), attrs, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(top, gitignoreName), gi, 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, top, "add", "-A")
	gitT(t, top, "-c", "user.name=Mira Dev", "-c", "user.email=mira@acme.test", "commit", "-q", "-m", "older lore")
	return attrs, gi
}

// TestClientRepoGitWiringChangesNoTrackedFile (devco:D-754, hunt H-13): in a
// client repo that still holds row files an older lore wired, lore doctor
// --fix (an install step), a write and lore fix's rewire leave the tracked
// .gitattributes and .gitignore byte for byte as they are, while the same
// run says "lore never changes its tracked files": the merge rules go to
// the clone's own .git/info/attributes, lore's local files to
// .git/info/exclude, and the rule hiding a row file is said, not rewritten.
// The same repo as an own repo still gets the tracked block (the control).
func TestClientRepoGitWiringChangesNoTrackedFile(t *testing.T) {
	f := newLiveFixture(t)
	attrs, gi := clientRepoWithOldWiring(t, f)
	client := func(context.Context) bool { return true }
	f.s.h.cfg.ClientRepo = client
	top := f.s.top

	f.s.ensureGitWiring(f.ctx, top, wireInstall) // lore doctor --fix
	if b, _ := os.ReadFile(filepath.Join(top, gitAttributesFile)); string(b) != string(attrs) {
		t.Fatalf("doctor --fix rewrote a client repo's tracked .gitattributes:\n%s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(top, gitignoreName)); string(b) != string(gi) {
		t.Fatalf("doctor --fix rewrote a client repo's tracked .gitignore:\n%s", b)
	}
	if !attributesHeldLocally(filepath.Join(top, ".git", "info", "attributes")) {
		t.Fatal("a client repo's row files got no merge rules (none in .git/info/attributes)")
	}
	if got := f.s.h.cfg.Stderr.(*strings.Builder).String(); !strings.Contains(got, "git ignores lore data in this client repo") {
		t.Fatalf("the rule hiding a row file was not said:\n%s", got)
	}

	write := &Run{Entry: specEntry{spec: op.Spec{Name: "memory.add", Access: op.AccessWrite}}}
	if err := GitignoreHookFor(top, client).Before(context.Background(), write); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(top, gitignoreName)); string(b) != string(gi) {
		t.Fatalf("a write added lore's lines to a client repo's tracked .gitignore:\n%s", b)
	}
	if b, err := os.ReadFile(filepath.Join(top, ".git", "info", "exclude")); err != nil || !strings.Contains(string(b), ".lore/lock") {
		t.Fatalf("a client repo's write left lore's lock in git status (no .git/info/exclude line): %q %v", b, err)
	}
	if why, local := excludeReason(f.ctx, top, false, true, client); !local || why != excludeClient {
		t.Errorf("an install step in a client repo would write the tracked .gitignore (why %d, local %v)", why, local)
	}
	if got := rewireTracked(f.ctx, top, client); len(got) != 0 {
		t.Errorf("lore fix's rewire wrote %v in a client repo", got)
	}

	// control: the same repo as an own repo gets the tracked block
	f.s.h.cfg.ClientRepo = nil
	f.s.ensureGitWiring(f.ctx, top, wireInstall)
	if b, _ := os.ReadFile(filepath.Join(top, gitAttributesFile)); string(b) == string(attrs) {
		t.Fatal("control: doctor --fix in an own repo no longer refreshes the tracked .gitattributes")
	}
}
