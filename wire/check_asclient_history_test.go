package wire

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestAsClientDataPrefix: the project's .lore/data folder relative to the
// git top: the top itself, a monorepo sub-folder at any depth, and a root
// outside the top (falls back to the top's folder).
func TestAsClientDataPrefix(t *testing.T) {
	t.Parallel()
	top := t.TempDir()
	cases := []struct{ root, want string }{
		{top, ".lore/data/"},
		{filepath.Join(top, "web"), "web/.lore/data/"},
		{filepath.Join(top, "svc", "api"), "svc/api/.lore/data/"},
		{filepath.Dir(top), ".lore/data/"},
	}
	for _, c := range cases {
		if got := asClientDataPrefix(top, c.root); got != c.want {
			t.Errorf("asClientDataPrefix(top, %s) = %q, want %q", c.root, got, c.want)
		}
	}
}

// TestResourceHistoryMonorepoPrefix (later 515): the history scan reads
// every resource blob ever committed under THIS project's folder (also one
// deleted since, on any branch), never another project's rows in the same
// repo; a repo with no commits has no history.
func TestResourceHistoryMonorepoPrefix(t *testing.T) {
	t.Parallel()
	top := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		gitIn(t, top, nil, append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(top, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	if got, err := resourceHistory(background(), top, "web/.lore/data/"); err != nil || len(got) != 0 {
		t.Fatalf("no commits: %v %v", got, err)
	}
	const (
		webOld   = "res_0e000000000000000000000000000001"
		webLive  = "res_0e000000000000000000000000000002"
		topOther = "res_0e000000000000000000000000000003"
		branchID = "res_0e000000000000000000000000000004"
	)
	res := func(id string) string { return `{"_v":2,"id":"` + id + `","name":"n","kind":"knd_x","fields":{}}` }
	write("web/.lore/data/resources/"+webOld+".json", res(webOld))
	write("web/.lore/data/resources/"+webLive+".json", res(webLive))
	write(".lore/data/resources/"+topOther+".json", res(topOther))
	git("add", "-A")
	git("commit", "-q", "--no-verify", "-m", "one")
	git("rm", "-q", "web/.lore/data/resources/"+webOld+".json")
	git("commit", "-q", "--no-verify", "-m", "two")
	git("checkout", "-q", "-b", "side")
	write("web/.lore/data/resources/"+branchID+".json", res(branchID))
	git("add", "-A")
	git("commit", "-q", "--no-verify", "-m", "side")
	git("checkout", "-q", "-")

	for _, c := range []struct {
		prefix string
		want   []string
	}{
		{"web/.lore/data/", []string{webOld, webLive, branchID}},
		{".lore/data/", []string{topOther}},
	} {
		rows, err := resourceHistory(background(), top, c.prefix)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range rows {
			if !slices.Contains(got, r.ID) {
				got = append(got, r.ID)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, c.want) {
			t.Errorf("prefix %s: %v, want %v", c.prefix, got, c.want)
		}
	}
}
