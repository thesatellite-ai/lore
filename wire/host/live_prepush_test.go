package host

// Ports of the old binary's pre-push F16 input tests (later 629;
// saas/cmd/cli/leak_prepush_test.go TestPushItemsAreOnlyWhatTheRemoteLacks,
// TestParsePushUpdates) onto wire/host's pushDocs / parsePushUpdates.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/internal/testenv"
)

// commitFile writes rel in dir, commits it with msg and returns the commit.
func commitFile(t *testing.T, dir, rel, body, msg string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", msg)
	out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: dir, Args: []string{"rev-parse", "HEAD"}})
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// TestPushDocsAreOnlyWhatTheRemoteLacks: the pre-push check reads only what
// the push sends (blobs by path, commit messages): a new branch and a
// branch ahead of the remote send the new commit only; a delete and a push
// of what the remote has send nothing; a tip lore cannot read is an error
// (the hook then refuses the push), never an empty list.
func TestPushDocsAreOnlyWhatTheRemoteLacks(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ctx := context.Background()
	origin := filepath.Join(t.TempDir(), "origin.git")
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "remote", "add", "origin", origin)
	base := commitFile(t, dir, "old.txt", "already on the remote\n", "old commit")
	gitT(t, dir, "push", "-q", "--no-verify", "origin", "main")
	gitT(t, dir, "fetch", "-q", "origin")
	gitT(t, dir, "switch", "-q", "-c", "feat")
	tip := commitFile(t, dir, "src/new.ts", "new content\n", "new commit")
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	zero := strings.Repeat("0", len(tip))
	whereSet := func(docs map[string][]byte) string {
		var w []string
		for where, b := range docs {
			if strings.HasPrefix(where, commitMsgWhere) {
				w = append(w, "msg:"+strings.TrimSpace(string(b)))
				continue
			}
			w = append(w, where)
		}
		sort.Strings(w)
		return strings.Join(w, ",")
	}
	cases := []struct {
		name string
		u    pushUpdate
		want string
	}{
		{"new branch", pushUpdate{"refs/heads/feat", tip, "refs/heads/feat", zero}, "msg:new commit,src/new.ts"},
		{"existing branch at base", pushUpdate{"refs/heads/feat", tip, "refs/heads/main", base}, "msg:new commit,src/new.ts"},
		{"delete", pushUpdate{"(delete)", zero, "refs/heads/feat", tip}, ""},
		{"nothing new", pushUpdate{"refs/heads/main", base, "refs/heads/main", base}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			docs, err := pushDocs(ctx, repo, "origin", []pushUpdate{c.u})
			if err != nil {
				t.Fatal(err)
			}
			if got := whereSet(docs); got != c.want {
				t.Fatalf("docs %q, want %q", got, c.want)
			}
		})
	}
	unreadable := strings.Repeat("1", len(tip))
	if _, err := pushDocs(ctx, repo, "origin", []pushUpdate{{"refs/heads/x", unreadable, "refs/heads/x", zero}}); err == nil {
		t.Fatal("an unreadable tip must be an error, never an empty list")
	}
}

// TestParsePushUpdates: git's pre-push lines; blank and malformed lines are
// skipped; the zero object id is recognised in both hash lengths.
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
