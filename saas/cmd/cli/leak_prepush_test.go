package main

// Unit tests of the pre-push leak check's plumbing (leak_prepush.go) and
// of the block message's fixes (check_cmd.go leakFix). The end-to-end
// behaviour (real hook, real store, bare origin) is in
// leak_trial_e2e_test.go.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"saas/pkg/aicoder/leakcheck"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := testCmd(t, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeAndCommit(t *testing.T, dir, rel, body, msg string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "--no-verify", "-m", msg)
	return runGit(t, dir, "rev-parse", "HEAD")
}

// pushItems sends to the check exactly what the remote does not have:
// files and messages of new commits, never those already on origin, and
// nothing for a delete. Matrix: new branch × existing branch × delete.
func TestPushItemsAreOnlyWhatTheRemoteLacks(t *testing.T) {
	origin := t.TempDir()
	runGit(t, origin, "init", "-q", "--bare", "-b", "main")
	dir := gitInitRepo(t)
	runGit(t, dir, "remote", "add", "origin", origin)
	base := writeAndCommit(t, dir, "old.txt", "already on the remote\n", "old commit")
	runGit(t, dir, "push", "-q", "--no-verify", "origin", "main")
	runGit(t, dir, "fetch", "-q", "origin")
	runGit(t, dir, "switch", "-q", "-c", "feat")
	tip := writeAndCommit(t, dir, "src/new.ts", "new content\n", "new commit")
	ctx := context.Background()

	whereSet := func(items []leakcheck.Item) string {
		var w []string
		for _, it := range items {
			if strings.HasPrefix(it.Where, "message of commit ") {
				w = append(w, "msg:"+strings.TrimSpace(string(it.Content)))
				continue
			}
			w = append(w, it.Where)
		}
		sort.Strings(w)
		return strings.Join(w, ",")
	}
	cases := []struct {
		name string
		u    pushUpdate
		want string
	}{
		{"new branch", pushUpdate{"refs/heads/feat", tip, "refs/heads/feat", strings.Repeat("0", 40)}, "msg:new commit,src/new.ts"},
		{"existing branch at base", pushUpdate{"refs/heads/feat", tip, "refs/heads/main", base}, "msg:new commit,src/new.ts"},
		{"delete", pushUpdate{"(delete)", strings.Repeat("0", 40), "refs/heads/feat", tip}, ""},
		{"nothing new", pushUpdate{"refs/heads/main", base, "refs/heads/main", base}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items, err := pushItems(ctx, dir, "origin", []pushUpdate{c.u})
			if err != nil {
				t.Fatal(err)
			}
			if got := whereSet(items); got != c.want {
				t.Fatalf("items %q, want %q", got, c.want)
			}
		})
	}
	// A tip lore cannot read fails (the caller refuses the push).
	if _, err := pushItems(ctx, dir, "origin", []pushUpdate{{"refs/heads/x", strings.Repeat("1", 40), "refs/heads/x", strings.Repeat("0", 40)}}); err == nil {
		t.Fatal("an unreadable tip must be an error, never an empty list")
	}
}

func TestParsePushUpdates(t *testing.T) {
	in := "refs/heads/a 1111 refs/heads/a 0000\n\nmalformed line\nrefs/heads/b 2222 refs/heads/b 3333\n"
	got := parsePushUpdates([]byte(in))
	if len(got) != 2 || got[0].localSHA != "1111" || got[1].remoteSHA != "3333" {
		t.Fatalf("got %+v", got)
	}
	for oid, zero := range map[string]bool{strings.Repeat("0", 40): true, strings.Repeat("0", 64): true, "": false, "0001": false} {
		if isZeroOID(oid) != zero {
			t.Fatalf("isZeroOID(%q) = %v", oid, !zero)
		}
	}
}

// F10, F11: the fix a refusal offers names what the user can act on.
func TestLeakFixNamesTheActionableRow(t *testing.T) {
	const (
		mem   = "mem_0b770000000000000000000000000001"
		other = "mem_0c120000000000000000000000000002"
		rul   = "rul_0d000000000000000000000000000003"
	)
	rowFile := ".lore/data/rules/" + rul + ".json"
	cases := []struct {
		name    string
		f       leakcheck.Finding
		lc      leakContext
		must    []string
		mustNot []string
	}{
		{"commit of a leaking repo rule", leakcheck.Finding{Rule: leakcheck.RuleText, Path: rowFile, RowID: mem},
			leakContext{action: leakActionCommit}, []string{"lore move " + rul + " --to <org>", "lore rule delete " + rul + " --confirm"}, []string{mem}},
		{"push of a leaking repo rule", leakcheck.Finding{Rule: leakcheck.RuleText, Path: rowFile, RowID: mem},
			leakContext{action: leakActionPush}, []string{rul, "rewrite that commit"}, []string{"lore move " + mem}},
		{"commit of a source file", leakcheck.Finding{Rule: leakcheck.RuleText, Path: "src/a.ts", RowID: mem},
			leakContext{action: leakActionCommit}, []string{"Remove the internal text from src/a.ts"}, []string{"lore move"}},
		{"commit message", leakcheck.Finding{Rule: leakcheck.RuleText, RowID: mem},
			leakContext{action: leakActionCommit}, []string{"the commit message"}, nil},
		{"move quoting another note", leakcheck.Finding{Rule: leakcheck.RuleText, RowID: other},
			leakContext{action: leakActionMove, source: mem}, []string{"Not moved", other, "Remove that part from " + mem}, []string{"lore move"}},
		{"write --to repo", leakcheck.Finding{Rule: leakcheck.RuleText, RowID: mem},
			leakContext{action: leakActionWrite}, []string{"Nothing written", "--to <org>"}, []string{"lore move " + mem}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := leakFix(c.f, c.lc)
			for _, s := range c.must {
				if !strings.Contains(got, s) {
					t.Fatalf("fix %q must say %q", got, s)
				}
			}
			for _, s := range c.mustNot {
				if strings.Contains(got, s) {
					t.Fatalf("fix %q must not say %q", got, s)
				}
			}
		})
	}
	// The move refusal's whole message never offers the same command.
	err := leakBlockedError([]leakcheck.Finding{{Rule: leakcheck.RuleText, Where: "move x", RowID: other, Words: 9}}, false, leakContext{action: leakActionMove, what: "this move", source: mem})
	if strings.Contains(err.Error(), "lore move <id> --to repo") {
		t.Fatalf("a move refusal must not tell the user to move again: %s", err)
	}
}
