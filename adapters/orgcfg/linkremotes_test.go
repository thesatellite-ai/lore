package orgcfg

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

// TestCloneLinkRemotes (owner item 26): the remotes every link row of a
// store clone names, read at its default branch tip with no network,
// normalised and once each; a clone with no commit on that branch has
// none.
func TestCloneLinkRemotes(t *testing.T) {
	ctx := context.Background()
	dir := gitRepo(t, "", "")
	if got, err := CloneLinkRemotes(ctx, dir); err != nil || got != nil {
		t.Fatalf("empty clone: %v %v", got, err)
	}
	run := func(args ...string) {
		t.Helper()
		if _, err := gitRun(ctx, append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	writeFile(t, filepath.Join(dir, "app", "_meta.json"), `{"links":[{"project_id":"prj_a","remote":"https://github.com/acme/app.git"},{"project_id":"prj_b","remote":"git@github.com:acme/api.git"}]}`)
	writeFile(t, filepath.Join(dir, "web", "_meta.json"), `{"links":[{"project_id":"prj_c","remote":"github.com/acme/app"}]}`)
	writeFile(t, filepath.Join(dir, "bad", "_meta.json"), `not json`)
	run("checkout", "-q", "-b", "main")
	run("add", ".")
	run("commit", "-q", "-m", "links")
	got, err := CloneLinkRemotes(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"github.com/acme/api", "github.com/acme/app"}; !slices.Equal(got, want) {
		t.Fatalf("link remotes %v, want %v", got, want)
	}
}
