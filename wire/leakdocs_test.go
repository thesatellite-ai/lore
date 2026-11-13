package wire

// Port of saas/cmd/cli leak_core_index_test.go (B11) to the F16 index of
// this module (leakdocs.go): which store classes feed the index, what a
// row contributes, which attached stores are read, and the fail-closed
// refusal when a store the org link names is not attached.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLeakIndexesClass pins which store classes feed the leak index for a
// client repo and for any other checked repo (v0.1.17's split).
func TestLeakIndexesClass(t *testing.T) {
	t.Parallel()
	cases := []struct {
		class  direction.Class
		client bool
		want   bool
	}{
		{direction.Personal, true, true},
		{direction.Personal, false, true},
		{direction.Internal, true, true},
		{direction.Internal, false, false},
		{direction.Client, true, false},
		{direction.Client, false, false},
		{direction.Class(""), true, false},
		{direction.Class("repo"), true, false},
		{direction.Class("bogus"), true, false},
	}
	for _, tc := range cases {
		if got := leakIndexesClass(tc.class, tc.client); got != tc.want {
			t.Errorf("leakIndexesClass(%q, client=%v) = %v, want %v", tc.class, tc.client, got, tc.want)
		}
	}
}

// leakTestRow builds a live row with text fields.
func leakTestRow(t *testing.T, id string, fields map[string]string) row.Row {
	t.Helper()
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	r := row.Row{ID: aid, Fields: map[string]value.Value{}}
	for k, v := range fields {
		tv, err := value.TextOf(v)
		if err != nil {
			t.Fatal(err)
		}
		r.Fields[k] = tv
	}
	return r
}

// TestLeakIndexRows: prose fields become index texts, id-holding fields do
// not, tombstones are skipped, and the indexed text is what the text rule
// then matches in a client file.
func TestLeakIndexRows(t *testing.T) {
	t.Parallel()
	const (
		memID   = "mem_01a1a67a33b7777587cca0cfc9e5d82b"
		taskID  = "tsk_01a1a67a33b7777587cca0cfc9e5d82c"
		deadID  = "rul_01a1a67a33b7777587cca0cfc9e5d82d"
		clientT = "tsk_01a1a67a33b7777587cca0cfc9e5d8ff"
		secret  = "client is slow to pay so always send the estimate before the call"
	)
	dead := leakTestRow(t, deadID, map[string]string{"body": "a deleted rule body with many words in it here"})
	dead.Deleted = true
	rows := []row.Row{
		leakTestRow(t, memID, map[string]string{"body": secret}),
		leakTestRow(t, taskID, map[string]string{"title": "chase the invoice", "description": "ünïcödé notes about the client call", "parent_task_id": clientT}),
		leakTestRow(t, "rul_01a1a67a33b7777587cca0cfc9e5d8aa", nil), // no prose: id only
		dead,
	}
	got := leakIndexRows(rows, schema.TableMemories, "agency", leakStoreLabelPrefix+"agency", nil)
	if len(got) != 3 {
		t.Fatalf("want 3 live rows, got %d: %+v", len(got), got)
	}
	if got[0].ID != memID || got[0].Source != "agency" || got[0].Label != "store agency" || len(got[0].Texts) != 1 || got[0].Texts[0] != secret {
		t.Fatalf("memory row: %+v", got[0])
	}
	for _, s := range got[1].Texts {
		if strings.Contains(s, clientT) {
			t.Fatalf("an id-holding field must not be indexed text: %+v", got[1])
		}
	}
	if len(got[1].Texts) != 2 || len(got[2].Texts) != 0 {
		t.Fatalf("texts: %+v / %+v", got[1].Texts, got[2].Texts)
	}
	for _, r := range got {
		if r.ID == deadID {
			t.Fatal("a tombstone is not indexed")
		}
	}

	// End to end through leakcheck: the internal text in a client file
	// blocks, the client task id an internal row points at does not.
	idx := leakcheck.NewIndex(got, nil)
	hit := leakcheck.Blocking(leakcheck.Check([]leakcheck.Item{{Where: "README.md", Content: []byte("Note: " + secret + ".")}}, idx, leakcheck.Options{}))
	if len(hit) == 0 {
		t.Fatal("internal text copied into a client file must block")
	}
	clean := leakcheck.Blocking(leakcheck.Check([]leakcheck.Item{{Where: "README.md", Content: []byte("see " + clientT)}}, idx, leakcheck.Options{}))
	if len(clean) != 0 {
		t.Fatalf("a client id named by an internal row is not internal: %v", clean)
	}
}

