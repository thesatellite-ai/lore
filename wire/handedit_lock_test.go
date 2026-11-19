package wire

// handedit_lock_test.go — devco D-437: four `add --to <store>/<folder>`
// processes each printed "wrote …" while their rows stayed untracked in
// the store clone until a later read picked them up as hand edits. The
// pick-up of another lore process ran `git status` and `git reset`
// (Unstage) on the clone without the store lock, between a writer's
// `git add` and its `git commit --allow-empty`: the writer's commit came
// out empty and its row file was left behind.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestPickUpWaitsForAWriterOfTheClone: while a writer holds the store
// lock with a new row file staged but not committed yet, a read in
// another App (another lore process) leaves the clone's index alone; the
// writer's commit holds its row and the clone ends clean.
func TestPickUpWaitsForAWriterOfTheClone(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	bareStore(t, hosts, "acme/kb")
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpInit, `{"name":"kb","url":"https://`+histHost+`/acme/kb"}`, true); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openWith(t, repo, Options{Home: a.opts.Home}) // the next command attaches kb
	histAddMemory(t, a, "kb", "first row")
	st, err := a.state(background())
	if err != nil {
		t.Fatal(err)
	}
	gb := gitOf(st.byName["kb"])
	if gb == nil || !gb.CloneMode() {
		t.Fatal("store kb is not a git clone")
	}
	clone := gb.Dir()
	home := a.opts.Home

	// the reader (another lore process), opened before the writer starts
	r := openWith(t, repo, Options{Home: home})
	rst, err := r.state(background())
	if err != nil {
		t.Fatal(err)
	}

	// the writer: a second App on the same clone holds its store lock,
	// writes a row file and stages it (git add), not committed yet
	w := openWith(t, repo, Options{Home: home})
	wst, err := w.state(background())
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := gitOf(wst.byName["kb"]).Hold(background())
	if err != nil {
		t.Fatal(err)
	}
	src := firstRowFile(t, clone, schema.TableMemories)
	raw := mustRead(t, src)
	const newID = "mem_0e45506e64357c94a87e70ef1c6bb0d7"
	oldID := strings.TrimSuffix(filepath.Base(src), ".json")
	rel, err := filepath.Rel(clone, filepath.Join(filepath.Dir(src), newID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(strings.Replace(string(raw), "first row", "the writer's row", 1), oldID, newID)
	if err = os.WriteFile(filepath.Join(clone, rel), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, clone, nil, "add", "--", rel)

	// the reader's pick-up of hand edits (every command runs it on open)
	// must wait for the writer's lock before it looks at the index
	done := make(chan []string, 1)
	go func() {
		var said []string
		rst.pickUpHandEdits(background(), func(m string) { said = append(said, m) })
		done <- said
	}()
	select {
	case said := <-done:
		t.Fatalf("the pick-up ran while a writer held the store lock: %q", said)
	case <-time.After(700 * time.Millisecond):
	}
	if staged := gitIn(t, clone, nil, "diff", "--cached", "--name-only"); !strings.Contains(staged, newID) {
		t.Fatalf("a read unstaged a writer's row while the writer held the store lock (staged now: %q)", staged)
	}
	gitIn(t, clone, nil, "commit", "-q", "--no-verify", "-m", "lore: memory add: the writer's row")
	if err = release(); err != nil {
		t.Fatal(err)
	}
	if said := <-done; len(said) != 0 {
		t.Fatalf("the pick-up took the writer's committed row for a hand edit: %q", said)
	}
	if files := gitIn(t, clone, nil, "show", "--name-only", "--format=", "HEAD~0"); !strings.Contains(files, newID) {
		if log := gitIn(t, clone, nil, "log", "--name-only", "--format=%s", "-3"); !strings.Contains(log, newID) {
			t.Fatalf("the writer's row is in no commit:\n%s", log)
		}
	}
	if status := gitIn(t, clone, nil, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("the clone is not clean after the writer committed:\n%s", status)
	}
}

// firstRowFile is the path of one committed row file of table in the
// clone.
func firstRowFile(t *testing.T, clone string, table string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(clone, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() && filepath.Base(filepath.Dir(p)) == table && strings.HasSuffix(p, ".json") {
			found = p
		}
		return nil
	})
	if err != nil || found == "" {
		t.Fatalf("no %s row file in %s: %v", table, clone, err)
	}
	return found
}
