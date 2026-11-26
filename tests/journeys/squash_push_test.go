//go:build journeys && !windows

package journeys

// squash_push_test.go — devco:D-1000 (store growth SG-4): lore made one
// remote commit per write (a busy store grew ~600 commits a day), and
// `lore batch undo` of an import made one commit per row. Now every push
// folds the waiting writes into one commit per author (squash=on, the
// default in every push mode), an undo is one commit, and `lore store set
// --settings squash=off` keeps one remote commit per write.
//
//	20 writes offline (push manual) ─▶ lore sync ─▶ the remote gains 1 commit
//	import 50 rows, undo it          ─▶ 2 local commits ─▶ sync ─▶ +1 remote
//	archive 21 rows, undo it         ─▶ the undo is 1 commit ─▶ sync ─▶ +1 remote
//	meta set on 10 rows; task delete --where of 5 ─▶ 1 commit each
//	squash=off, 3 writes             ─▶ sync ─▶ +3 remote (+1 settings commit)

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// jSqCount is the commit count of rev in the git dir gitDir.
func jSqCount(t *testing.T, gitDir, rev string) int {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", gitDir, "rev-list", "--count", rev).CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-list %s: %v\n%s", rev, err, out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestJourneyPushSquashesWrites(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nia := w.Persona("Nia Okoro", "nia@acme-co.example")
	lap := nia.Laptop()
	me := w.Remote("github.com/nia-okoro/lore-me")
	code := lap.Clone(w.Remote("github.com/acme-co/orbit"), "orbit")
	clone := filepath.Join(lap.Home, ".lore", "stores", "nia-okoro", "lore-me", ".git")
	var remote int
	gained := func() int {
		t.Helper()
		n := jSqCount(t, me.Dir, "HEAD")
		d := n - remote
		remote = n
		return d
	}

	w.Step("day1/a-personal-store", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		lap.LoreIn(code, "init").OK(t)
		// the first write also adds Nia's own actor row: a roster change
		// keeps a commit of its own (the replay judges later commits
		// against the roster it leaves), so it goes out before the count
		lap.LoreIn(code, "memory", "add", "--body", "widget note: the first one", "--to", "me").OK(t)
		lap.Lore(lap.Home, "sync").OK(t)
		gained()
	})

	w.Step("day1/twenty-writes-one-remote-commit", func() {
		for i := range 20 {
			lap.LoreIn(code, "memory", "add", "--body", fmt.Sprintf("widget note %d: the export cron runs at noon", i), "--to", "me").OK(t)
		}
		lap.Lore(lap.Home, "store", "show", "--store", "me", "--json").OK(t).Says(t, `"squash": "on"`)
		lap.Lore(lap.Home, "sync").OK(t)
		if d := gained(); d != 1 {
			t.Fatalf("the remote gained %d commits for 20 writes, want 1", d)
		}
		if jSqCount(t, clone, "HEAD") != jSqCount(t, me.Dir, "HEAD") {
			t.Fatal("the clone and the remote differ after the sync")
		}
		lap.Lore(lap.Home, "memory", "list", "--store", "me").OK(t).Says(t, "widget note 0", "widget note 19")
	})

	w.Step("day1/an-import-and-its-undo-one-commit-each", func() {
		var b strings.Builder
		for i := range 50 {
			fmt.Fprintf(&b, `{"op":"memory add","input":{"body":"gadget import %d","to":"me"}}`+"\n", i)
		}
		file := filepath.Join(lap.Home, "gadgets.jsonl")
		if err := os.WriteFile(file, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		local := jSqCount(t, clone, "HEAD")
		out := lap.LoreIn(code, "batch", "apply", "--file", file, "--yes").OK(t).Out()
		m := regexp.MustCompile(`bat_[0-9a-f]{32}`).FindString(out)
		if m == "" {
			t.Fatalf("no batch id in %s", out)
		}
		if d := jSqCount(t, clone, "HEAD") - local; d != 1 {
			t.Fatalf("the import of 50 rows made %d commits, want 1", d)
		}
		lap.LoreIn(code, "batch", "undo", "--id", m, "--yes").OK(t).Says(t, "50")
		if d := jSqCount(t, clone, "HEAD") - local; d != 2 {
			t.Fatalf("the undo of 50 rows made %d commits, want 1", d-1)
		}
		lap.Lore(lap.Home, "memory", "list", "--store", "me").OK(t).Never(t, "gadget import")
		lap.Lore(lap.Home, "sync").OK(t)
		if d := gained(); d != 1 {
			t.Fatalf("the remote gained %d commits for the import and its undo, want 1", d)
		}
	})

	w.Step("day1/an-archive-undo-is-one-commit", func() {
		out := lap.LoreIn(code, "memory", "archive", "--where", "body~=widget note", "--yes").OK(t).Says(t, "archived 21 memory(s)").Out()
		m := regexp.MustCompile(`lore batch undo --id (bat_[0-9a-f]{32})`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no undo hint in %s", out)
		}
		local := jSqCount(t, clone, "HEAD")
		lap.LoreIn(code, "batch", "undo", "--id", m[1], "--yes").OK(t).Says(t, "21")
		if d := jSqCount(t, clone, "HEAD") - local; d != 1 {
			t.Fatalf("the undo of an archive of 21 rows made %d commits, want 1", d)
		}
		lap.Lore(lap.Home, "memory", "list", "--store", "me").OK(t).Says(t, "widget note 0", "widget note 19")
		lap.Lore(lap.Home, "sync").OK(t)
		if d := gained(); d != 1 {
			t.Fatalf("the remote gained %d commits for the archive and its undo, want 1", d)
		}
	})

	w.Step("day1/bulk-meta-and-task-delete-one-commit-each", func() {
		var b strings.Builder
		for i := range 10 {
			fmt.Fprintf(&b, `{"op":"memory add","input":{"body":"core gadget %d","to":"me","meta":["team=core"]}}`+"\n", i)
		}
		file := filepath.Join(lap.Home, "core.jsonl")
		if err := os.WriteFile(file, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		lap.LoreIn(code, "batch", "apply", "--file", file, "--yes").OK(t)
		local := jSqCount(t, clone, "HEAD")
		lap.LoreIn(code, "meta", "set", "--where", "team=core", "--pair", "owner=ops", "--yes").OK(t).Says(t, "on 10 rows")
		if d := jSqCount(t, clone, "HEAD") - local; d != 1 {
			t.Fatalf("a metadata set on 10 rows made %d commits, want 1", d)
		}
		for i := range 5 {
			lap.LoreIn(code, "task", "add", fmt.Sprintf("gizmo chore %d", i), "--to", "me").OK(t)
		}
		local = jSqCount(t, clone, "HEAD")
		lap.LoreIn(code, "task", "delete", "--where", "title~=gizmo chore", "--yes").OK(t).Says(t, "5 tasks")
		if d := jSqCount(t, clone, "HEAD") - local; d != 1 {
			t.Fatalf("a delete --where of 5 tasks made %d commits, want 1", d)
		}
		lap.Lore(lap.Home, "sync").OK(t)
		if d := gained(); d != 1 {
			t.Fatalf("the remote gained %d commits for the import, the metadata set, 5 tasks and their delete, want 1", d)
		}
	})

	w.Step("day1/squash-off-keeps-one-commit-per-write", func() {
		lap.Lore(lap.Home, "store", "set", "--store", "me", "--settings", "squash=off").OK(t).Says(t, "squash off")
		for i := range 3 {
			lap.LoreIn(code, "memory", "add", "--body", fmt.Sprintf("sprocket note %d", i), "--to", "me").OK(t)
		}
		lap.Lore(lap.Home, "sync").OK(t)
		if d := gained(); d != 3 {
			t.Fatalf("with squash=off the remote gained %d commits for 3 writes, want 3", d)
		}
	})
}
