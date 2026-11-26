package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestSquashGroupsByAuthor (devco:D-1000, store growth item 4): a push
// squashes the waiting commits into one batch commit per run of one
// author, in order, each keeping its author and committer (never the
// pushing process's identity); a run of one commit goes out as it was.
// The authority replay judges each commit by its author, so authors are
// never mixed. Break-verified: the old squash made ONE commit signed by
// the pushing handle for all six writes.
func TestSquashGroupsByAuthor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	alice := Signature{Name: "Alice", Email: "alice@acme.invalid"}
	bob := Signature{Name: "Bob", Email: "bob@acme.invalid"}
	remote := newRemote(t, true)
	base := filepath.Join(t.TempDir(), "clones", "host", "store")
	a := openPushAt(t, remote, base, WithMerger(newestMerger{}), WithSigner(alice))
	b := openPushAt(t, remote, base, WithMerger(newestMerger{}), WithSigner(bob))
	g := newGen(t)
	before := remoteCount(t, remote)
	for _, w := range []*Backend{a, a, a, b, b, a} {
		write(t, w, store.Change{Row: g.row(newID(t), "row", hlc.HLC{})})
	}
	single := gitT(t, a.Dir(), "log", "-1", "--format=%H|%s", "HEAD")
	if _, err := a.SyncWith(ctx, SyncOptions{Squash: true}); err != nil {
		t.Fatal(err)
	}
	if got := remoteCount(t, remote); got != before+3 {
		t.Fatalf("remote has %d new commits, want 3 (alice x3, bob x2, alice x1):\n%s", got-before, gitT(t, remote, "log", "--format=%s|%an", testBranch))
	}
	want := []string{
		"lore: batch 3 rows (memories 3)|Alice|Alice",
		"lore: batch 2 rows (memories 2)|Bob|Bob",
		strings.SplitN(single, "|", 2)[1] + "|Alice|Alice",
	}
	got := strings.Split(gitT(t, remote, "log", "--reverse", "--format=%s|%an|%cn", "-3", testBranch), "\n")
	if !slices.Equal(got, want) {
		t.Fatalf("pushed history:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if gitT(t, remote, "rev-parse", testBranch+"^{tree}") != gitT(t, a.Dir(), "rev-parse", "HEAD^{tree}") {
		t.Fatal("the pushed tree is not the clone's")
	}
	if n, _ := a.Unpushed(ctx); n != 0 {
		t.Fatalf("Unpushed after the push = %d", n)
	}
}

