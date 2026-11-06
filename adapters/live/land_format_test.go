package live

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
)

// testBinary is a release version this test's binary claims to be.
const testBinary = "0.1.18"

// TestBuildLandingFormatGate: a landing (and the ride-along, which shares
// BuildLanding) into a folder whose branch _meta.json this lore may not
// write is refused E_OLD_LORE before any change is built (FORMAT_EVOLUTION
// rule 2, OV48); without a version set the check is skipped (callers that
// predate it).
func TestBuildLandingFormatGate(t *testing.T) {
	known := formatgate.Known()[0].Name
	cases := []struct {
		name    string
		meta    string // "" = no _meta.json on the branch
		version string
		refused bool
	}{
		{"no _meta.json", "", testBinary, false},
		{"known feature, no floor", `{"features":["` + known + `"]}`, testBinary, false},
		{"floor equal", `{"min_lore_version":"` + testBinary + `"}`, testBinary, false},
		{"other keys only", `{"project_id":"x"}`, testBinary, false},
		{"unknown feature", `{"features":["` + known + `","from_the_future"]}`, testBinary, true},
		{"floor above", `{"min_lore_version":"999.0.0"}`, testBinary, true},
		{"floor above, dev build", `{"min_lore_version":"999.0.0"}`, "dev", true},
		{"conflict markers", "<<<<<<< ours\n{}\n=======\n{}\n>>>>>>> theirs\n", testBinary, true},
		{"features not a list", `{"features":"x"}`, testBinary, true},
		{"unknown feature, no version set: skipped", `{"features":["from_the_future"]}`, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := map[string][]byte{}
			if c.meta != "" {
				files[".lore/data/_meta.json"] = []byte(c.meta)
			}
			read := func(p string) ([]byte, bool, error) { b, ok := files[p]; return b, ok, nil }
			layout := testLayout
			layout.BinaryVersion = c.version
			item := LandItem{Folder: RootFolder, Key: RowKey{Table: "tasks", ID: "tsk_1"}, File: openRow(t0, t0), Action: LandFinal}
			ch, _, err := BuildLanding(context.Background(), read, []LandItem{item}, layout)
			if !c.refused {
				if err != nil || len(ch.Put) != 1 {
					t.Fatalf("want one row landed, got %+v %v", ch, err)
				}
				return
			}
			if errs.CodeOf(err) != errs.OldLore {
				t.Fatalf("want E_OLD_LORE, got %v", err)
			}
			if !ch.Empty() {
				t.Fatalf("a refused landing builds nothing: %+v", ch)
			}
		})
	}
}

// TestBuildLandingFormatGateEveryFolder: one gated folder refuses the
// whole landing, whichever folder comes first.
func TestBuildLandingFormatGateEveryFolder(t *testing.T) {
	files := map[string][]byte{"sub/.lore/data/_meta.json": []byte(`{"features":["from_the_future"]}`)}
	read := func(p string) ([]byte, bool, error) { b, ok := files[p]; return b, ok, nil }
	layout := testLayout
	layout.BinaryVersion = testBinary
	items := []LandItem{
		{Folder: RootFolder, Key: RowKey{Table: "tasks", ID: "tsk_1"}, File: openRow(t0, t0), Action: LandFinal},
		{Folder: "sub", Key: RowKey{Table: "tasks", ID: "tsk_2"}, File: openRow(t0, t0), Action: LandFinal},
	}
	if _, _, err := BuildLanding(context.Background(), read, items, layout); errs.CodeOf(err) != errs.OldLore {
		t.Fatalf("want E_OLD_LORE, got %v", err)
	}
}
