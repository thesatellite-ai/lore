package gitremote_test

// This test lives beside the forwarding shell until merge3 moves into the
// root module (P3-PORT-LIVE): it needs merge3, which the root gitremote
// tests cannot import from saas.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	gt "github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
	"saas/pkg/aicoder/merge3"
)

func push(t *testing.T, r gitremote.Remote, req gitremote.PushRequest) gitremote.PushResult {
	t.Helper()
	res, err := r.Push(context.Background(), req)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	return res
}

func status(t *testing.T, res gitremote.PushResult, dst string) gitremote.Status {
	t.Helper()
	r, ok := res.Ref(dst)
	if !ok {
		t.Fatalf("no verdict for %s in %+v", dst, res)
	}
	return r.Status
}

const live = "refs/lore/live"

// loadFile reads one row file of rev as a merge3.File: absent, a row, or a
// tombstone (`_live.deleted: true`, the marker F2 writes).
func loadFile(t *testing.T, r *gitref.Repo, rev, path string) merge3.File {
	t.Helper()
	b, ok, err := r.ReadFile(context.Background(), rev, path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return merge3.Absent()
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if lv, _ := doc["_live"].(map[string]any); lv != nil && lv["deleted"] == true {
		return merge3.Tombstone(doc)
	}
	return merge3.Row(doc)
}

// TestDeleteVersusEditAcrossClones is the F4 E2E shape on the primitives:
// clone A tombstones a live row and pushes; clone B edits it, loses the
// push race, fetches, merges file-level (modify beats delete) and pushes a
// merge commit. The row survives with B's edit; nothing is half a row.
func TestDeleteVersusEditAcrossClones(t *testing.T) {
	ctx := context.Background()
	const row = "_root/tasks/tsk_02.json"
	w := gt.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	a.Write(live, row, `{"id":"tsk_02","status":"todo","title":"fix login"}`)
	a.MustPush(live)
	b.SyncRef(live)

	a.Write(live, row, `{"_live":{"deleted":true},"id":"tsk_02"}`)
	a.MustPush(live)
	bEdit := b.Write(live, row, `{"id":"tsk_02","status":"todo","title":"fix login timeout"}`)
	res := push(t, b.Remote, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: bEdit, Dst: live}}})
	if status(t, res, live) != gitremote.StatusNonFastForward {
		t.Fatalf("B's push = %+v", res)
	}

	b.Fetch()
	theirs := b.Ref("refs/lore-remote/origin/live")
	baseRev, ok, err := b.Repo.MergeBase(ctx, bEdit, theirs)
	if err != nil || !ok {
		t.Fatalf("merge base = %v %v", ok, err)
	}
	fr, err := merge3.MergeFile(loadFile(t, b.Repo, baseRev, row), loadFile(t, b.Repo, bEdit, row), loadFile(t, b.Repo, theirs, row), merge3.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if fr.Decision != merge3.DecisionModifyBeatsDelete || fr.DeletedBy != merge3.SideTheirs || fr.File.Kind != merge3.FileRow {
		t.Fatalf("merge = %v deleted by %v kind %v", fr.Decision, fr.DeletedBy, fr.File.Kind)
	}
	out, err := json.Marshal(fr.File.Doc)
	if err != nil {
		t.Fatal(err)
	}
	m, err := b.Repo.MakeCommit(ctx, gitref.CommitRequest{Base: bEdit, MergeParents: []string{theirs}, Message: "lore: live merge", Signature: gt.Sig,
		Changes: gitref.Changes{Put: map[string][]byte{row: out}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Repo.UpdateRef(ctx, live, m.OID, bEdit); err != nil {
		t.Fatal(err)
	}
	b.MustPush(live)

	final := loadFile(t, w.Origin, live, row)
	if final.Kind != merge3.FileRow || final.Doc["title"] != "fix login timeout" {
		t.Fatalf("origin row = %v %v", final.Kind, final.Doc)
	}
	if n := gt.Git(t, w.OriginDir, "rev-list", "--count", live); n != "4" {
		t.Fatalf("live history has %s commits, want 4 (seed, tombstone, edit, merge)", n)
	}
}
