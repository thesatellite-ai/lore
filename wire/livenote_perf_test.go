package wire

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/store"
)

// servingGets is a repo backend that serves the live ref (the cmd/lore
// live overlay) and counts Get calls.
type servingGets struct{ *countingGets }

// ServesLiveRef implements LiveOverlay.
func (servingGets) ServesLiveRef() bool { return true }

// currentLiveMemory is a current-format live memory as lore writes it:
// two-space indented, `_v` on its own top-level line.
func currentLiveMemory(id, project string) string {
	return "{\n  \"_hlc\": \"" + liveOld + "\",\n  \"_live\": {\n    \"hlc\": \"" + liveOld + "\"\n  },\n" +
		"  \"_table\": \"memories\",\n  \"_v\": 2,\n  \"body\": \"current\",\n  \"created_at\": \"2026-11-04T08:09:56Z\",\n" +
		"  \"id\": \"" + id + "\",\n  \"kind\": \"retrieved\",\n  \"project_id\": \"" + project + "\",\n" +
		"  \"source_kind\": \"manual\",\n  \"updated_at\": \"2026-11-04T08:09:56Z\"\n}\n"
}

// TestLiveNoteLooksUpNoCurrentRepoRow (devco:T-12, T-20): the live note of
// every command (whoami and setup list included) looked each of the repo's
// live rows up in the repo store and, when missing there, in every other
// attached store (one git process per lookup), though the live overlay
// serves current-format rows itself: 10-30 s per command at 5,000 open
// tasks. Only the rows older than the row format are looked up now.
func TestLiveNoteLooksUpNoCurrentRepoRow(t *testing.T) {
	t.Parallel()
	ctx := background()
	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores:\n  - name: agency\n    url: "+folderStoreAt(t, folderStore, rowfs.VisibilityInternal)+"\n")
	a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, a)
	project := projectIn(t, a, store.SourceRepo)
	files := map[string]string{livePath(liveOnlyMem): liveMemory(liveOnlyMem, project, "old live row", liveOld)}
	const current = 40
	for i := range current {
		id := fmt.Sprintf("mem_01a1a59b891c70b789144cd3e7%06x", 0x200+i)
		files[livePath(id)] = currentLiveMemory(id, project)
	}
	writeLiveRef(t, repo, liveRefLocal, files)
	st, err := a.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repoGets := &countingGets{Backend: st.byName[store.SourceRepo]}
	agencyGets := &countingGets{Backend: st.byName["agency"]}
	st.byName[store.SourceRepo], st.byName["agency"] = servingGets{repoGets}, agencyGets
	defer func() { st.byName[store.SourceRepo], st.byName["agency"] = repoGets.Backend, agencyGets.Backend }()
	if note := liveNote(ctx, st); strings.Contains(note, "memories") {
		t.Errorf("live note = %q: the overlay serves the repo's live rows, nothing waits for a migration", note)
	}
	if repoGets.gets > 1 || agencyGets.gets > 1 {
		t.Errorf("the live note looked up %d rows in the repo store and %d in agency, want at most 1 each (the old row; %d current rows the overlay serves)",
			repoGets.gets, agencyGets.gets, current)
	}
}

// TestLiveRowCurrent: a current-format live document is known from its
// bytes; anything else (old format, another layout, a nested `_v`) is
// left to the full decode.
func TestLiveRowCurrent(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"{\n  \"_table\": \"tasks\",\n  \"_v\": 2,\n  \"id\": \"x\"\n}": true,
		"{\n  \"_table\": \"tasks\",\n  \"_v\": 12\n}":                  true,
		"{\n  \"_table\": \"tasks\",\n  \"_v\": 1,\n  \"id\": \"x\"\n}": false,
		`{"_table":"tasks","_v":2,"id":"x"}`:                            false,
		"{\n  \"a\": {\n    \"_v\": 2\n  },\n  \"_v\": 1\n}":            false,
		"{\n  \"_v\": 2x\n}": false,
		"{\n  \"_table\": \"tasks\",\n  \"_v\": \"2\",\n  \"id\": \"x\"\n}": false,
	}
	for doc, want := range cases {
		if got := liveRowCurrent([]byte(doc)); got != want {
			t.Errorf("liveRowCurrent(%q) = %v, want %v", doc, got, want)
		}
	}
}

// TestRevBlobsManyPathsByBlobID (devco:T-12): reading many rows of one
// commit named each "<rev>:<path>", and git walked the tree once per path
// (a 5,000-row folder: 15 s per read). From revTreeMin paths on, the tree
// is listed once and read by blob id; the answer is the same: every blob
// path, nothing for a missing path or a folder.
func TestRevBlobsManyPathsByBlobID(t *testing.T) {
	t.Parallel()
	ctx := background()
	repo := gitRepo(t)
	files := map[string]string{}
	var paths []string
	for i := range revTreeMin + 8 {
		p := fmt.Sprintf("rows/r%03d.json", i)
		files[p] = fmt.Sprintf(`{"n":%d}`, i)
		paths = append(paths, p)
	}
	writeLiveRef(t, repo, "refs/lore/test", files)
	asked := append(append([]string{}, paths...), "rows/missing.json", "rows")
	got, err := revBlobs(ctx, repo, nil, "refs/lore/test", asked)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(paths) {
		t.Fatalf("revBlobs returned %d files, want %d (no missing path, no folder)", len(got), len(paths))
	}
	for _, p := range paths {
		if string(got[p]) != files[p] {
			t.Errorf("%s = %q, want %q", p, got[p], files[p])
		}
	}
}
