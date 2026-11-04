package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// purgedTag is a purge fixture with one tag in kb, purged, and the live
// file bytes it had (what an old clone would put back).
type purgedTag struct {
	f      *fixture
	dir    string
	ref    Ref
	fields map[string]value.Value
	live   []byte
	tomb   row.Row // the tombstone Purge wrote
}

func newPurgedTag(t *testing.T) purgedTag {
	t.Helper()
	ctx := context.Background()
	f, dir := purgeFixture(t)
	prj, err := f.eng.Create(ctx, kbStore, schema.TableProjects, map[string]value.Value{"name": txt("kb-project")})
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{"project_id": mustRef(t, prj.Row.ID), "name": txt("deploy")}
	tag, err := f.eng.Create(ctx, kbStore, schema.TableTags, fields)
	if err != nil {
		t.Fatal(err)
	}
	live := mustFile(t, rowPath(dir, schema.TableTags, tag.Row.ID))
	ref := Ref{Store: kbStore, Table: schema.TableTags, ID: tag.Row.ID}
	w, err := f.eng.Purge(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	return purgedTag{f: f, dir: dir, ref: ref, fields: fields, live: live, tomb: w.Row}
}

// resurrect puts the live file back over the tombstone (an old clone).
func (p purgedTag) resurrect(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(rowPath(p.dir, p.ref.Table, p.ref.ID), p.live, 0o600); err != nil {
		t.Fatal(err)
	}
}

// dropTombstone removes the tombstone file (the id stays in _purged.json).
func (p purgedTag) dropTombstone(t *testing.T) {
	t.Helper()
	if err := os.Remove(rowPath(p.dir, p.ref.Table, p.ref.ID)); err != nil {
		t.Fatal(err)
	}
}

// TestPurgedErrorShape (DELETION_DESIGN 2.7): every write refused for a
// purged id is E_PURGED (refused, exit 23), and the message names who
// purged it and when from the tombstone, like "mem_… was purged by mira
// on 2026-08-19; purged rows never come back (DQ4)". With no tombstone
// to read (its file gone, or an old clone's live file over it) the
// message names the id alone, never a guessed actor or date.
func TestPurgedErrorShape(t *testing.T) {
	ctx := context.Background()
	day := func(p purgedTag) string { return p.tomb.UpdatedAt.UTC().Format(purgedDay) }
	cases := []struct {
		name    string
		prepare func(t *testing.T, p purgedTag)
		write   func(p purgedTag) error
		who     bool // the message names actor and date
	}{
		{"update of the tombstone", nil, func(p purgedTag) error {
			_, err := p.f.eng.Update(ctx, p.ref, map[string]value.Value{"color": txt("red")})
			return err
		}, true},
		{"delete of the tombstone", nil, func(p purgedTag) error {
			_, err := p.f.eng.Delete(ctx, p.ref)
			return err
		}, true},
		{"create with the id over the tombstone", nil, func(p purgedTag) error {
			_, err := p.f.eng.CreateWithID(ctx, kbStore, p.ref.Table, p.ref.ID, p.fields)
			return err
		}, true},
		{"move of the tombstone out", nil, func(p purgedTag) error {
			_, err := p.f.eng.Move(ctx, p.ref, store.SourceRepo)
			return err
		}, true},
		{"move a copy into the store holding the tombstone", func(t *testing.T, p purgedTag) {
			copyToRepo(t, p)
		}, func(p purgedTag) error {
			_, err := p.f.eng.Move(ctx, Ref{Store: store.SourceRepo, Table: p.ref.Table, ID: p.ref.ID}, kbStore)
			return err
		}, true},
		{"create with the id, tombstone file gone", func(t *testing.T, p purgedTag) {
			p.dropTombstone(t)
		}, func(p purgedTag) error {
			_, err := p.f.eng.CreateWithID(ctx, kbStore, p.ref.Table, p.ref.ID, p.fields)
			return err
		}, false},
		{"update of a resurrected file", func(t *testing.T, p purgedTag) {
			p.resurrect(t)
		}, func(p purgedTag) error {
			_, err := p.f.eng.Update(ctx, p.ref, map[string]value.Value{"color": txt("red")})
			return err
		}, false},
		{"delete of a resurrected file", func(t *testing.T, p purgedTag) {
			p.resurrect(t)
		}, func(p purgedTag) error {
			_, err := p.f.eng.Delete(ctx, p.ref)
			return err
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPurgedTag(t)
			if c.prepare != nil {
				c.prepare(t, p)
			}
			err := c.write(p)
			if !errs.Has(err, errs.Purged) {
				t.Fatalf("got %v, want E_PURGED", err)
			}
			if d := errs.DefOf(errs.CodeOf(err)); d.Exit != errs.ExitRefused {
				t.Fatalf("exit %d, want %d", d.Exit, errs.ExitRefused)
			}
			msg := err.Error()
			if !strings.Contains(msg, p.ref.ID.String()+" was purged") || !strings.Contains(msg, "purged rows never come back (DQ4)") {
				t.Fatalf("message %q lacks the DELETION_DESIGN 2.7 shape", msg)
			}
			named := strings.Contains(msg, " by "+testActor.String()) && strings.Contains(msg, " on "+day(p))
			if named != c.who {
				t.Fatalf("message %q: names who/when = %v, want %v", msg, named, c.who)
			}
		})
	}
}

// copyToRepo writes the purged tag's live row into the repo store.
func copyToRepo(t *testing.T, p purgedTag) {
	t.Helper()
	r, err := row.Decode(p.live)
	if err != nil {
		t.Fatal(err)
	}
	sr, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.f.repo.Open(store.OpenOptions{}).Write(context.Background(), []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
}

// TestPurgedHiddenFromReads (DQ4 "purged rows never come back"): when an
// old clone puts a purged id's live file back, no engine read returns it:
// Get gives a tombstone with no content, List and FindByKey skip it, a
// Refresh hands the cache a tombstone (so a cache rebuilt from the files,
// and with it search, sql and render, never holds the row), and
// Resurrected names the file for `lore doctor`. Purging the id again
// writes the tombstone over the file, after which nothing is resurrected.
func TestPurgedHiddenFromReads(t *testing.T) {
	ctx := context.Background()
	p := newPurgedTag(t)
	p.resurrect(t)
	eng := p.f.eng

	_, got, err := eng.Get(ctx, p.ref)
	if err != nil || !got.Deleted || len(got.Fields) != 0 {
		t.Fatalf("Get of a resurrected file: %+v %v, want a tombstone with no fields", got, err)
	}
	_, got, err = eng.Get(ctx, Ref{ID: p.ref.ID}) // no store, no table named
	if err != nil || !got.Deleted {
		t.Fatalf("Get by bare id: %+v %v", got, err)
	}
	list, err := eng.List(ctx, kbStore, schema.TableTags)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list {
		if r.ID == p.ref.ID {
			t.Fatalf("List returned the purged %s", r.ID)
		}
	}
	found, err := FindByKey(ctx, eng, schema.TableTags, map[string]string{"name": "deploy"})
	if err != nil || len(found) != 0 {
		t.Fatalf("FindByKey: %+v %v, want nothing", found, err)
	}

	// a cache rebuilt from the files gets the id as a tombstone
	if rerr := p.f.cache.Rebuild(ctx, kbStore); rerr != nil {
		t.Fatal(rerr)
	}
	if _, rerr := eng.Refresh(ctx, kbStore); rerr != nil {
		t.Fatal(rerr)
	}
	p.f.cache.mu.Lock()
	cached, ok := p.f.cache.rows[kbStore][store.Key{Table: p.ref.Table, ID: p.ref.ID}]
	p.f.cache.mu.Unlock()
	if !ok || !cached.Deleted || strings.Contains(string(cached.Doc), "deploy") {
		t.Fatalf("cache after refresh: %+v (present %v), want a content-free tombstone", cached, ok)
	}

	res, err := eng.Resurrected(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Store != kbStore || res[0].Table != p.ref.Table || res[0].ID != p.ref.ID {
		t.Fatalf("Resurrected = %+v, want the one tag in kb", res)
	}

	// purge again: the tombstone goes back over the file
	w, err := eng.Purge(ctx, p.ref)
	if err != nil || !w.Row.Deleted {
		t.Fatalf("re-purge: %+v %v", w.Row, err)
	}
	if raw := string(mustFile(t, rowPath(p.dir, p.ref.Table, p.ref.ID))); strings.Contains(raw, "deploy") || !strings.Contains(raw, `"_deleted"`) {
		t.Fatalf("file after re-purge still holds the row: %s", raw)
	}
	if res, err = eng.Resurrected(ctx); err != nil || len(res) != 0 {
		t.Fatalf("Resurrected after re-purge = %+v %v", res, err)
	}
	if _, err := eng.Purge(ctx, p.ref); err != nil {
		t.Fatalf("purge a third time: %v", err)
	}
}

// TestResurrectedNoneWithoutPurges: stores with no purged ids (and the
// memstore repo, which keeps no list) report nothing, and a live row that
// was never purged is never hidden.
func TestResurrectedNoneWithoutPurges(t *testing.T) {
	ctx := context.Background()
	f, dir := purgeFixture(t)
	w, err := f.eng.Create(ctx, kbStore, schema.TableProjects, map[string]value.Value{"name": txt("p")})
	if err != nil {
		t.Fatal(err)
	}
	if res, rerr := f.eng.Resurrected(ctx); rerr != nil || len(res) != 0 {
		t.Fatalf("Resurrected = %+v %v", res, rerr)
	}
	if _, got, gerr := f.eng.Get(ctx, Ref{Store: kbStore, ID: w.Row.ID}); gerr != nil || got.Deleted {
		t.Fatalf("a never-purged row read as %+v %v", got, gerr)
	}
	if _, serr := os.Stat(filepath.Clean(rowPath(dir, schema.TableProjects, w.Row.ID))); serr != nil {
		t.Fatal(serr)
	}
}
