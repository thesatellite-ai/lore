package wire

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestClientTaskUnheldNoteGoesToTheStore (later 65, v0.1.17 fix-r15note on
// cmd/lore): in a client repo linked to a 0.1.18 org store, a note on the
// client repo's own task that nobody holds is a comment row in the store's
// linked folder (<project>/<table>/), written through the core and
// pushed to the store's main branch. Nothing lands in the client repo (no
// file, no commit) and nothing on a live ref of either side. The
// multi-clone half (a second clone reads the note) is a team e2e
// (T-TEAME2E, TestRecipe15_NoteOnATaskNotHeldGoesToTheStore). The fixture
// ran `lore store init --name agency` in the client repo, so stores.yaml attaches
// the bare name "agency" at the store root and the linked folder is
// attached as "agency/acme" (renameOrgClashes): coordination must use the
// latter (it wrote into the store root before).
func TestClientTaskUnheldNoteGoesToTheStore(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	h := orgHostAt(t, w.repo, mira)
	if _, err := h.Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := h.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "acme"}, true); err != nil {
		t.Fatalf("link: %v", err)
	}
	app, err := New(StandaloneOptions(w.repo, mira, w.repo, "test"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	withProject(t, app)
	out := mustRun(t, app, opstask.OpAdd, `{"title":"ship the login page","to":"repo"}`)
	var added struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.Unmarshal([]byte(out), &added); err != nil || !strings.HasPrefix(added.Data.ID, "tsk_") {
		t.Fatalf("task add output has no task id: %s (%v)", out, err)
	}
	tid := added.Data.ID

	repoHead := orgGit(t, w.repo, "rev-parse", "HEAD")
	repoDirty := orgGit(t, w.repo, "status", "--porcelain", "--untracked-files=all")
	storeBefore := w.storeFiles(t)

	lines, err := noteLines(t, app, `{"id":"`+tid+`","text":["the copy needs","a second review"]}`)
	if err != nil {
		t.Fatalf("note: %v", err)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "✓ note cmt_") || !strings.HasSuffix(lines[0], " on "+tid) {
		t.Fatalf("note report %q", lines)
	}
	cid := strings.Fields(lines[0])[2]

	// the comment is in the store's linked folder on main, naming the task
	// (once the person pushes: C5)
	w.push(t, mira, orgTestStoreURL)
	store := filepath.Join(w.hosts, "agency/notes.git")
	var path string
	for _, f := range strings.Split(w.storeFiles(t), "\n") {
		if strings.Contains(f, cid) {
			path = f
		}
	}
	if !strings.HasPrefix(path, "acme/comments/") || strings.Contains(storeBefore, cid) {
		t.Fatalf("comment %s not in the linked folder acme on the store's main:\n%s", cid, w.storeFiles(t))
	}
	body := orgGit(t, store, "show", "main:"+path)
	if !strings.Contains(body, tid) || !strings.Contains(body, "a second review") {
		t.Fatalf("store comment does not name the task and text:\n%s", body)
	}

	// nothing in the client repo, nothing on any live ref
	if got := orgGit(t, w.repo, "rev-parse", "HEAD"); got != repoHead {
		t.Fatalf("the note made a client repo commit: %s -> %s", repoHead, got)
	}
	if got := orgGit(t, w.repo, "status", "--porcelain", "--untracked-files=all"); got != repoDirty {
		t.Fatalf("the note changed client repo files:\nbefore:\n%s\nafter:\n%s", repoDirty, got)
	}
	for _, dir := range []string{store, w.repo, filepath.Join(w.hosts, "acme/app.git")} {
		if refs := orgGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/lore/live", "refs/remotes/origin/lore/live"); refs != "" {
			t.Fatalf("%s has a live ref after an unheld note: %s", dir, refs)
		}
	}

	// the claimed half: a claim of a second client task keeps the claim on
	// the store's signals ref under the linked folder and its linked row
	// (clientRows) in acme, never at the store root
	out2 := mustRun(t, app, opstask.OpAdd, `{"title":"fix the footer","to":"repo"}`)
	var t2 struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.Unmarshal([]byte(out2), &t2); err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, app, "task.claim", `{"id":"`+t2.Data.ID+`"}`, true); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got := orgGit(t, store, "ls-tree", "-r", "--name-only", "refs/lore/signals/main"); got != "acme/claims/"+t2.Data.ID+".json" {
		t.Fatalf("store signals ref holds %q, want the claim under the linked folder acme/", got)
	}
	w.push(t, mira, orgTestStoreURL) // C5: the task row waits for the person's push
	linkedRow := false
	for _, f := range strings.Split(w.storeFiles(t), "\n") {
		linkedRow = linkedRow || strings.HasPrefix(f, "acme/tasks/")
		if strings.HasPrefix(f, "tasks/") || strings.HasPrefix(f, "comments/") {
			t.Fatalf("a client task's coordination row reached the store root: %s", f)
		}
	}
	if !linkedRow {
		t.Fatalf("no linked task row under acme/tasks:\n%s", w.storeFiles(t))
	}
}
