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
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
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
	whereSet := func(sets []pushSet) string {
		var w []string
		docs := map[string][]byte{}
		for _, set := range sets {
			for where, b := range set.docs {
				docs[where] = b
			}
		}
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

// TestPushOfSeveralCommitsChecksEveryVersion (devco:S-9): a push of
// several commits that change one row file carries each version of it. A
// key put in by the newest commit (made with --no-verify) is refused,
// though an older commit of the same push changed the file too (its clean
// version used to replace the newer one, so the push went out); a key one
// commit adds and a later one removes is refused as well, since the push
// still sends it.
func TestPushOfSeveralCommitsChecksEveryVersion(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ctx := context.Background()
	origin := filepath.Join(t.TempDir(), "origin.git")
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "remote", "add", "origin", origin)
	const row = ".lore/data/memories/mem_01a1dd5c74427dec80871af648c3aaef.json"
	base := commitFile(t, dir, row, `{"body":"retry the flaky job once"}`+"\n", "lore: memory")
	gitT(t, dir, "push", "-q", "--no-verify", "origin", "main")
	gitT(t, dir, "fetch", "-q", "origin")
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, dir, row, `{"body":"retry the flaky job twice"}`+"\n", "tidy memory")
	keyTip := commitFile(t, dir, row, `{"body":"retry the flaky job twice, prod key `+fakeKey+`"}`+"\n", "tidy memory again")
	sets, err := pushDocs(ctx, repo, "origin", []pushUpdate{{"refs/heads/main", keyTip, "refs/heads/main", base}})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkPushDocs(ctx, leakStub, sets, nil, nil); !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), row) {
		t.Fatalf("a key in the newest of two versions of %s went through: %v", row, err)
	}
	cleanTip := commitFile(t, dir, row, `{"body":"retry the flaky job twice"}`+"\n", "drop the key")
	sets, err = pushDocs(ctx, repo, "origin", []pushUpdate{{"refs/heads/main", cleanTip, "refs/heads/main", base}})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkPushDocs(ctx, leakStub, sets, nil, nil); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("a key an earlier commit of the push carries went through: %v", err)
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

// TestPushLeakChecksOnlyAddedLines (devco:D-728): the pre-push F16 check
// reads only the lines the pushed commits add. A file pushed long ago
// holds text the check refuses; a push of a commit that adds an unrelated
// line goes out, and a push of a commit that adds such a line again is
// refused, the finding on the added line (the old one blanked).
func TestPushLeakChecksOnlyAddedLines(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ctx := context.Background()
	origin := filepath.Join(t.TempDir(), "origin.git")
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "remote", "add", "origin", origin)
	old := "# Notes\n\nold " + fakeKey + "\n"
	base := commitFile(t, dir, "CLAUDE.md", old, "add notes")
	gitT(t, dir, "push", "-q", "--no-verify", "origin", "main")
	gitT(t, dir, "fetch", "-q", "origin")
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var seen string
	check := func(ctx context.Context, where string, docs map[string][]byte) error {
		narrowed := wire.AddedLinesOnly(ctx, docs)
		seen = string(narrowed["CLAUDE.md"])
		return leakStub(ctx, where, narrowed)
	}
	push := func(tip string) error {
		u := []pushUpdate{{"refs/heads/main", tip, "refs/heads/main", base}}
		sets, err := pushDocs(ctx, repo, "origin", u)
		if err != nil {
			t.Fatal(err)
		}
		return checkPushDocs(ctx, check, sets, pushAddedLines(ctx, repo, "origin", u), nil)
	}
	tip := commitFile(t, dir, "CLAUDE.md", old+"\nRun make test first.\n", "document the build")
	if err := push(tip); err != nil {
		t.Fatalf("a push adding an unrelated line was refused for a line pushed long ago: %v", err)
	}
	tip = commitFile(t, dir, "CLAUDE.md", old+"\nRun make test first.\nnew "+fakeKey+"\n", "repeat it")
	if err := push(tip); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("a pushed line the check refuses went out: %v", err)
	}
	if want := "\n\n\n\n\nnew " + fakeKey + "\n"; seen != want {
		t.Fatalf("the check read %q, want only the added line %q", seen, want)
	}
}
