package wire

// storeuse_test.go — `lore store use` (D1 "use my own clone"): a person's
// own clone of a configured store, made anywhere with git clone, becomes
// the clone lore reads and writes; --default goes back and never touches
// the person's clone; every refusal names its cause and the next step.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// useOut decodes a store.use result.
func useOut(t *testing.T, out string) opsstores.UseOut {
	t.Helper()
	var got opsstores.UseOut // store operations answer the bare object
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("store use output %s: %v", out, err)
	}
	return got
}

// wantErr checks err's code and that its message and hint name want.
func wantErr(t *testing.T, what string, err error, code errs.Code, inMsg, inHint string) {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != code || !strings.Contains(e.Message, inMsg) || !strings.Contains(e.Hint, inHint) {
		t.Fatalf("%s: want %s naming %q (hint %q), got %v (hint %q)", what, code, inMsg, inHint, err, hintOf(err))
	}
}

func TestStoreUse(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	url, _ := bareStore(t, hosts, "acme/kb")
	home := t.TempDir()
	repo := openWith(t, gitRepo(t), Options{Home: home})
	if _, err := run(t, repo, opsstores.OpInit, `{"name":"kb","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := run(t, repo, opsstores.OpFolderAdd, `{"store":"kb","folder":"client_a"}`, true); err != nil {
		t.Fatalf("folder add: %v", err)
	}
	// C5: the folder commit waits in lore's clone until the person pushes
	if _, err := run(t, repo, opsstores.OpPush, `{"store":"kb"}`, true); err != nil {
		t.Fatalf("store push: %v", err)
	}
	lores := filepath.Join(home, gitbackend.StoresDir, "acme", "kb")

	// the person's own clone, anywhere
	mine := filepath.Join(t.TempDir(), "work", "kb")
	gitIn(t, t.TempDir(), nil, "clone", "-q", url, mine)
	out, err := run(t, repo, opsstores.OpUse, `{"path":"`+filepath.Join(mine, "client_a")+`"}`, true)
	if err != nil {
		t.Fatalf("store use: %v", err)
	}
	got := useOut(t, out)
	if !got.Changed || !got.Own || got.Path != mine || got.Store != "kb" || got.Previous != lores || got.Unused != lores {
		t.Fatalf("store use = %+v (%s)", got, out)
	}
	if again := useOut(t, mustRun(t, repo, opsstores.OpUse, `{"path":"`+mine+`"}`)); again.Changed {
		t.Fatalf("a second use changed something: %+v", again)
	}
	if rec := gitbackend.ReadCloneRecord(home).Clones["example.test/acme/kb"]; rec.Path != mine || rec.Why != gitbackend.WhyOwn {
		t.Fatalf("record: %+v", rec)
	}

	// lore now writes the person's clone: a task added in its folder is
	// committed there and pushed
	in := openWith(t, filepath.Join(mine, "client_a"), Options{Home: home})
	if _, err = run(t, in, opstask.OpAdd, `{"title":"written through my clone"}`, true); err != nil {
		t.Fatalf("task add in my clone: %v", err)
	}
	if log := gitIn(t, mine, nil, "log", "-1", "--name-only", "--format="); !strings.Contains(log, "client_a/") {
		t.Fatalf("the task was not committed in my clone: %s", log)
	}
	if _, serr := os.Stat(filepath.Join(mine, ".git", "lore", "lock")); serr != nil {
		t.Fatalf("lore's lock is not in my clone's .git/lore: %v", serr)
	}
	if attrs, aerr := os.ReadFile(filepath.Join(mine, ".git", "info", "attributes")); aerr != nil || !strings.Contains(string(attrs), "*.json -merge") {
		t.Fatalf("my clone got no row attributes: %q %v", attrs, aerr)
	}
	found := false
	scan, err := gitbackend.ScanClones(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range scan {
		if c.Paths.Dir == mine && c.Own && c.State == gitbackend.CloneWhole {
			found = true
		}
	}
	if !found {
		t.Fatalf("the clone scan does not list my clone as own: %+v", scan)
	}

	// refusals: not a clone, a clone of no lore store, a store lore does
	// not know, a name that is no store
	plain := t.TempDir()
	_, err = run(t, repo, opsstores.OpUse, `{"path":"`+plain+`"}`, true)
	wantErr(t, "a plain folder", err, errs.NotAStore, plain, "git clone")
	code := gitRepo(t)
	_, err = run(t, repo, opsstores.OpUse, `{"path":"`+code+`"}`, true)
	wantErr(t, "a code repo", err, errs.NotAStore, "lore-store.yaml", "lore store list")
	otherURL, _ := bareStore(t, hosts, "acme/other")
	other := openWith(t, gitRepo(t), Options{Home: t.TempDir()})
	if _, err = run(t, other, opsstores.OpInit, `{"name":"other","url":"`+otherURL+`"}`, true); err != nil {
		t.Fatalf("init other: %v", err)
	}
	theirs := filepath.Join(t.TempDir(), "other")
	gitIn(t, t.TempDir(), nil, "clone", "-q", otherURL, theirs)
	_, err = run(t, repo, opsstores.OpUse, `{"path":"`+theirs+`"}`, true)
	wantErr(t, "a store lore does not know", err, errs.NotFound, "example.test/acme/other", "lore store add")
	_, err = run(t, repo, opsstores.OpUse, `{"path":"`+mine+`","store":"nope"}`, true)
	wantErr(t, "no such store", err, errs.NotFound, "nope", "lore store list")
	_, err = run(t, repo, opsstores.OpUse, `{}`, true)
	wantErr(t, "no path", err, errs.InvalidInput, "usage", "git clone")

	// back to lore's own clone: the record goes, my clone stays
	back := useOut(t, mustRun(t, repo, opsstores.OpUse, `{"default":"kb"}`))
	if !back.Changed || back.Own || back.Path != lores || back.Previous != mine {
		t.Fatalf("store use --default = %+v", back)
	}
	if _, ok := gitbackend.ReadCloneRecord(home).Clones["example.test/acme/kb"]; ok {
		t.Fatal("--default kept the record")
	}
	if _, serr := os.Stat(filepath.Join(mine, "lore-store.yaml")); serr != nil {
		t.Fatalf("--default touched my clone: %v", serr)
	}

	// lore's clone holding a commit its remote lacks blocks a switch
	gitIn(t, lores, nil, "commit", "-q", "--allow-empty", "-m", "offline write")
	_, err = run(t, repo, opsstores.OpUse, `{"path":"`+mine+`"}`, true)
	wantErr(t, "unpushed work in lore's clone", err, errs.Refused, "unpushed", "lore sync")
}
