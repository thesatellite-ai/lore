package wire

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// dryRunShape is a dryRunCase whose op needs a world the plain repo of
// TestDryRunFailsLikeTheWrite lacks (devco:D-938, H-199): a clock, a born
// branch, alias repo ids, a second project, a second store, an org
// roster. open builds it and returns the app, the folders whose store
// files and git refs a dry run must leave byte-identical, and {{id}}.
type dryRunShape struct {
	op        string
	open      func(t *testing.T) (a *App, roots []string, id string)
	bad, good string // good "" = the shape refuses every input
}

// dryRunShapes are the shaped cases of dryRunChecked ops (each also had
// its write made check-first by D-938, so the bad input is refused before
// any row is written, with and without --dry-run).
var dryRunShapes = []dryRunShape{
	{op: "task.stale", open: shapeStale, bad: `{"days":-1,"close":true}`, good: `{"days":30,"close":true}`},
	{op: "trash.add", open: shapeBornBranch, bad: `{"born_branch":"feat/x","kind":["bogus"]}`, good: `{"born_branch":"feat/x"}`},
	{op: "repo.merge_ids", open: shapeAliasIDs, good: `{}`},
	{op: "sync.fix_projects", open: shapeTwoProjects, bad: `{"keep":"{{id}}"}`, good: `{}`},
	{op: "member.add", open: shapeOrgRoster, bad: `{"email":"pat@acme.example","name":"Pat","type":"boss"}`, good: `{"email":"pat@acme.example","name":"Pat"}`},
	// D-938: the dry run opened the roster read-only, so it passed where
	// the write is refused (no org store to keep people in)
	{op: "member.add", open: shapeNoOrg, bad: `{"email":"pat@acme.example","name":"Pat"}`},
	{op: "row.move", open: shapeTwoStores, bad: `{"from":"repo","to":"nowhere/x"}`, good: `{"from":"repo","to":"me"}`},
}

// TestDryRunFailsLikeTheWriteShapes is TestDryRunFailsLikeTheWrite for
// the ops whose world is shaped (H-193 left them without a case). Not
// parallel: a shape may set the environment.
func TestDryRunFailsLikeTheWriteShapes(t *testing.T) {
	for _, c := range dryRunShapes {
		t.Run(c.op, func(t *testing.T) {
			if _, ok := dryRunChecked[c.op]; !ok {
				t.Fatalf("%s has a case but is not in dryRunChecked", c.op)
			}
			a, roots, id := c.open(t)
			checkDryRunLikeWrite(t, a, roots, id, c.op, c.bad, c.good)
		})
	}
}

// checkDryRunLikeWrite is the guard of one case: the bad input fails with
// the same code with and without --dry-run, the good one's dry run leaves
// every root's store files and git refs byte-identical, its write passes.
func checkDryRunLikeWrite(t *testing.T, a *App, roots []string, id, opName, bad, good string) {
	t.Helper()
	in := func(s string, dry bool) string {
		s = strings.ReplaceAll(s, "{{id}}", id)
		if dry {
			s = strings.TrimSuffix(s, "}")
			if s != "{" {
				s += ","
			}
			s += `"dry_run":true}`
		}
		return s
	}
	if bad != "" {
		_, real := run(t, a, opName, in(bad, false), true)
		_, dry := run(t, a, opName, in(bad, true), false)
		if real == nil || dry == nil || errs.CodeOf(real) != errs.CodeOf(dry) {
			t.Errorf("bad input %s: write %v, dry run %v; want the same error code", bad, real, dry)
		} else {
			t.Logf("bad input %s: %s with and without --dry-run", bad, errs.CodeOf(real))
		}
	}
	sum := func() map[string]string {
		all := map[string]string{}
		for _, r := range roots {
			for k, v := range rootSum(t, r) {
				all[r+k] = v
			}
		}
		return all
	}
	if good == "" { // a shape where every input is refused
		return
	}
	before := sum()
	out, err := run(t, a, opName, in(good, true), false)
	if err != nil {
		t.Fatalf("dry run of good input %s: %v", good, err)
	}
	if after := sum(); !maps2Equal(before, after) {
		t.Errorf("dry run of %s wrote: %s (output %s)", good, treeDiff(before, after), out)
	}
	if _, err := run(t, a, opName, in(good, false), true); err != nil {
		t.Errorf("the write of good input %s: %v", good, err)
	}
	// the write changes what the dry run left alone, so the byte check
	// above proves something
	if after := sum(); maps2Equal(before, after) {
		t.Errorf("the write of good input %s changed no store file: the shape has nothing to write", good)
	}
}

