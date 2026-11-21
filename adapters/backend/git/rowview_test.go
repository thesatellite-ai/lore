package git

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// TestRowViewAnswersAsTheReplay: a row read in a new process at a HEAD
// the replay already reached reads the small row view saved with it, not
// the whole roster replay (devco:D-826: decoding a long store's ~2 MB
// replay was ~17 ms of every read that looks up one actor row), and gets
// exactly the replay's answer: the same holds and the same credited
// writer. At another HEAD the view is not used.
func TestRowViewAnswersAsTheReplay(t *testing.T) {
	o := newOrgTeam(t)
	spoof, roster := mustID[ids.Memory](t), mustID[ids.ActorPeriod](t)
	o.h.commit(o.max.email, map[string]string{orgPath("memories", spoof): stampedRow(spoof, o.boss.actor, "2026-01-02T00:00:01.000Z", "says the admin")})
	o.h.commit(o.max.email, map[string]string{orgPath("actor_periods", roster): o.max.periodRowAt("2026-01-03T00:00:00.000Z", "admin", "")})
	b := orgFolderClone(t, o.h.remote)
	ctx := context.Background()

	full := authorityNow(t, b)
	if len(full.held) == 0 || len(full.credited) == 0 {
		t.Fatalf("the fixture lost its point: %d held, %d credited", len(full.held), len(full.credited))
	}
	want, err := b.Get(ctx, row.Table("memories"), spoof)
	if err != nil {
		t.Fatal(err)
	}
	forgetAuthority(b)
	v := b.loadRowView(full.head, full.orgHead)
	if v == nil {
		t.Fatal("no row view saved with the replay")
	}
	if !reflect.DeepEqual(v.held, full.held) || !reflect.DeepEqual(v.trusted, full.trusted) || !reflect.DeepEqual(v.credited, full.credited) {
		t.Errorf("the row view differs from the replay:\n got  %+v %+v %+v\n want %+v %+v %+v", v.held, v.trusted, v.credited, full.held, full.trusted, full.credited)
	}
	if b.loadRowView("0000000000000000000000000000000000000000", full.orgHead) != nil {
		t.Error("the row view was used at another HEAD")
	}
	// the whole replay is unreadable now: a row read that needed it would
	// replay the history again
	if err := os.WriteFile(b.authorityPath(), []byte("not a replay"), 0o600); err != nil {
		t.Fatal(err)
	}
	forgetAuthority(b)
	before := replayCount()
	got, err := b.Get(ctx, row.Table("memories"), spoof)
	if err != nil {
		t.Fatal(err)
	}
	if n := replayCount() - before; n != 0 {
		t.Errorf("a row read replayed %d commit(s), want 0 (the row view)", n)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the row read through the view differs:\n got  %+v\n want %+v", got, want)
	}
	// a refresh whose cache is already at HEAD needs only the holds and
	// credits (it may serve a held row once, heldRecheck): the view again,
	// never the whole replay
	forgetAuthority(b)
	rowViews.Lock()
	delete(rowViews.byWork, b.work)
	rowViews.Unlock()
	before = replayCount()
	rows, next, err := b.Read(ctx, store.Cursor(full.head))
	if err != nil || string(next) != full.head {
		t.Fatalf("read at HEAD: %d rows, next %q, %v", len(rows), next, err)
	}
	if n := replayCount() - before; n != 0 {
		t.Errorf("a refresh with the cache at HEAD replayed %d commit(s), want 0 (the row view)", n)
	}
}
