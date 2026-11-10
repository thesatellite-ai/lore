package live

import (
	"strconv"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/canonjson"
)

// fileWith is a finished-and-named live row whose body is doc.
func fileWith(t *testing.T, doc string) File {
	t.Helper()
	body, err := canonjson.Decode([]byte(doc), canonjson.DefaultMaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	return File{Body: body, Block: Block{LiveSince: t0, HLC: HLC{Wall: t0}}}
}

// TestNestedConflictNeverLands (METADATA 3.12: every conflict scanner
// recurses into meta): HasConflictMarkers finds a marker block at any
// depth and a structured meta conflict entry, and ShouldLand keeps such a
// row live even when it is finished and named; clean shapes of the same
// depth still land.
func TestNestedConflictNeverLands(t *testing.T) {
	q := strconv.Quote("<<<<<<< 0001\nmine\n=======\ntheirs\n>>>>>>> 0002")
	cases := []struct {
		name     string
		doc      string
		conflict bool
	}{
		{"clean row", `{"id":"tsk_1","title":"x"}`, false},
		{"clean meta", `{"id":"tsk_1","title":"x","meta":{"mky_1":{"name":"cost","v":3.5},"mky_2":{"name":"labels","v":["a","b"]}}}`, false},
		{"clean nested object value", `{"id":"tsk_1","meta":{"mky_1":{"name":"deploy","v":{"env":{"region":"eu"}}}}}`, false},
		{"marker-like line only", `{"id":"tsk_1","meta":{"mky_1":{"name":"note","v":"<<<<<<< not a block"}}}`, false},
		{"a field named conflict outside meta", `{"id":"tsk_1","fields":{"conflict":{"v":"x"}}}`, false},
		{"top-level text clash", `{"id":"tsk_1","title":` + q + `}`, true},
		{"structured meta conflict", `{"id":"tsk_1","meta":{"mky_1":{"name":"cost","strict":true,"conflict":{"base":3.5,"ours":4,"theirs":5}}}}`, true},
		{"structured conflict next to clean entries", `{"id":"tsk_1","meta":{"mky_0":{"name":"a","v":"1"},"mky_1":{"name":"cost","conflict":{"base":null,"ours":4,"theirs":null}}}}`, true},
		{"marker block in a meta text value", `{"id":"tsk_1","meta":{"mky_1":{"name":"note","v":` + q + `}}}`, true},
		{"marker block in a meta list item", `{"id":"tsk_1","meta":{"mky_1":{"name":"labels","v":["a",` + q + `]}}}`, true},
		{"marker block in a nested object field", `{"id":"tsk_1","meta":{"mky_1":{"name":"deploy","v":{"env":{"region":` + q + `}}}}}`, true},
		{"marker block in a fields entry", `{"id":"res_1","fields":{"kfd_1":{"name":"host","v":` + q + `}}}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := fileWith(t, c.doc)
			if got := f.HasConflictMarkers(); got != c.conflict {
				t.Fatalf("HasConflictMarkers = %v, want %v", got, c.conflict)
			}
			want := LandFinal
			if c.conflict {
				want = LandStay
			}
			got := ShouldLand(LandInputs{File: f, Finished: true, Named: true, MaxAge: week, Now: t0.Add(time.Hour)})
			if got.Action != want {
				t.Fatalf("ShouldLand = %+v, want %s", got, want)
			}
		})
	}
}