// rootSum is treeSum of a repo (it has .lore), else the sha256 of every
// file of the folder (a folder store holds its rows at its top).
func rootSum(t *testing.T, root string) map[string]string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, ".lore")); err == nil {
		return treeSum(t, root)
	}
	sums := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(filepath.Clean(p))
		if rerr != nil {
			return rerr
		}
		h := sha256.Sum256(b)
		sums[strings.TrimPrefix(p, root)] = hex.EncodeToString(h[:])
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return sums
}

// shapeGit runs git in dir as a test person (LORE_LAYER off: a hook must
// not run a lore of the box).
func shapeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test Person", "-c", "user.email=test@example.test"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LORE_LAYER=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// shapePlain is the guard's plain repo with its project row; home is
// shared by every app the shape opens on it.
func shapePlain(t *testing.T, wall func() time.Time) (repo, home string, a *App) {
	t.Helper()
	repo, home = gitRepo(t), t.TempDir()
	a = openWith(t, repo, Options{Home: home, Wall: wall})
	withProject(t, a)
	return repo, home, a
}

// shapeStale: two tasks last changed 100 days ago (a clock), so task
// stale --days 30 --close cancels both.
func shapeStale(t *testing.T) (*App, []string, string) {
	t.Helper()
	old := time.Now().Add(-100 * 24 * time.Hour)
	repo, home, past := shapePlain(t, func() time.Time { return old })
	for _, title := range []string{"Old draft", "Old review"} {
		if _, err := run(t, past, "task.add", `{"title":"`+title+`"}`, true); err != nil {
			t.Fatal(err)
		}
	}
	_ = past.Close()
	return openWith(t, repo, Options{Home: home}), []string{repo}, ""
}

// shapeBornBranch: a memory and a task made on feat/x (a branch with an
// upstream, so the rows keep its name, PROV8), then back on main.
func shapeBornBranch(t *testing.T) (*App, []string, string) {
	t.Helper()
	t.Setenv("LORE_LAYER", "off")
	repo, home, a := shapePlain(t, nil)
	shapeGit(t, repo, "commit", "-q", "--allow-empty", "-m", "start")
	shapeGit(t, repo, "switch", "-q", "-c", "feat/x")
	remote := t.TempDir()
	shapeGit(t, remote, "init", "-q", "--bare")
	shapeGit(t, repo, "remote", "add", "origin", remote)
	shapeGit(t, repo, "push", "-q", "-u", "origin", "feat/x")
	_ = a.Close()
	onX := openWith(t, repo, Options{Home: home}) // HEAD is read once per open
	for op, in := range map[string]string{"memory.add": `{"body":"made on x"}`, "task.add": `{"title":"task made on x"}`} {
		if _, err := run(t, onX, op, in, true); err != nil {
			t.Fatal(err)
		}
	}
	_ = onX.Close()
	shapeGit(t, repo, "switch", "-q", "-")
	return openWith(t, repo, Options{Home: home}), []string{repo}, ""
}

