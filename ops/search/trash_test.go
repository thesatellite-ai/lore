package search_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/ops/rule"
	"github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// hitIDs runs a search and returns its hits' ids and trash marks in order.
func hitIDs(t *testing.T, h *opstest.H, input string) ([]string, []bool) {
	t.Helper()
	var out search.RunOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(search.OpRun, input))), &out); err != nil {
		t.Fatal(err)
	}
	var idList []string
	var trash []bool
	for _, x := range out.Data.Hits {
		idList = append(idList, x.ID)
		trash = append(trash, x.InTrash)
	}
	return idList, trash
}

// TestSearchIncludeDeleted (later 25, DELETION 2.9, FA-META-4): rows in
// the trash never show in a plain search; with --include-deleted they are
// listed after every live hit and marked, the scope filters still apply
// (--tables, --master-only for a repo-scoped row, archived rows only with
// --include-archived), and a trashed resource is never listed (pinned as
// an outcome: wire searchTrashed drops the resources target, and the
// resources table has no full-text fields either, so the target drop
// alone cannot be seen failing here).
func TestSearchIncludeDeleted(t *testing.T) {
	h := opstest.New(t)
	// a repo identity, so a memory can be repo-scoped (as ops/scope tests)
	data := filepath.Join(h.Repo, rowfs.LoreDir, rowfs.DataDir)
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, rowfs.MetaFileName), []byte(`{"_v":1,"project_id":"`+h.Project+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.App.EnsureRepoIdentity(context.Background()); err != nil {
		t.Fatal(err)
	}
	live := h.ID(h.MustYes(memory.OpAdd, `{"body":"zebra crossing live"}`))
	gone := h.ID(h.MustYes(memory.OpAdd, `{"body":"zebra crossing gone"}`))
	repoGone := h.ID(h.MustYes(memory.OpAdd, `{"body":"zebra crossing repo","scope":"repo"}`))
	archGone := h.ID(h.MustYes(memory.OpAdd, `{"body":"zebra crossing archived"}`))
	h.Must(memory.OpArchive, `{"id":"`+archGone+`"}`)
	liveRule := h.ID(h.MustYes(rule.OpAdd, `{"body":"zebra crossing rule","severity":"should"}`))
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	res := h.ID(h.MustYes(settings.OpResAdd, `{"kind":"host","name":"zebra"}`))
	for _, id := range []string{gone, repoGone, archGone} {
		h.MustYes(memory.OpDelete, `{"id":"`+id+`"}`)
	}
	h.MustYes(settings.OpResDelete, `{"id":"`+res+`"}`)

	cases := []struct {
		name      string
		in        string
		live      []string // in any order, all before the trashed ones
		trashed   []string // in any order
		forbidden []string
	}{
		{"plain", `{"query":"zebra"}`, []string{live, liveRule}, nil, []string{gone, repoGone, archGone, res}},
		{"include deleted", `{"query":"zebra","include_deleted":true}`, []string{live, liveRule}, []string{gone, repoGone}, []string{archGone, res}},
		{"archived too", `{"query":"zebra","include_deleted":true,"include_archived":true}`, []string{live, liveRule}, []string{gone, repoGone, archGone}, []string{res}},
		{"tables", `{"query":"zebra","include_deleted":true,"tables":["rule"]}`, []string{liveRule}, nil, []string{live, gone, repoGone}},
		{"master only", `{"query":"zebra","include_deleted":true,"master_only":true}`, []string{live, liveRule}, []string{gone}, []string{repoGone}},
		{"words must all match", `{"query":"zebra gone","include_deleted":true}`, nil, []string{gone}, []string{repoGone, archGone}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, marks := hitIDs(t, h, c.in)
			pos := map[string]int{}
			for i, id := range got {
				pos[id] = i
			}
			lastLive := -1
			for _, id := range c.live {
				i, ok := pos[id]
				if !ok || marks[i] {
					t.Fatalf("live %s missing or marked: %v %v", id, got, marks)
				}
				lastLive = max(lastLive, i)
			}
			for _, id := range c.trashed {
				i, ok := pos[id]
				if !ok || !marks[i] || i < lastLive {
					t.Fatalf("trashed %s missing, unmarked or before a live hit: %v %v", id, got, marks)
				}
			}
			for _, id := range c.forbidden {
				if _, ok := pos[id]; ok {
					t.Fatalf("%s listed: %v", id, got)
				}
			}
		})
	}
}
