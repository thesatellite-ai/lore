package snapshot_test

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/schema"
)

// spec is v0.1.17's snapshot facts (read from its real output, 2026-11-04).
var spec = contenttest.Spec{
	Noun: "snapshot", Table: schema.TableSnapshots, Prefix: "snp",
	KindList: "snapshot.list", KindShow: "snapshot.show", KindSearch: "snapshot.search",
	Empty: "(no snapshots)\n", Badge: "", TitleKey: "title",
	Add: func(title, body string) string {
		return `{"title":` + contenttest.Quote(title) + `,"body":` + contenttest.Quote(body) + `}`
	},
	RecordKeys: []string{"id", "created_at", "updated_at", "trust_score", "source_kind", "project_id", "title", "body", "taken_at", "created_by_actor_id"},
	HitKeys:    []string{"body", "title"},
	Archive:    true,
	AddLine:    func(id, title string) string { return contenttest.Mark + " " + id + " — " + title + "\n" },
	ListLine: func(t testing.TB, rec string) string {
		return contenttest.Str(t, rec, "id") + " " + contenttest.Str(t, rec, "title") + "   (" + taken(t, rec).Format(time.DateOnly) + ")\n"
	},
	ShowText: func(t testing.TB, rec string) string {
		return contenttest.Str(t, rec, "id") + "\n  title:    " + contenttest.Str(t, rec, "title") + "\n  taken_at: " +
			taken(t, rec).Format(time.RFC3339) + "\n\n" + contenttest.Str(t, rec, "body") + "\n"
	},
}

func TestSnapshotConformance(t *testing.T) {
	contenttest.Check(t, spec, func(t *testing.T) contenttest.Runner { return opstest.New(t) })
}

// taken is a record's taken_at.
func taken(t testing.TB, rec string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, contenttest.Str(t, rec, "taken_at"))
	if err != nil {
		t.Fatalf("taken_at: %v in %s", err, rec)
	}
	return at
}

// TestSource: --source is v0.1.17's closed set (default manual); any
// other value is refused with the set named, and nothing is written.
func TestSource(t *testing.T) {
	h := opstest.New(t)
	for _, src := range []string{"", "manual", "learn-from", "agent-proposal", "plugin", "imported", "migrated", " plugin "} {
		id := h.ID(h.Must("snapshot.add", `{"title":"S","body":"b","source":`+opstest.Quote(src)+`}`))
		want := strings.TrimSpace(src)
		if want == "" {
			want = "manual"
		}
		if got := contenttest.Str(t, h.JSON(h.Must("snapshot.show", `{"id":"`+id+`"}`)), "data", "source_kind"); got != want {
			t.Errorf("source %q stored as %q, want %q", src, got, want)
		}
	}
	before := h.JSON(h.Must("snapshot.list", ""))
	for _, bad := range []string{"bogus", "Manual", "learn_from", "manual,plugin", "🚀"} {
		msg := h.Fails("snapshot.add", `{"title":"S","body":"b","source":`+opstest.Quote(bad)+`}`, errs.InvalidInput)
		if !strings.Contains(msg, "manual, learn-from, agent-proposal, plugin, imported, migrated") {
			t.Errorf("source %q: message %q does not name the set", bad, msg)
		}
	}
	if after := h.JSON(h.Must("snapshot.list", "")); after != before {
		t.Error("a refused --source wrote a row")
	}
}
