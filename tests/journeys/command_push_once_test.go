//go:build journeys && !windows

package journeys

// command_push_once_test.go — devco:D-1002 (store growth SG-4 follow-up):
// in push mode now a command pushed after each of its writes, and task
// import committed its batch before every record of a task tree (a child
// read its parent back from the store). Now a command pushes once, at its
// end, and an import of task trees is one commit.
//
//	store me, push=now ─▶ task import of 3 trees (12 tasks, statuses) ─▶ 1 commit, 1 push line
//	the same file into the repo ─▶ 1 live commit (before: 15)
//	task add --template of 4 tasks ─▶ 1 live commit (before: one per task)
//	the trees read as in the file: 9 sub-tasks, 3 done, no shared ranks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyCommandPushesOnce(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona("Ada Vance", "ada@acme-co.example")
	lap := ada.Laptop()
	me := w.Remote("github.com/ada-vance/lore-me")
	code := lap.Clone(w.Remote("github.com/acme-co/widgets"), "widgets")
	clone := filepath.Join(lap.Home, ".lore", "stores", "ada-vance", "lore-me", ".git")

	w.Step("day1/a-personal-store-that-pushes-at-once", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		lap.LoreIn(code, "init").OK(t)
		lap.Lore(lap.Home, "store", "set", "--store", "me", "--settings", "push=now").OK(t)
		lap.LoreIn(code, "task", "add", "widget backlog", "--to", "me").OK(t) // actor row and first push out of the way
		lap.Lore(lap.Home, "sync").OK(t)
	})

	w.Step("day1/a-tree-import-is-one-commit-and-one-push", func() {
		file := filepath.Join(lap.Home, "gadgets.csv")
		if err := os.WriteFile(file, []byte(treeFile("gadget")), 0o600); err != nil {
			t.Fatal(err)
		}
		local, remote := jSqCount(t, clone, "HEAD"), jSqCount(t, me.Dir, "HEAD")
		r := lap.LoreIn(code, "task", "import", "--file", file, "--to", "me", "--yes").OK(t)
		r.Says(t, "12 tasks imported")
		if d := jSqCount(t, clone, "HEAD") - local; d != 1 {
			t.Fatalf("the import of 3 task trees made %d commits, want 1", d)
		}
		if n := strings.Count(r.Out(), "pushed store"); n != 1 {
			t.Fatalf("the import pushed %d times, want once:\n%s", n, r.Out())
		}
		if d := jSqCount(t, me.Dir, "HEAD") - remote; d != 1 {
			t.Fatalf("the remote gained %d commits, want 1", d)
		}
	})

	w.Step("day1/a-tree-import-into-the-repo-is-one-live-commit", func() {
		// no push squashes here: the repo's live ref shows each commit the
		// import made (before: one per task-tree record, 13 for this file)
		code.Lore("task", "add", "widget chore").OK(t) // the live ref exists before the import
		code.WriteFile("trees.csv", treeFile("sprocket"))
		before := splitLiveCommits(t, code.Git)
		code.Lore("task", "import", "--file", "trees.csv", "--yes").OK(t).Says(t, "12 tasks imported")
		if d := splitLiveCommits(t, code.Git) - before; d != 1 {
			t.Fatalf("the import of 3 task trees made %d live commits, want 1", d)
		}
		code.Lore("task", "tree", "check").OK(t).Says(t, "task tree ok")
	})

	w.Step("day1/a-template-is-one-live-commit", func() {
		code.WriteFile("rel.yaml", "task: {title: Widget release, children: [{title: Tag the widget, children: [{title: Sign the tag}]}, {title: Ship the widget}]}\n")
		code.Lore("task", "template", "add", "--name", "rel", "--from-file", "rel.yaml", "--yes").OK(t)
		before := splitLiveCommits(t, code.Git)
		code.Lore("task", "add", "--template", "rel", "--yes").OK(t)
		if d := splitLiveCommits(t, code.Git) - before; d != 1 {
			t.Fatalf("a template of 4 tasks made %d live commits, want 1", d)
		}
		code.Lore("task", "tree", "check").OK(t).Says(t, "task tree ok")
	})

	w.Step("day1/the-trees-read-as-in-the-file", func() {
		tree := lap.LoreIn(code, "task", "list", "--all", "--store", "me", "--json").OK(t).Stdout
		if n := strings.Count(tree, `"title": "gadget step `); n != 9 {
			t.Fatalf("task list shows %d sub-tasks, want 9:\n%s", n, tree)
		}
		done := lap.LoreIn(code, "task", "list", "--status", "done", "--store", "me", "--json").OK(t).Stdout
		if n := strings.Count(done, `"title": "gadget step `); n != 3 {
			t.Fatalf("task list --status done shows %d sub-tasks, want 3:\n%s", n, done)
		}
		// shared sibling ranks would mean a child did not see its staged
		// siblings (the rank chain reads the stage, D-1002)
		lap.LoreIn(code, "task", "tree", "check").OK(t).Says(t, "task tree ok")
	})
}

// treeFile is a task import file of 3 trees of noun: an epic with 3 steps
// each, the first step done.
func treeFile(noun string) string {
	var b strings.Builder
	b.WriteString("id,title,parent,status\n")
	for tr := range 3 {
		fmt.Fprintf(&b, "%s-%d,%s epic %d,,todo\n", noun, tr, noun, tr)
		for c := range 3 {
			st := "todo"
			if c == 0 {
				st = "done"
			}
			fmt.Fprintf(&b, "%s-%d-%d,%s step %d.%d,%s-%d,%s\n", noun, tr, c, noun, tr, c, noun, tr, st)
		}
	}
	return b.String()
}
