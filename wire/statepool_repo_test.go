package wire

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestRepoStatePoolSeesEveryChangeToItsFiles (bug hunt H-216): the read
// server hands a kept full repo open to the next command only while every
// file it was built from is unchanged. A hand edit in a store clone's work
// tree or in the repo's .lore (one that keeps the size and the time
// included: git's racy case), a new row file, a branch switch in the repo,
// a changed machine.json, and an edit made while the command ran (after
// its open was handed out) each make the next command open afresh, so it
// sees the change exactly as a one-shot command does. Not parallel: the
// pool is process-wide.
func TestRepoStatePoolSeesEveryChangeToItsFiles(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".lore")
	repo := t.TempDir()
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	clone := filepath.Join(home, "stores", "_local", "kb")
	storeRow := filepath.Join(clone, "work", "notes", "n1.md")
	repoRow := filepath.Join(repo, ".lore", "data", "rules", "r1.md")
	write(filepath.Join(home, "config.yaml"), "personal:\n  store: x\n")
	write(filepath.Join(home, "machine.json"), `{"id":"m1"}`)
	write(filepath.Join(clone, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(filepath.Join(clone, ".git", "refs", "heads", "main"), "1111111111111111111111111111111111111111\n")
	write(storeRow, "billing retries per batch\n")
	write(repoRow, "deploy with make release\n")
	write(filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	stop := EnableStateReuse()
	defer stop()

	// keep puts a kept repo open whose work tree print was taken now (as
	// openState takes it before the open reads the trees)
	keep := func() *state {
		t.Helper()
		since := treeHashSince(time.Now())
		st := &state{poolKey: "k", openedAt: time.Now(), repoKept: true, repoTop: repo, treePrint: repoTreePrint(home, repo, since), treeSince: since}
		if !putState(st, home, repo) {
			t.Fatal("a repo open that wrote nothing was not kept")
		}
		return st
	}
	st := keep()
	if got := takeState("k", home, repo); got != st {
		t.Fatal("an unchanged home and repo did not hand back the kept repo open")
	}
	// sameSizeSameTime rewrites p with its own text in capitals and puts
	// its old time back: no stat field changes, only the content
	sameSizeSameTime := func(p string) {
		t.Helper()
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		write(p, strings.ToUpper(string(body)))
		if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		what   string
		change func()
	}{
		{"a hand edit of a row in a store clone", func() { write(storeRow, "billing retries per batch, then per row\n") }},
		{"a same-size same-time hand edit in a store clone", func() { sameSizeSameTime(storeRow) }},
		{"a hand edit of a repo row", func() { write(repoRow, "deploy with make release, never make all\n") }},
		{"a same-size same-time hand edit of a repo row", func() { sameSizeSameTime(repoRow) }},
		{"a new row file in a store clone", func() { write(filepath.Join(clone, "work", "notes", "n2.md"), "new\n") }},
		{"a branch switch in the repo", func() { write(filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/feature\n") }},
		{"machine.json changed", func() { write(filepath.Join(home, "machine.json"), `{"id":"m2"}`) }},
	} {
		keep()
		c.change()
		if got := takeState("k", home, repo); got != nil {
			t.Errorf("%s: the kept repo open was handed out; the next command must open afresh", c.what)
		}
	}
	// an edit while a command ran: its open was handed out, the edit lands,
	// the command ends and the open goes back; the next command must not
	// take it (the print it carries is from before the edit)
	keep()
	running := takeState("k", home, repo)
	if running == nil {
		t.Fatal("an unchanged repo did not hand back the kept repo open")
	}
	write(storeRow, "edited while a command ran\n")
	if !putState(running, home, repo) {
		t.Fatal("the running command's open was not kept")
	}
	if got := takeState("k", home, repo); got != nil {
		t.Error("a hand edit made while a command ran was taken in as seen: the next command was handed the open that never read it")
	}
	since := treeHashSince(time.Now())
	said := &state{poolKey: "k", openedAt: time.Now(), repoKept: true, repoTop: repo, treePrint: repoTreePrint(home, repo, since), treeSince: since, saidOnFinish: true}
	if putState(said, home, repo) {
		t.Error("a repo open whose pick-up said a line (a hand edit that waits) was kept: the next command would not say it, as a one-shot one does")
	}
}

// TestRepoStatePoolKeepsATreeWhoseFilesAge (bug hunt H-217, devco:D-958):
// a kept repo open stays kept while its files only grow older. A row
// written just before the open (a store write, then reads) is hashed in
// the open's tree print; once it is past racyHashWindow the check of the
// next read must still print it hashed, or the print of an unchanged tree
// differs and the read opens the repo and every store afresh and runs git
// status in each clone (the bench question 2 s after a batch of 300
// comments: warm 95-149 ms instead of ~50). The same-size same-time hand
// edit of that aged file is still seen. Not parallel: the pool is
// process-wide.
func TestRepoStatePoolKeepsATreeWhoseFilesAge(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".lore")
	repo := t.TempDir()
	clone := filepath.Join(home, "stores", "_local", "kb")
	row := filepath.Join(clone, "work", "comments", "c1.md")
	for p, body := range map[string]string{
		filepath.Join(home, "config.yaml"):   "personal:\n  store: x\n",
		filepath.Join(clone, ".git", "HEAD"): "ref: refs/heads/main\n",
		row:                                  "checked the import path again\n",
		filepath.Join(repo, ".lore", "data", "r1.md"): "deploy with make release\n",
		filepath.Join(repo, ".git", "HEAD"):           "ref: refs/heads/main\n",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stop := EnableStateReuse()
	defer stop()
	// written a moment before the line: hashed by the open, past the line
	// by the time the next read checks
	edge := 150 * time.Millisecond
	written := time.Now().Add(-racyHashWindow + edge)
	if err := os.Chtimes(row, written, written); err != nil {
		t.Fatal(err)
	}
	keep := func() *state {
		t.Helper()
		since := treeHashSince(time.Now())
		st := &state{poolKey: "k", openedAt: time.Now(), repoKept: true, repoTop: repo, treePrint: repoTreePrint(home, repo, since), treeSince: since}
		if !putState(st, home, repo) {
			t.Fatal("a repo open that wrote nothing was not kept")
		}
		return st
	}
	st := keep()
	time.Sleep(2 * edge)
	if got := takeState("k", home, repo); got != st {
		t.Fatal("a kept repo open was not handed back once a file it hashed aged past the racy window: the tree did not change, yet the next read opened afresh")
	}
	if !putState(st, home, repo) {
		t.Fatal("the handed-out open was not kept again")
	}
	if err := os.WriteFile(row, []byte("CHECKED THE IMPORT PATH AGAIN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(row, written, written); err != nil {
		t.Fatal(err)
	}
	if got := takeState("k", home, repo); got != nil {
		t.Error("a same-size same-time hand edit of a file the open hashed was missed once the file aged past the racy window")
	}
}

// TestRepoStateKeyNamesTheCaller (H-216): the key of a kept repo open
// prints the plain values of the options and the identity, pointers
// followed (agent detection's result is one), and leaves out the host's
// wiring (funcs, interfaces), whose addresses differ on every command.
func TestRepoStateKeyNamesTheCaller(t *testing.T) {
	type detection struct{ Tool string }
	type who struct {
		Key       string
		Detection *detection
		Hook      func()
		Any       any
	}
	print := func(v who) string {
		var b strings.Builder
		plainPrint(&b, reflect.ValueOf(v), 0)
		return b.String()
	}
	a := print(who{Key: "human:ada", Detection: &detection{Tool: "codex"}, Hook: func() {}, Any: 1})
	if b := print(who{Key: "human:ada", Detection: &detection{Tool: "codex"}, Hook: func() {}, Any: 2}); a != b {
		t.Errorf("the same caller printed two keys (the wiring or a pointer's address got in):\n%s\n%s", a, b)
	}
	if b := print(who{Key: "human:ada", Detection: &detection{Tool: "other"}}); a == b {
		t.Error("two callers that agent detection tells apart printed one key")
	}
	if b := print(who{Key: "human:ada"}); a == b {
		t.Error("a detected agent and no detection printed one key")
	}
}

// TestServedCommandsEachGetTheirOwnNumber (H-216): every command the read
// server runs is its own request, so a kept backend's per-command state
// (storePush.begin) starts afresh, and none is taken for a REST request
// (its audit keeps the CLI's session link).
func TestServedCommandsEachGetTheirOwnNumber(t *testing.T) {
	ctx := context.Background()
	a, b := ServedCommand(ctx), ServedCommand(ctx)
	if requestOf(a) == 0 || requestOf(a) == requestOf(b) {
		t.Errorf("served commands numbered %d and %d; want two different non-zero numbers", requestOf(a), requestOf(b))
	}
	if IsRemoteRequest(a) {
		t.Error("a served command was taken for a REST request")
	}
	if requestOf(ctx) != 0 {
		t.Error("a one-shot command has a request number")
	}
	if r := RemoteRequest(a); requestOf(r) == requestOf(a) || !IsRemoteRequest(r) {
		t.Error("a REST request inside a served command did not get its own number")
	}
}