// shapeAliasIDs: a memory stamped with the repo's first id, then a new
// repo id whose alias is the first (a re-identified checkout, PROV 11.9).
func shapeAliasIDs(t *testing.T) (*App, []string, string) {
	t.Helper()
	repo, home, a := shapePlain(t, nil)
	shapeGit(t, repo, "remote", "add", "origin", "git@github.com:agency/acmehub_backend.git")
	pid, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SeedRepoMeta(background(), pid); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	a = openWith(t, repo, Options{Home: home})
	if _, err := a.CheckRepoAddresses(background()); err != nil {
		t.Fatal(err)
	}
	m, _, err := rowfs.ReadRepoMeta(repo)
	if err != nil {
		t.Fatal(err)
	}
	loser := m.RepoID
	if loser == "" {
		t.Fatal("no repo id minted")
	}
	if _, err := run(t, a, "memory.add", `{"body":"stamped with the first repo id"}`, true); err != nil {
		t.Fatal(err)
	}
	winner, err := ids.New[ids.Repo]()
	if err != nil {
		t.Fatal(err)
	}
	m.RepoID, m.Aliases = winner.String(), []string{loser}
	if err := rowfs.WriteRepoMeta(repo, m); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	return openWith(t, repo, Options{Home: home}), []string{repo}, ""
}

// shapeTwoProjects: a second project row next to the repo's own (a
// merge of two checkouts); {{id}} is an id no project has, which the
// write refuses as --keep.
func shapeTwoProjects(t *testing.T) (*App, []string, string) {
	t.Helper()
	repo := gitRepo(t)
	// the row verbs of a binary whose sync host lacks them run on the
	// core (syncRows), as in cmd/lore
	a := openWith(t, repo, Options{Sync: stubSync{err: errs.New(errs.Unsupported, "not in this host")}})
	withProject(t, a)
	name, err := value.TextOf("second")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Create(background(), store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": name}); err != nil {
		t.Fatal(err)
	}
	ghost, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	return a, []string{repo}, ghost.String()
}

// shapeTwoStores: two memories in the repo and the person's own store
// (me, a folder store) as the second store a bulk move writes; its
// folder is a root too.
func shapeTwoStores(t *testing.T) (*App, []string, string) {
	t.Helper()
	repo, home, a := shapePlain(t, nil)
	if _, err := a.InitStore(background(), opsstores.InitIn{Name: opsstores.PersonalName, RepoOptional: true}); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	a = openWith(t, repo, Options{Home: home}) // the next command sees me attached
	for _, body := range []string{"first note to move", "second note to move"} {
		if _, err := run(t, a, "memory.add", `{"body":"`+body+`"}`, true); err != nil {
			t.Fatal(err)
		}
	}
	return a, []string{repo, personalFolderDir(home)}, ""
}

// orgAdminHome is a lore home whose default setup is an admin setup of
// org acme (member add runs from an admin setup).
func orgAdminHome(t *testing.T) string {
	t.Helper()
	return writeSetupsConfig(t, "orgs:\n  - name: acme\n    identity: {key: \""+testEmail+"\"}\nsetups:\n"+
		"  - name: acme-admin\n    kind: admin\n    org: acme\n    personal_store: none\n    default: true\n")
}

// shapeOrgRoster: a repo of org acme whose roster is the org's folder
// store acme/team (Options.Org + OrgStores); its folder is a root too.
func shapeOrgRoster(t *testing.T) (*App, []string, string) {
	t.Helper()
	dir := t.TempDir()
	sid, err := ids.Parse[ids.Store]("sto_0e45506e64357c94a87e70ef1c6bbb01")
	if err != nil {
		t.Fatal(err)
	}
	if err := folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	url := fileuri.URL(dir, "")
	repo := gitRepo(t)
	a := openWith(t, repo, Options{
		Home: orgAdminHome(t),
		Org: func(context.Context) (*identity.Org, error) {
			return &identity.Org{Name: "acme", Identity: identity.Entry{Key: testEmail}}, nil
		},
		OrgStores: func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: "acme/team", URL: url}}, nil
		},
		RepoIsClient: func(context.Context) (bool, error) { return false, nil },
	})
	withProject(t, a)
	return a, []string{repo, dir}, ""
}

// shapeNoOrg: an admin setup in a repo of no org: the roster write is
// refused (people are kept in an org store), so is its dry run.
func shapeNoOrg(t *testing.T) (*App, []string, string) {
	t.Helper()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{Home: orgAdminHome(t), RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, a)
	return a, []string{repo}, ""
}
