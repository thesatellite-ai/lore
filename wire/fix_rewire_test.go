package wire

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// TestFixMigrateRewiresInTheSameRun (devco N-5): the host's hooks ran while
// the project was on the lore 0.1.17 format and kept lore's rules in the
// clone's .git/info files. The lore fix run whose migrate step raised the
// format asks the host for the tracked wiring right after that step
// (HostWiring.Rewire), and names what it wrote in a "git wiring" step, so a
// second lore fix has nothing left to write. A run that migrates nothing
// does not ask.
func TestFixMigrateRewiresInTheSameRun(t *testing.T) {
	t.Parallel()
	a, repo := repoDBApp(t, `{"_v": 1, "project_id": "`+v17ProjA+`"}`+"\n")
	var asked []string
	hw := &HostWiring{Rewire: func(_ context.Context, root string) []string {
		asked = append(asked, root)
		return []string{".gitignore", ".gitattributes"}
	}}
	ctx := WithHostWiring(opsdoctor.WithMigrate(background()), hw)
	steps := a.FixSteps(ctx, false)
	if len(asked) != 1 || asked[0] != repo {
		t.Fatalf("Rewire asked for %v, want once for %s", asked, repo)
	}
	i := slices.IndexFunc(steps, func(s opsdoctor.FixStep) bool { return s.Step == opsdoctor.StepGitWiring })
	if i < 0 || !steps[i].Changed || !strings.Contains(steps[i].Next, "git add .gitignore .gitattributes") {
		t.Fatalf("no git wiring step naming the files: %+v", steps)
	}

	asked = nil
	ctx = WithHostWiring(opsdoctor.WithMigrate(background()), &HostWiring{Rewire: hw.Rewire})
	for _, s := range a.FixSteps(ctx, false) {
		if s.Step == opsdoctor.StepGitWiring {
			t.Fatalf("a second run that wrote nothing has a git wiring step: %+v", s)
		}
	}
	if len(asked) != 0 {
		t.Fatalf("a run that migrated nothing asked for the wiring again: %v", asked)
	}
}

// TestFixOldFilesUnreadableFails (devco N-5): a lore.db this lore cannot
// read keeps the files as a failed step with the plain cause and the next
// step, never "--" and an error code.
func TestFixOldFilesUnreadableFails(t *testing.T) {
	t.Parallel()
	ctx := background()
	a, repo := repoDBApp(t, `{"_v": 1, "project_id": "`+v17ProjA+`"}`+"\n")
	if steps := a.FixSteps(opsdoctor.WithMigrate(ctx), false); len(steps) == 0 || steps[0].Failed {
		t.Fatalf("the migration failed: %+v", steps)
	}
	db := filepath.Join(repo, ".lore", "lore.db")
	for _, p := range []string{db, db + "-wal", db + "-shm"} {
		_ = os.Remove(p) // the fixture's db and any WAL: replaced by an unreadable file below
	}
	if err := os.WriteFile(db, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := a.fixOldFiles(ctx, repo, false)
	if !st.Failed || strings.Contains(st.Result, "[E_") || !strings.Contains(st.Result, "lore fix --migrate") {
		t.Fatalf("unreadable lore.db: %+v", st)
	}
}

// TestFixMigrateStampsRepoID (devco N-15): lore fix --migrate leaves the
// repo id in _meta.json, as lore init does, so the private render folder
// is named by the repo id in a migrated and an inited repo alike.
func TestFixMigrateStampsRepoID(t *testing.T) {
	t.Parallel()
	_, repo := repoDBApp(t, `{"_v": 1, "project_id": "`+v17ProjA+`"}`+"\n")
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	own := func(context.Context) (bool, error) { return false, nil } // the team's own repo, as a setup's repos pattern says
	a, err := New(Options{RepoDir: repo, Home: t.TempDir(), Actor: actor, ModeB: V017ModeB{}, RepoIsClient: own})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() }) // test teardown
	gitT(t, repo, "-c", "user.name=Ada Ray", "-c", "user.email=ada@example.test", "commit", "-q", "--allow-empty", "-m", "first")
	if steps := a.FixSteps(opsdoctor.WithMigrate(background()), false); len(steps) == 0 || steps[0].Failed || !steps[0].Changed {
		t.Fatalf("the migration did not run: %+v", steps)
	}
	b, rerr := os.ReadFile(filepath.Join(repo, ".lore", "data", "_meta.json"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(b), `"repo_id": "rep_`) {
		t.Fatalf("a migrated project has no repo id after lore fix --migrate:\n%s", b)
	}
}
