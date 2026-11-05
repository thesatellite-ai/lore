package reposync

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// branchRepo is a repository whose main holds one rule, with a feature
// branch that adds a second, checked out on main.
func branchRepo(t *testing.T, sub string) (top, dir string) {
	t.Helper()
	top = gitInitRepo(t)
	dir = filepath.Join(top, filepath.FromSlash(sub))
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = top
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	rel := strings.TrimSuffix(sub+"/", "/") // "" or "svc/"
	if rel != "" {
		rel += "/"
	}
	writeRepoFile(t, top, rel+".lore/data/_meta.json", `{"_v":1}`)
	writeRepoFile(t, top, rel+".lore/data/rules/rul_a.json", `{"id":"rul_a","body":"main rule\nsecond line"}`)
	git("add", ".")
	git("commit", "-qm", "base")
	git("checkout", "-q", "-b", "feature/x")
	writeRepoFile(t, top, rel+".lore/data/rules/rul_b.json", `{"id":"rul_b","body":"feature-only fact"}`)
	writeRepoFile(t, top, rel+".lore/data/tasks/tsk_c.json", `{"id":"tsk_c","title":"a task"}`)
	writeRepoFile(t, top, rel+".lore/data/rules/rul_bad.json", `{broken`)
	git("add", ".")
	git("commit", "-qm", "feature")
	git("checkout", "-q", "main")
	return top, dir
}

// TestPeek (TestE2E_PeekOtherBranch): another branch's rows are listed
// with their summaries, a table narrows it, an unreadable file is shown as
// such, the checkout is untouched, and a bad ref is refused.
func TestPeek(t *testing.T) {
	ctx := context.Background()
	for _, sub := range []string{"", "svc/api"} {
		t.Run("project at "+sub, func(t *testing.T) {
			top, dir := branchRepo(t, sub)
			rows, err := Peek(ctx, dir, "feature/x", "")
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range rows {
				got = append(got, r.Table+"/"+r.ID+"="+r.Summary)
			}
			want := []string{"rules/rul_a=main rule", "rules/rul_b=feature-only fact", "rules/rul_bad=(unreadable: invalid character 'b' looking for beginning of object key string)", "tasks/tsk_c=a task"}
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("peek:\n got %q\nwant %q", got, want)
			}
			rows, err = Peek(ctx, dir, "feature/x", "tasks")
			if err != nil || len(rows) != 1 || rows[0].ID != "tsk_c" {
				t.Fatalf("table filter: %+v %v", rows, err)
			}
			if _, err := Peek(ctx, dir, "no-such-ref", ""); err == nil {
				t.Fatal("a bad ref must be refused")
			}
			if lines := PeekLines("feature/x", nil); len(lines) != 1 || !strings.Contains(lines[0], "no lore rows") {
				t.Fatalf("empty peek: %q", lines)
			}
			st := exec.Command("git", "status", "--porcelain")
			st.Dir = top
			if out, _ := st.Output(); len(out) != 0 {
				t.Fatalf("peek changed the checkout: %s", out)
			}
		})
	}
}

// TestPromote (TestE2E_PromoteToMain): a row written on a feature branch
// is committed onto main without switching (dry run first), the work tree
// stays clean, main then holds the file; the checked-out branch, a missing
// branch, an unknown id and no --to are refused.
func TestPromote(t *testing.T) {
	ctx := context.Background()
	top, dir := branchRepo(t, "")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = top
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("checkout", "-q", "feature/x")
	mainBefore := git("rev-parse", "main")
	o := PromoteOptions{Dir: dir, IDs: []string{"rul_b"}, Branch: "main", DryRun: true, Now: time.Unix(1_700_000_000, 0)}
	res, err := Promote(ctx, o)
	if err != nil || res.Commit != "" || strings.Join(res.Paths, ",") != ".lore/data/rules/rul_b.json" {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	if l := PromoteLines(o, res); len(l) != 1 || !strings.Contains(l[0], "would commit") {
		t.Fatalf("dry-run lines: %q", l)
	}
	if git("rev-parse", "main") != mainBefore {
		t.Fatal("a dry run moved main")
	}
	o.DryRun = false
	res, err = Promote(ctx, o)
	if err != nil || res.Commit == "" {
		t.Fatalf("promote: %+v %v", res, err)
	}
	if git("rev-parse", "main") != res.Commit || git("rev-parse", "main~1") != mainBefore {
		t.Fatal("main must move by exactly one commit")
	}
	if st := git("status", "--porcelain"); st != "" {
		t.Fatalf("promote dirtied the checkout: %s", st)
	}
	if body := git("show", "main:.lore/data/rules/rul_b.json"); !strings.Contains(body, "feature-only fact") {
		t.Fatalf("main lacks the promoted row: %s", body)
	}
	if l := PromoteLines(o, res); !strings.Contains(l[0], "not pushed") {
		t.Fatalf("lines: %q", l)
	}
	again, err := Promote(ctx, o)
	if err != nil || again.Commit != "" || git("rev-parse", "main") != res.Commit {
		t.Fatalf("a second promote of the same file must commit nothing: %+v %v", again, err)
	}
	for name, bad := range map[string]PromoteOptions{
		"checked-out branch": {Dir: dir, IDs: []string{"rul_b"}, Branch: "feature/x"},
		"missing branch":     {Dir: dir, IDs: []string{"rul_b"}, Branch: "nope"},
		"unknown id":         {Dir: dir, IDs: []string{"rul_00000000000070008000000000000000"}, Branch: "main"},
		"no branch":          {Dir: dir, IDs: []string{"rul_b"}},
		"no ids":             {Dir: dir, Branch: "main"},
	} {
		if _, err := Promote(ctx, bad); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}
