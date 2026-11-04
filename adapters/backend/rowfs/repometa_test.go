package rowfs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// newRepoID mints a rep_ id for a test.
func newRepoID(t *testing.T) string {
	t.Helper()
	id, err := ids.New[ids.Repo]()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

// TestRepoMetaKeepsOtherKeys pins that the identity write keeps every key
// it does not own (project_id, _v, links, a newer lore's keys) and that a
// repeated write leaves the bytes alone.
func TestRepoMetaKeepsOtherKeys(t *testing.T) {
	top := t.TempDir()
	dir := filepath.Join(top, LoreDir, DataDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := WriteRepoMeta(top, RepoMeta{RepoID: newRepoID(t)}); err == nil {
		t.Fatalf("no _meta.json: writing an identity must refuse")
	}
	full := filepath.Join(dir, MetaFileName)
	if err := os.WriteFile(full, []byte(`{"_v":1,"future":{"x":1},"project_id":"prj_x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := RepoMeta{RepoID: newRepoID(t), URLs: []string{"github.com/a/b"}}
	if err := WriteRepoMeta(top, m); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Clean(full))
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"_v", "future", "project_id", MetaKeyRepoID, MetaKeyURLs} {
		if _, ok := doc[k]; !ok {
			t.Fatalf("key %s lost: %s", k, b)
		}
	}
	got, exists, err := ReadRepoMeta(top)
	if err != nil || !exists || got.RepoID != m.RepoID || !slices.Equal(got.URLs, m.URLs) {
		t.Fatalf("read back %+v %v %v", got, exists, err)
	}
	st, _ := os.Stat(full)
	if err := WriteRepoMeta(top, m); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(full)
	if !st.ModTime().Equal(st2.ModTime()) {
		t.Fatalf("an unchanged identity must not rewrite the file")
	}
	if err := os.WriteFile(full, []byte(`{"repo_id":"rep_bad"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadRepoMeta(top); err == nil {
		t.Fatalf("an invalid repo_id must be an error, never 'no identity'")
	}
}

// TestResolveRepoIdentity pins Q5: a fork merge keeps ours (either way
// round), a race keeps the older id with the other as an alias (the same
// on every clone), and the same id unions the addresses.
func TestResolveRepoIdentity(t *testing.T) {
	up := RepoMeta{RepoID: newRepoID(t), URLs: []string{"github.com/agency/x"}}
	fork := RepoMeta{RepoID: newRepoID(t), URLs: []string{"github.com/tomas/x"}, ForkedFrom: up.RepoID}
	if got := ResolveRepoIdentity(up, fork); got.RepoID != up.RepoID || got.ForkedFrom != "" || !slices.Equal(got.URLs, up.URLs) {
		t.Fatalf("fork merged into upstream must keep upstream (ours): %+v", got)
	}
	if got := ResolveRepoIdentity(fork, up); got.RepoID != fork.RepoID || got.ForkedFrom != up.RepoID {
		t.Fatalf("upstream merged into the fork must keep the fork (ours): %+v", got)
	}
	a := RepoMeta{RepoID: newRepoID(t), URLs: []string{"github.com/agency/x"}}
	b := RepoMeta{RepoID: newRepoID(t), URLs: []string{"git.agency.example/mirror/x"}}
	older, newer := a, b
	if b.RepoID < a.RepoID {
		older, newer = b, a
	}
	for _, got := range []RepoMeta{ResolveRepoIdentity(a, b), ResolveRepoIdentity(b, a)} {
		if got.RepoID != older.RepoID || !slices.Equal(got.Aliases, []string{newer.RepoID}) || len(got.URLs) != 2 {
			t.Fatalf("race: older id wins, other aliased: %+v", got)
		}
	}
	same := ResolveRepoIdentity(up, RepoMeta{RepoID: up.RepoID, URLs: []string{"github.com/agency/y"}})
	if !slices.Equal(same.URLs, []string{"github.com/agency/x", "github.com/agency/y"}) {
		t.Fatalf("same id: urls union, ours first: %+v", same)
	}
}

// TestFolderMergeKeepsOurRepoID pins the Q5 rule inside MergeFolderFile:
// the generic "larger value" never picks a repo id.
func TestFolderMergeKeepsOurRepoID(t *testing.T) {
	up, forkID := newRepoID(t), newRepoID(t)
	ours := []byte(`{"_v":2,"project_id":"prj_x","repo_id":"` + up + `","urls":["github.com/agency/x"]}`)
	theirs := []byte(`{"_v":2,"forked_from":"` + up + `","project_id":"prj_x","repo_id":"` + forkID + `","urls":["github.com/tomas/x"]}`)
	base := []byte(`{"_v":2,"project_id":"prj_x","repo_id":"` + up + `","urls":["github.com/agency/x"]}`)
	out, err := MergeFolderFile("p/.lore/data/_meta.json", base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, up) || strings.Contains(s, forkID) || strings.Contains(s, "forked_from") {
		t.Fatalf("merge must keep ours: %s", s)
	}
}