// Store ids of the leak world's folder stores.
const (
	leakAgencyStore   = "sto_01a1a5aca7f57659be7320c5f4b70a01"
	leakHandbookStore = "sto_01a1a5aca7f57659be7320c5f4b70a02"
)

// leakWorld opens a repo with an internal store (agency), a shareable one
// (handbook) and the personal store, each holding one memory whose body
// names its store; client decides the repo's class.
func leakWorld(t *testing.T, client bool) *App {
	t.Helper()
	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores:\n"+
		"  - name: agency\n    url: "+folderStoreAt(t, leakAgencyStore, rowfs.VisibilityInternal)+"\n"+
		"  - name: handbook\n    url: "+folderStoreAt(t, leakHandbookStore, rowfs.VisibilityShareable)+"\n")
	home := t.TempDir()
	if _, err := run(t, openWith(t, repo, Options{Home: home}), opsstores.OpInit, `{"name":"me","url":"`+fileURL(t.TempDir())+`"}`, false); err != nil {
		t.Fatal(err)
	}
	a := openWith(t, repo, Options{Home: home, RepoIsClient: func(context.Context) (bool, error) { return client, nil }})
	for _, s := range []store.Source{store.SourceRepo, "agency", "handbook", "me"} {
		name, err := value.TextOf("prj-" + string(s))
		if err != nil {
			t.Fatal(err)
		}
		p, err := a.Create(background(), s, schema.TableProjects, map[string]value.Value{"name": name})
		if err != nil {
			t.Fatalf("create in %s: %v", s, err)
		}
		f := map[string]value.Value{"project_id": nil, "kind": nil, "body": nil, "source_kind": nil}
		for k, v := range map[string]string{"project_id": p.Row.ID.String(), "kind": schema.MemoryKindCore, "body": leakNote(s), "source_kind": "manual"} {
			if f[k], err = value.TextOf(v); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := a.Create(background(), s, schema.TableMemories, f); err != nil {
			t.Fatalf("memory in %s: %v", s, err)
		}
	}
	return a
}

// leakNote is the memory body the leak world writes in store s.
func leakNote(s store.Source) string { return "a note that lives in " + string(s) + " only" }

// TestLeakRowsStoreChoice: which attached stores feed the index, for a
// client repo and for any other checked repo (CFG6: a shareable store is
// never internal; repo and client stores are what the client reads).
func TestLeakRowsStoreChoice(t *testing.T) {
	// Heavy on SQLite: its own process keeps it off this process's
	// allocator lock (see package isolate).
	if isolate.Run(t) {
		return
	}
	cases := []struct {
		client bool
		want   []string
	}{
		{true, []string{"agency", "me"}},
		{false, []string{"me"}},
	}
	for _, tc := range cases {
		a := leakWorld(t, tc.client)
		got, err := a.leakRows(background(), tc.client, nil)
		if err != nil {
			t.Fatalf("client=%v: %v", tc.client, err)
		}
		var srcs []string
		for _, r := range got {
			if r.Table != string(schema.TableMemories) {
				continue
			}
			if r.Label != leakStoreLabelPrefix+r.Source || !slices.Equal(r.Texts, []string{leakNote(store.Source(r.Source))}) {
				t.Errorf("client=%v: index row %+v", tc.client, r)
			}
			srcs = append(srcs, r.Source)
		}
		slices.Sort(srcs)
		if !slices.Equal(srcs, tc.want) {
			t.Errorf("client=%v: indexed stores %v, want %v", tc.client, srcs, tc.want)
		}
	}
}

// TestLeakRowsFailsClosed: an org store the link names that is not
// attached (unreachable at open) refuses the check with E_LEAK_BLOCKED
// "cannot check" and no partial index, for a client repo and an own repo
// alike; with every named store attached the check runs.
func TestLeakRowsFailsClosed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		client   bool
		expected []store.Source
		refused  bool
	}{
		{"linked org store not attached", true, []store.Source{"agency", "northwind"}, true},
		{"linked org store not attached, own repo", false, []store.Source{"northwind"}, true},
		{"every linked org store attached", true, []store.Source{"agency", "me"}, false},
		{"no linked store", false, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := leakWorld(t, tc.client)
			got, err := a.leakRows(background(), tc.client, tc.expected)
			if !tc.refused {
				if err != nil {
					t.Fatalf("want no refusal, got %v", err)
				}
				return
			}
			if !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), "cannot check") {
				t.Fatalf("want E_LEAK_BLOCKED cannot check, got %v", err)
			}
			if got != nil {
				t.Fatalf("a refusal returns no partial index: %v", got)
			}
		})
	}
}

