package orgcfg

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// TestReadRemotesLocalKey pins devco D-706: a path remote (absolute,
// file:// or relative to the work tree) gets its _local/<path> key in
// LocalRemotes and never enters the hosted remotes, so links, project
// ids and addresses are unchanged; Gather matches an own: pattern naming
// the key, and with none the repo resolves as before.
func TestReadRemotesLocalKey(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ctx := context.Background()
	dir := gitRepo(t, "/srv/git/acme/tools.git", "")
	if _, err := gitRun(ctx, "-C", dir, "remote", "add", "mirror", "file:///srv/git/acme/tools.git"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitRun(ctx, "-C", dir, "remote", "add", "sibling", "../kit.git"); err != nil {
		t.Fatal(err)
	}
	norm, local, notes := readRemotes(ctx, dir)
	top, _ := workTreeTop(ctx, dir)
	want := []string{"_local" + filepath.ToSlash(filepath.Join(filepath.Dir(top), "kit")), "_local/srv/git/acme/tools"}
	if len(norm) != 0 || !reflect.DeepEqual(local, want) || len(notes) != 3 {
		t.Fatalf("readRemotes = %v, %v, notes %v; want no hosted, %v, three notes", norm, local, notes, want)
	}
	if got, _ := Remotes(ctx, dir); len(got) != 0 {
		t.Fatalf("Remotes = %v, want none (a path is no hosted remote)", got)
	}

	home := t.TempDir()
	write := func(own string) {
		writeFile(t, ConfigPath(home), "orgs:\n  - name: acme\n    identity: {key: human:a@acme.com}\n    stores:\n      - url: https://github.com/acme/notes\n        org_folder: org\n    own: [\""+own+"\"]\n")
	}
	setup := orgresolve.SetupHint{Name: "acme-admin", Org: "acme", Why: "test"}
	write("_local/srv/git/acme/*")
	s, err := Gather(ctx, In{Dir: dir, Home: home, Setup: setup})
	if err != nil {
		t.Fatal(err)
	}
	if s.Result.Role != orgresolve.RoleOwn || len(s.Remotes) != 0 {
		t.Fatalf("own: _local pattern: role %v remotes %v (%s)", s.Result.Role, s.Remotes, s.Result.MatchedBy)
	}
	write("github.com/acme-co/*")
	s, err = Gather(ctx, In{Dir: dir, Home: home, Setup: setup})
	if err != nil {
		t.Fatal(err)
	}
	if s.Result.Role != orgresolve.RoleClient || s.Result.Step != orgresolve.StepSetup {
		t.Fatalf("no pattern naming the path: role %v step %v, want client by the setup as before", s.Result.Role, s.Result.Step)
	}
}
