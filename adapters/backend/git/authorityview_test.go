package git

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/store"
)

// TestOrgFileChecksReadHeadView (bug hunt H-277, devco:D-1015): at the
// HEAD a replay was saved at, the org file check (OrgFileChanges), who is
// an admin at HEAD, at an org file change and at its parent (AdminAt), and
// the held list (Held) are answered from the small views saved beside the
// replay, with the whole replay's answers, and decode no saved replay.
// Before, each decoded the whole replay, whose stamps, accepted versions
// and admin sets grow with every row version of the history: ~115 MB of
// lore doctor's memory at 100k commits. A commit the view does not hold
// still gets the whole replay's answer.
func TestOrgFileChecksReadHeadView(t *testing.T) {
	o := newOrgTeam(t)
	orgFile := rowfs.FolderOrg + "/" + orgFileName
	o.h.commit(o.boss.email, map[string]string{orgFile: "lore_org: 1\nstores: []\n"})
	o.h.commit(o.max.email, map[string]string{orgFile: "lore_org: 1\nstores: []\nown: [\"github.com/acme-co/*\"]\n"})
	home := filepath.Join(t.TempDir(), "home")
	dir := filepath.Join(home, StoresDir, "host", "store")
	gitT(t, filepath.Dir(o.h.dir), "clone", "--quiet", o.h.remote, dir)
	ctx := context.Background()
	b, err := OpenClone(ctx, o.h.remote, dir, rowfs.FolderOrg, store.OpenOptions{Home: home}, WithMerger(newestMerger{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	want := authorityNow(t, b)
	h := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD"))
	if len(want.orgFile) < 2 {
		t.Fatalf("the fixture has %d org file change(s), want 2", len(want.orgFile))
	}
	wantChanges, ok := OrgFileChanges(ctx, dir, h)
	if !ok {
		t.Fatal("OrgFileChanges: no replay")
	}
	member := want.orgFile[len(want.orgFile)-1]
	type ask struct{ rev, email string }
	asks := []ask{{h, o.boss.email}, {h, o.max.email}, {member.parent, o.max.email}, {member.parent, o.boss.email}, {member.commit, o.boss.email}}
	wantAdmin := map[ask][2]bool{}
	for _, a := range asks {
		admin, decided := want.adminAt(a.rev, a.email)
		wantAdmin[a] = [2]bool{admin, decided}
	}
	if w := wantAdmin[ask{member.parent, o.max.email}]; w[0] || !w[1] {
		t.Fatalf("the member is an admin at their own change's parent (%v): the fixture lost its point", w)
	}

	newProcess(b)
	n := replayFileDecodes.Load()
	got, ok := OrgFileChanges(ctx, dir, h)
	if !ok || len(got) != len(wantChanges) {
		t.Fatalf("OrgFileChanges from the head view: %d change(s) ok=%v, want %d", len(got), ok, len(wantChanges))
	}
	for i := range got {
		if got[i] != wantChanges[i] {
			t.Errorf("change %d: %+v, want %+v", i, got[i], wantChanges[i])
		}
	}
	for _, a := range asks {
		admin, decided := AdminAt(ctx, dir, a.rev, a.email)
		if w := wantAdmin[a]; admin != w[0] || decided != w[1] {
			t.Errorf("AdminAt(%s, %s) = %v, %v; want %v, %v", a.rev[:7], a.email, admin, decided, w[0], w[1])
		}
	}
	held, err := b.Held(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != len(want.held) {
		t.Errorf("Held: %d, want %d", len(held), len(want.held))
	}
	if d := replayFileDecodes.Load() - n; d != 0 {
		t.Errorf("the org file check, AdminAt and Held decoded the whole saved replay %d time(s) at its HEAD, want 0", d)
	}

	// a commit the view does not hold: the whole replay answers
	first := strings.TrimSpace(gitT(t, dir, "rev-list", "--max-parents=0", "HEAD"))
	wantFirst, wantFirstDecided := want.adminAt(first, o.boss.email)
	if admin, decided := AdminAt(ctx, dir, first, o.boss.email); admin != wantFirst || decided != wantFirstDecided {
		t.Errorf("AdminAt(root commit) = %v, %v; want %v, %v", admin, decided, wantFirst, wantFirstDecided)
	}
}

// TestRosterHeadReplaysNoOrgHistory (bug hunt H-277, devco:D-1015): a
// client store checked against its org's roster asks for the org clone's
// HEAD (to tell whether its kept replay is still current) without
// replaying the org's history or decoding its saved replay. Before, the
// question went through the roster source, which replays the org store's
// whole history when this process holds none.
func TestRosterHeadReplaysNoOrgHistory(t *testing.T) {
	o := newOrgTeam(t)
	org := orgFolderClone(t, o.h.remote)
	org.marker.OrgFolder = rowfs.FolderOrg // the hand store's marker names none
	client := newHandStore(t)
	client.commit(o.boss.email, map[string]string{"widgets/projects/" + o.project.String() + ".json": rowJSON("projects", o.project, map[string]string{"name": `"widgets"`})})
	ctx := context.Background()
	b, err := OpenClone(ctx, client.remote, filepath.Join(t.TempDir(), "clones", "host", "client"), "widgets", store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	b.SetRosterSource(org)
	want, err := org.head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newProcess(org)
	replays, decodes := replayCount(), replayFileDecodes.Load()
	if got := b.rosterHead(ctx); got != want {
		t.Fatalf("rosterHead = %q, want the org clone's HEAD %q", got, want)
	}
	if r, d := replayCount()-replays, replayFileDecodes.Load()-decodes; r != 0 || d != 0 {
		t.Errorf("asking the org's HEAD replayed %d commit(s) and decoded %d saved replay(s), want none", r, d)
	}
}
