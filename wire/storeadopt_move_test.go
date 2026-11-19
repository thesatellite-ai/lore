package wire

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// liveRefPaths lists the row paths refs/lore/live of repo holds.
func liveRefPaths(t *testing.T, repo string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "ls-tree", "-r", "--name-only", liveRefLocal).Output()
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, p := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(p, liveRootFolder+"/") {
			rows = append(rows, p)
		}
	}
	return rows
}

// TestStoreAdoptRemoveSourceTakesLiveRows (devco D-716): --remove-source
// takes the adopted rows off the repo's live ref as well as its row files.
// Before, the live copies stayed ("refs/lore/live: 3 adopted rows stay on
// the live ref"), and once the repo read the folder each id was "two live
// rows with one id", one `lore row move` per row. The dry run counts them
// and changes nothing; the run removes them in one live commit, names the
// repo commit left, and a rerun finds nothing more to take.
func TestStoreAdoptRemoveSourceTakesLiveRows(t *testing.T) {
	t.Parallel()
	repo, _, _, _ := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"northwind","folder":"client_x"}`, true); err != nil {
		t.Fatal(err)
	}
	src := adoptRepo(t)
	if got := liveRefPaths(t, src); len(got) != 3 {
		t.Fatalf("the fixture's live ref holds %v, want 3 rows", got)
	}
	in := `{"target":"northwind/client_x","from":"` + filepath.ToSlash(src) + `","remove_source":true`

	dry, err := adoptRun(t, a, in+`,"dry_run":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if dry.LiveRemoved != 3 || len(dry.Next) != 1 || !strings.Contains(dry.Text(), "would take 3 adopted rows off the repo's live ref") {
		t.Fatalf("dry run: %+v\n%s", dry, dry.Text())
	}
	if got := liveRefPaths(t, src); len(got) != 3 {
		t.Fatalf("the dry run changed the live ref: %v", got)
	}

	out, err := adoptRun(t, a, in+`}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.LiveRemoved != 3 || len(out.LiveKept) != 0 {
		t.Fatalf("run: %+v", out)
	}
	if got := liveRefPaths(t, src); len(got) != 0 {
		t.Fatalf("the adopted rows are still on the live ref: %v", got)
	}
	want := "git -C " + out.From + " add -A .lore/data && git -C " + out.From + " commit -m \"lore: move the notes to " + out.Store + "/client_x\""
	if len(out.Next) != 1 || out.Next[0] != want || !strings.Contains(out.Text(), want) {
		t.Fatalf("next step %v, want %q\n%s", out.Next, want, out.Text())
	}

	again, err := adoptRun(t, a, in+`}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.LiveRemoved != 0 || again.Removed != 0 {
		t.Fatalf("rerun: %+v", again)
	}
}

// TestStoreAdoptMoveAndCommitChecks (devco D-713): --move and --commit are
// checked before anything is written: --move links the repo it runs in
// (so --from another repo is refused, naming where to run it), --own and
// --commit need what they finish, and --commit refuses to sweep another
// staged file into the move's commit.
func TestStoreAdoptMoveAndCommitChecks(t *testing.T) {
	t.Parallel()
	repo, storeDir, _, _ := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"northwind","folder":"client_x"}`, true); err != nil {
		t.Fatal(err)
	}
	src := adoptRepo(t)
	from := `"target":"northwind/client_x","from":"` + filepath.ToSlash(src) + `"`
	before := treeSums(t, storeDir)
	srcBefore := treeSums(t, src)

	if _, err := adoptRun(t, a, `{`+from+`,"move":true}`, true); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "--move links the repo it runs in") ||
		!strings.Contains(adoptHint(err), "run it inside ") || !strings.Contains(adoptHint(err), "--move") {
		t.Fatalf("--move with --from another repo: %v (hint %q)", err, adoptHint(err))
	}
	if _, err := adoptRun(t, a, `{`+from+`,"own":true}`, true); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "--own") {
		t.Fatalf("--own without --move: %v", err)
	}
	if _, err := adoptRun(t, a, `{`+from+`,"commit":true}`, true); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "--commit") {
		t.Fatalf("--commit without a removal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", src, "add", "README.md").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := adoptRun(t, a, `{`+from+`,"remove_source":true,"commit":true}`, true); !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("--commit with another staged file: %v", err)
	}
	if !mapsEqual(treeSums(t, storeDir), before) {
		t.Fatal("a refused run wrote into the store")
	}
	srcBefore["README.md"] = treeSums(t, src)["README.md"]
	if !mapsEqual(treeSums(t, src), srcBefore) {
		t.Fatal("a refused run changed the repo")
	}
}

// adoptHint is err's hint ("" when it carries none).
func adoptHint(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}
