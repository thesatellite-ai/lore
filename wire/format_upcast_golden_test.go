package wire

// TestUpcastGolden (FORMAT_EVOLUTION rule 3): every v1 document of the
// corpus upgrades to its golden file under testdata/format/upcast/v1-to-2/,
// and reading a golden file and writing it again changes nothing (Up is
// idempotent on its own output).
//
// The corpus is
//
//	generated   one all-columns v1 row per synced table (fixtureRow of
//	            format_determinism_test.go), golden <table>/all-columns.json
//	hand-made   testdata/format/v1/<table>/<case>.json: the _hlc fallbacks
//	            (no updated_by, created_at only, no times), keys this lore
//	            does not model, a pretty-printed hand edit
//
// Beside the byte golden, each case is checked against what the upgrade
// must do, so a golden can never pin a wrong result: the version is 2, the
// _hlc is the one upcastHLC derives (updated_at, else created_at, counter
// 0000, updated_by), the audit times stay, and every v1 field and unknown
// key keeps its value. Rewrite the goldens after a deliberate change with
//
//	go test ./wire -run TestUpcastGolden -update-upcast

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

var updateUpcast = flag.Bool("update-upcast", false, "rewrite testdata/format/upcast/v1-to-2")

// upcastDir is the golden folder of the one release step (v1 → v2).
var upcastDir = filepath.Join("testdata", "format", "upcast", "v1-to-2")

// upcastTo is the version the corpus upgrades to: the release step's (a
// `loretest_format` build adds an Additive step that only rows carrying
// "_loretest" need, so these rows stay at 2 there too).
const upcastTo = 2

// upcastCase is one v1 document and its golden path.
type upcastCase struct {
	name   string // <table>/<case>
	v1     []byte
	golden string
}