// TestSquashKeepsRosterCommitsAlone (devco:D-1000): a commit that changes
// a roster row (the authority replay judges each later commit against the
// roster as it left it) keeps a commit of its own with its own message;
// lore's row writes on each side of it are squashed. Break-verified: the
// old squash folded the roster change into one batch with the rows.
func TestSquashKeepsRosterCommitsAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	before := remoteCount(t, remote)
	write(t, a, store.Change{Row: g.row(newID(t), "one", hlc.HLC{})})
	write(t, a, store.Change{Row: g.row(newID(t), "two", hlc.HLC{})})
	write(t, a, store.Change{Row: rosterRow(t, g)})
	roster := gitT(t, a.Dir(), "log", "-1", "--format=%s", "HEAD")
	write(t, a, store.Change{Row: g.row(newID(t), "three", hlc.HLC{})})
	write(t, a, store.Change{Row: g.row(newID(t), "four", hlc.HLC{})})
	if _, err := a.SyncWith(ctx, SyncOptions{Squash: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{"lore: batch 2 rows (memories 2)", roster, "lore: batch 2 rows (memories 2)"}
	got := strings.Split(gitT(t, remote, "log", "--reverse", "--format=%s", "-3", testBranch), "\n")
	if n := remoteCount(t, remote); n != before+3 || !slices.Equal(got, want) {
		t.Fatalf("pushed %d commits:\n%s\nwant:\n%s", n-before, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if gitT(t, remote, "rev-parse", testBranch+"^{tree}") != gitT(t, a.Dir(), "rev-parse", "HEAD^{tree}") {
		t.Fatal("the pushed tree is not the clone's")
	}
}

// TestSquashSoloPaths: which changed paths keep a commit of its own.
func TestSquashSoloPaths(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		paths []string
		want  bool
	}{
		{[]string{"notes/memories/mem_1.json", "notes/tasks/tsk_1.json"}, false},
		{[]string{"org/" + schema.TableActorPeriods + "/apr_1.json"}, true},
		{[]string{"org/" + schema.TableProjectMembers + "/pm_1.json"}, true},
		{[]string{"org/" + schema.TableTeamContacts + "/tc_1.json"}, true},
		{[]string{"org/" + schema.TableActors + "/act_1.json", "org/memories/mem_1.json"}, true}, // a first write that registers its writer
		{[]string{"lore-org.yaml"}, true},
		{[]string{"lore-store.yaml"}, true},
		{[]string{"org/lore-org.yaml.migrated"}, true},
		{[]string{"README.md", ".0-needs-lore-0.1.18-or-newer", "_store/memories/mem_1.json"}, false},
		{nil, false},
	} {
		if got := squashSolo(tc.paths); got != tc.want {
			t.Errorf("squashSolo(%v) = %v, want %v", tc.paths, got, tc.want)
		}
	}
}

// rosterRow is a new stint row (a roster table the replay keeps; a new
// stint gives a right, so it is never folded).
func rosterRow(t *testing.T, g *gen) store.Row {
	t.Helper()
	id, err := ids.New[ids.ActorPeriod]()
	if err != nil {
		t.Fatal(err)
	}
	v, err := value.TextOf("Carol")
	if err != nil {
		t.Fatal(err)
	}
	stamp := g.clock.Now(hlc.HLC{})
	r, err := store.FromRow(row.Row{Table: row.Table(schema.TableActorPeriods), ID: id.Any(), HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor, Fields: map[string]value.Value{"name": v}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestSquashKeepsTheHeldSet (devco:D-1000, spec rule 1: security does not
// weaken): the same local history — a member's rows, his self-promotion
// and an offboard of a teammate made by hand, an admin's row in between,
// rows by a member who left — judged as it is and judged after a
// squashing sync rewrote it gives the same held changes (row, table,
// author, reason), and the push stays held. The squashed history has
// fewer commits.
func TestSquashKeepsTheHeldSet(t *testing.T) {
	t.Parallel()
	boss, mallory, carol, kim := newPerson(t, "boss@example.test"), newPerson(t, "mallory@example.test"), newPerson(t, "carol@example.test"), newPerson(t, "kim@example.test")
	stranger := newPerson(t, "stranger@example.test")
	m1, m2, m3, m4, m5 := mustID[ids.Memory](t), mustID[ids.Memory](t), mustID[ids.Memory](t), mustID[ids.Memory](t), mustID[ids.Memory](t)
	mem := func(id ids.AnyID, body string) map[string]string {
		return map[string]string{orgPath("memories", id): rowJSON("memories", id, map[string]string{"body": `"` + body + `"`})}
	}
	run := func(squash bool) ([]string, int) {
		h := newHandStore(t)
		h.commit(boss.email, map[string]string{
			orgPath("actors", boss.actor): boss.actorRow(), orgPath("actor_periods", boss.period): boss.periodRow("admin", ""),
			orgPath("actors", mallory.actor): mallory.actorRow(), orgPath("actor_periods", mallory.period): mallory.periodRow("member", ""),
			orgPath("actors", carol.actor): carol.actorRow(), orgPath("actor_periods", carol.period): carol.periodRow("member", ""),
			orgPath("actors", kim.actor): kim.actorRow(), orgPath("actor_periods", kim.period): kim.periodRow("member", "2026-01-02"),
		})
		before := remoteCount(t, h.remote)
		a := orgFolderClone(t, h.remote)
		local := func(author string, files map[string]string) {
			t.Helper()
			for p, body := range files {
				full := filepath.Join(a.Dir(), filepath.FromSlash(p))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				gitT(t, a.Dir(), "add", p)
			}
			gitT(t, a.Dir(), "-c", "user.name=x", "-c", "user.email="+author, "commit", "--quiet", "--no-verify", "--author", "x <"+author+">", "-m", "edit")
		}
		local(mallory.email, mem(m1, "one"))
		local(mallory.email, mem(m2, "two"))
		local(mallory.email, map[string]string{orgPath("actor_periods", mallory.period): mallory.periodRow("admin", "")})
		local(mallory.email, mem(m3, "three"))
		local(mallory.email, map[string]string{orgPath("actors", stranger.actor): stranger.actorRow()}) // another person's new actor row: held, and folded
		local(mallory.email, map[string]string{orgPath("actor_periods", carol.period): carol.periodRow("member", "2026-02-01")})
		local(boss.email, mem(m4, "four"))
		local(kim.email, mem(m5, "after leaving"))
		local(kim.email, mem(m1, "an edit after leaving"))
		if squash {
			// the hold keeps the push back (D-694: lore never publishes a
			// change every reader holds back); the squash has moved the
			// branch already, so the clone's own replay judges the
			// squashed history
			rep, err := a.SyncWith(context.Background(), SyncOptions{Squash: true})
			if err != nil || rep.Pushed || rep.Held == "" {
				t.Fatalf("squashed sync = %+v, %v; want the push held", rep, err)
			}
		}
		if n := remoteCount(t, h.remote); n != before {
			t.Fatalf("the remote gained %d commits; a held change was published", n-before)
		}
		held, err := a.Held(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, hc := range held {
			out = append(out, strings.Join([]string{hc.ID, hc.Table, hc.Author, hc.Reason}, " | "))
		}
		slices.Sort(out)
		n, err := a.Unpushed(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return out, n
	}
	plain, plainN := run(false)
	squashed, squashedN := run(true)
	if len(plain) < 3 {
		t.Fatalf("fixture: the plain push holds %d changes, want the two stints and the leaver's rows:\n%s", len(plain), strings.Join(plain, "\n"))
	}
	if !slices.Equal(plain, squashed) {
		t.Fatalf("held set differs:\nplain:\n%s\nsquashed:\n%s", strings.Join(plain, "\n"), strings.Join(squashed, "\n"))
	}
	if plainN != 9 || squashedN >= plainN {
		t.Fatalf("%d local commits plain and %d squashed, want 9 and fewer", plainN, squashedN)
	}
}

// TestSquashKeepsTheReplayIncremental (devco:D-1000 with D-999): a push
// that squashes the waiting commits rewrites them, so the roster replay
// saved at the old HEAD no longer lies on the new history. The replay
// kept at the remote tip the squash builds on does: the push's own check
// and the next process replay only the batch commit, never the whole
// history, and the result is a full replay's. Break-verified: without the
// replay at the remote tip the push replayed every commit again.
func TestSquashKeepsTheReplayIncremental(t *testing.T) {
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	for range 6 {
		write(t, a, store.Change{Row: g.row(newID(t), "pushed", hlc.HLC{})})
	}
	syncOK(t, a)
	authorityNow(t, a) // level with the remote: kept as the replay at the remote tip too
	if _, err := os.Stat(a.authorityPath() + authorityBaseSuffix); err != nil {
		t.Fatalf("no replay kept at the remote tip: %v", err)
	}
	for range 3 {
		write(t, a, store.Change{Row: g.row(newID(t), "waiting", hlc.HLC{})})
	}
	authorityNow(t, a) // the saved replay moves on to the local HEAD
	before := replayCount()
	if _, err := a.SyncWith(ctx, SyncOptions{Squash: true}); err != nil {
		t.Fatal(err)
	}
	forgetAuthority(a)
	got := authorityNow(t, a)
	if n := replayCount() - before; n != 1 {
		t.Errorf("after a squashing push the replay ran over %d commit(s), want 1 (the batch commit)", n)
	}
	sameAuthority(t, got, fullReplay(t, a))
}

// TestSquashKeepsALayerBase (devco:D-1000): a branch layer names the store
// commit it was made on (Lore-Layer-Store-Base) and lands against it. A
// push never folds that commit or any before it: they go out as they are,
// and only the later ones are squashed, so the layer's base stays on the
// branch (the gate's BL stories: `lore branch land` failed with "the
// store file _meta.json changed on both sides" after the base was folded).
func TestSquashKeepsALayerBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	var made []string
	for range 4 {
		write(t, a, store.Change{Row: g.row(newID(t), "row", hlc.HLC{})})
		made = append(made, gitT(t, a.Dir(), "rev-parse", "HEAD"))
	}
	layer := gitT(t, a.Dir(), "commit-tree", emptyTree, "-m", "lore: notes of feat/a\n\n"+TrailerStoreBase+": "+made[1])
	gitT(t, a.Dir(), "update-ref", branchlayer.RefPrefix+"main/notes/feat-a", layer)
	before := remoteCount(t, remote)
	if _, err := a.SyncWith(ctx, SyncOptions{Squash: true}); err != nil {
		t.Fatal(err)
	}
	if n := remoteCount(t, remote) - before; n != 3 {
		t.Fatalf("the remote gained %d commits, want 3 (two kept up to the layer base, one batch):\n%s", n, gitT(t, remote, "log", "--format=%H %s", testBranch))
	}
	if _, err := exec.Command("git", "-C", remote, "merge-base", "--is-ancestor", made[1], testBranch).CombinedOutput(); err != nil {
		t.Fatal("the layer's base is not on the pushed branch: it was folded")
	}
	if subj := gitT(t, remote, "log", "-1", "--format=%s", testBranch); subj != "lore: batch 2 rows (memories 2)" {
		t.Fatalf("the commits after the base went out as %q, want one batch", subj)
	}
}