// faultyLeakReader is the real App with injected read failures: the store
// list, one store's class, or one store's table listing.
type faultyLeakReader struct {
	*App
	identsErr error
	classErr  map[store.Source]error
	listErr   map[store.Source]error
}

func (f faultyLeakReader) StoreIdentities(ctx context.Context) ([]StoreIdentity, error) {
	if f.identsErr != nil {
		return nil, f.identsErr
	}
	return f.App.StoreIdentities(ctx)
}

func (f faultyLeakReader) StoreClass(ctx context.Context, name store.Source) (direction.Class, error) {
	if err := f.classErr[name]; err != nil {
		return "", err
	}
	return f.App.StoreClass(ctx, name)
}

func (f faultyLeakReader) List(ctx context.Context, src store.Source, t row.Table) ([]row.Row, error) {
	if err := f.listErr[src]; err != nil && t == schema.TableRules {
		return nil, err
	}
	return f.App.List(ctx, src, t)
}

// TestLeakRowsFailsClosedOnReadFailures: a store or table of the index that
// cannot be read refuses the check (E_LEAK_BLOCKED "cannot check", no
// partial index) instead of counting as empty; a failure in a store the
// index does not read changes nothing.
func TestLeakRowsFailsClosedOnReadFailures(t *testing.T) {
	// Heavy on SQLite: its own process keeps it off this process's
	// allocator lock (see package isolate).
	if isolate.Run(t) {
		return
	}
	boom := errors.New("disk on fire")
	cases := []struct {
		name    string
		client  bool
		fault   faultyLeakReader
		refused bool
	}{
		{"store list fails", true, faultyLeakReader{identsErr: boom}, true},
		{"store list fails, own repo", false, faultyLeakReader{identsErr: boom}, true},
		{"internal class unreadable", true, faultyLeakReader{classErr: map[store.Source]error{"agency": boom}}, true},
		{"personal class unreadable, own repo", false, faultyLeakReader{classErr: map[store.Source]error{"me": boom}}, true},
		{"internal table unlistable", true, faultyLeakReader{listErr: map[store.Source]error{"agency": boom}}, true},
		{"personal table unlistable, own repo", false, faultyLeakReader{listErr: map[store.Source]error{"me": boom}}, true},
		{"internal unlistable, own repo (not indexed)", false, faultyLeakReader{listErr: map[store.Source]error{"agency": boom}}, false},
		{"repo store unlistable, client repo (not indexed)", true, faultyLeakReader{listErr: map[store.Source]error{store.SourceRepo: boom}}, false},
		{"shareable store's class unreadable (skipped first)", true, faultyLeakReader{classErr: map[store.Source]error{"handbook": boom}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.fault
			f.App = leakWorld(t, tc.client)
			got, err := leakRowsFrom(background(), f, tc.client, nil)
			if !tc.refused {
				if err != nil {
					t.Fatalf("want no refusal, got %v", err)
				}
				return
			}
			if !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), "cannot check") || !errors.Is(err, boom) {
				t.Fatalf("want E_LEAK_BLOCKED cannot check carrying the cause, got %v", err)
			}
			// B-15: the refusal names the cause and the way out
			var coded *errs.Error
			if !errors.As(err, &coded) || !strings.Contains(coded.Message, boom.Error()) || coded.Hint != leakCannotCheckHint {
				t.Fatalf("want the cause in the message and the sync hint, got %+v", coded)
			}
			if got != nil {
				t.Fatalf("a refusal returns no partial index: %v", got)
			}
		})
	}
}

// TestLeakRowsNamesANeverReadStore (B-15): an org store the check needs
// that this machine never read refuses naming that store, with the hint
// that says how to get past it (it printed the bare "cannot check" line).
func TestLeakRowsNamesANeverReadStore(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	f := faultyLeakReader{App: leakWorld(t, true)}
	_, err := leakRowsFrom(background(), f, true, []store.Source{"client-store"})
	var coded *errs.Error
	if !errors.As(err, &coded) || coded.Code != errs.LeakBlocked {
		t.Fatalf("want E_LEAK_BLOCKED, got %v", err)
	}
	if !strings.Contains(coded.Message, "store client-store") || !strings.Contains(coded.Message, "never read on this machine") || coded.Hint != leakCannotCheckHint {
		t.Fatalf("refusal %q hint %q: want the store named and the sync hint", coded.Message, coded.Hint)
	}
}