// upcastCorpus lists the generated and the hand-made cases.
func upcastCorpus(t *testing.T) []upcastCase {
	t.Helper()
	var out []upcastCase
	defs := fixtureTables(t)
	idOf := fixtureIDs(defs)
	for _, def := range defs {
		doc, err := fixtureRow(def, idOf[def.Name], idOf)
		if err != nil {
			t.Fatalf("no v1 fixture: %v", err)
		}
		name := def.Name.String() + "/all-columns"
		out = append(out, upcastCase{name: name, v1: []byte(doc), golden: filepath.Join(upcastDir, name+".json")})
	}
	hand, err := filepath.Glob(filepath.Join("testdata", "format", "v1", "*", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(hand) == 0 {
		t.Fatal("no hand-made v1 fixtures under testdata/format/v1")
	}
	for _, p := range hand {
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(filepath.Dir(p)) + "/" + strings.TrimSuffix(filepath.Base(p), ".json")
		out = append(out, upcastCase{name: name, v1: b, golden: filepath.Join(upcastDir, name+".json")})
	}
	return out
}

func TestUpcastGolden(t *testing.T) {
	t.Parallel()
	cases := upcastCorpus(t)
	for _, c := range cases {
		v1, err := row.Decode(c.v1)
		if err != nil {
			t.Errorf("%s: the v1 document does not decode: %v", c.name, err)
			continue
		}
		got, err := row.Encode(v1)
		if err != nil {
			t.Errorf("%s: encode: %v", c.name, err)
			continue
		}
		checkUpcastSemantics(t, c, got)
		if *updateUpcast {
			if mkErr := os.MkdirAll(filepath.Dir(c.golden), 0o750); mkErr != nil {
				t.Fatal(mkErr)
			}
			if wErr := os.WriteFile(c.golden, got, 0o600); wErr != nil {
				t.Fatal(wErr)
			}
			continue
		}
		want, err := os.ReadFile(filepath.Clean(c.golden))
		if err != nil {
			t.Errorf("%s: no golden (run with -update-upcast after checking the output): %v", c.name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: upgrade differs from %s:\ngot  %s\nwant %s", c.name, c.golden, got, want)
		}
		// Up on its own output: a v2 document reads and writes unchanged.
		v2, err := row.Decode(want)
		if err != nil {
			t.Errorf("%s: the golden does not decode: %v", c.name, err)
			continue
		}
		again, err := row.Encode(v2)
		if err != nil || !bytes.Equal(again, want) {
			t.Errorf("%s: re-reading the golden changed it (%v):\n%s", c.name, err, again)
		}
	}
	// Every golden belongs to a case: a stale golden of a removed table or
	// case fails here instead of lingering.
	if !*updateUpcast {
		files, err := filepath.Glob(filepath.Join(upcastDir, "*", "*.json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if !slices.ContainsFunc(cases, func(c upcastCase) bool { return c.golden == f }) {
				t.Errorf("%s is a golden without a v1 case", f)
			}
		}
	}
}

// checkUpcastSemantics checks the upgrade against rule 3, independently of
// the golden bytes.
func checkUpcastSemantics(t *testing.T, c upcastCase, out []byte) {
	t.Helper()
	v1, err := row.Decode(c.v1)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := row.Decode(out)
	if err != nil {
		t.Errorf("%s: the upgraded document does not decode: %v", c.name, err)
		return
	}
	if v1.SourceVersion != 1 || v2.SourceVersion != upcastTo {
		t.Errorf("%s: versions %d → %d, want 1 → %d", c.name, v1.SourceVersion, v2.SourceVersion, upcastTo)
	}
	if !bytes.Contains(out, []byte(`"_v":2`)) && !bytes.Contains(out, []byte(`"_v": 2`)) {
		t.Errorf("%s: the upgraded file does not say _v 2:\n%s", c.name, out)
	}
	if want := wantUpcastHLC(v1); v2.HLC.String() != want.String() {
		t.Errorf("%s: _hlc %s, want %s (updated_at, else created_at, else the v1 floor; counter 0000; updated_by)", c.name, v2.HLC, want)
	}
	if v2.HLC.Counter != 0 {
		t.Errorf("%s: an upgraded stamp has counter %d, want 0", c.name, v2.HLC.Counter)
	}
	if !v2.UpdatedAt.Equal(v1.UpdatedAt) || !v2.CreatedAt.Equal(v1.CreatedAt) || v2.UpdatedBy != v1.UpdatedBy || v2.CreatedBy != v1.CreatedBy {
		t.Errorf("%s: the audit fields changed", c.name)
	}
	if len(v2.Fields) != len(v1.Fields) || len(v2.Reserved) != len(v1.Reserved) {
		t.Errorf("%s: %d fields, %d reserved keys after the upgrade, want %d and %d", c.name, len(v2.Fields), len(v2.Reserved), len(v1.Fields), len(v1.Reserved))
	}
	for k, v := range v1.Fields {
		if w, ok := v2.Fields[k]; !ok || !value.Equal(v, w) {
			t.Errorf("%s: field %s changed by the upgrade", c.name, k)
		}
	}
	for k, v := range v1.Reserved {
		if w, ok := v2.Reserved[k]; !ok || !value.Equal(v, w) {
			t.Errorf("%s: reserved key %s changed by the upgrade", c.name, k)
		}
	}
}

// v1FloorWall is the wall of a v1 row with no audit time at all; the test
// reads it from a row that has none rather than repeating the constant.
func v1FloorWall(t *testing.T) time.Time {
	t.Helper()
	r, err := row.Decode([]byte(`{"_v":1,"_table":"memories","id":"mem_01a1a59b891c70b78914e000000000ff"}`))
	if err != nil {
		t.Fatal(err)
	}
	return r.HLC.Wall
}

// wantUpcastHLC is spec 12's stamp of a v1 row, computed here from the
// row's own fields.
func wantUpcastHLC(r row.Row) hlc.HLC {
	switch {
	case !r.UpdatedAt.IsZero():
		return hlc.FromUpdate(r.UpdatedAt, r.UpdatedBy)
	case !r.CreatedAt.IsZero():
		return hlc.FromUpdate(r.CreatedAt, r.CreatedBy)
	}
	return r.HLC // checked against the floor in TestUpcastFloorStamp
}

// TestUpcastFloorStamp: a v1 row with no audit time gets the v1 floor stamp
// (a fixed instant, not the reading machine's clock): two reads agree and
// the wall is before every 0.1.18 write.
func TestUpcastFloorStamp(t *testing.T) {
	t.Parallel()
	a, b := v1FloorWall(t), v1FloorWall(t)
	if a.IsZero() || !a.Equal(b) {
		t.Fatalf("floor wall %v / %v: want one fixed instant", a, b)
	}
	if !a.Before(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("floor wall %v is not before the 0.1.18 era", a)
	}
}
