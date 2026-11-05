package settings_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// ownRepo makes the harness repo an org's own repo (the binary wires
// RepoIsClient; with no hook the repo fails closed to client, whose `_o`
// keeps no import slot for a one-off file, PROVENANCE 8.1).
func ownRepo(o *wire.Options) {
	o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
}

// importOut decodes a resource.import result.
func importOut(t *testing.T, h *opstest.H, in string) settings.ResImportOut {
	t.Helper()
	var out settings.ResImportOut
	if err := json.Unmarshal([]byte(h.JSON(h.MustYes(settings.OpResImport, in))), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// actions maps name → import action of out.
func actions(out settings.ResImportOut) map[string]string {
	m := map[string]string{}
	for _, r := range out.Rows {
		m[r.Name] = r.Action
	}
	return m
}

// TestResourceReimportMatrix (PROVENANCE 11.4, P2-RES-2/3): importing the
// same file again matches rows by (importer kind, external id):
//   - a row unchanged since the last import is updated in place;
//   - a row edited in lore since is kept, with the "kept" line;
//   - --overwrite replaces the edited row too;
//   - the same name from another source (csv after json) is E_RESOURCE_TAKEN
//     (inside the all-or-nothing E_RESOURCE_INVALID);
//   - a row imported before the history mark (no import:true entry) counts
//     as edited and is kept;
//   - in a client repo (no import slot is kept) a re-import never
//     overwrites: E_RESOURCE_TAKEN.
func TestResourceReimportMatrix(t *testing.T) {
	h := opstest.New(t, ownRepo)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`},
	)
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	v1 := write("v1.json", `{"resources":[{"kind":"host","name":"h1","fields":{"os":"linux"}},{"kind":"host","name":"h2","fields":{"os":"bsd"}},{"kind":"host","name":"h3","fields":{"os":"mac"}}]}`)
	v2 := write("v2.json", `{"resources":[{"kind":"host","name":"h1","fields":{"os":"linux-2"}},{"kind":"host","name":"h2","fields":{"os":"bsd-2"}},{"kind":"host","name":"h3","fields":{"os":"mac-2"}}]}`)
	if out := importOut(t, h, `{"file":"`+v1+`"}`); out.Created != 3 {
		t.Fatalf("first import %+v", out)
	}
	h3 := h.ID(h.Must(settings.OpResShow, `{"id":"h3"}`))

	// Same file again, nothing edited: every row is a re-import, updated in place.
	if out := importOut(t, h, `{"file":"`+v1+`"}`); out.Updated != 3 || out.Created != 0 || out.Kept != 0 {
		t.Fatalf("unchanged re-import %+v", out)
	}
	h.MustYes(settings.OpResEdit, `{"id":"h2","set":["os=edited"]}`)

	// h3: strip the import mark from its history, as a row imported before
	// the mark existed (P2-RES-2: counts as edited).
	p := filepath.Join(h.Repo, ".lore", "data", schema.TableResources, h3+".json")
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err = json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	var hist []map[string]json.RawMessage
	if err = json.Unmarshal(doc["history"], &hist); err != nil {
		t.Fatal(err)
	}
	for _, e := range hist {
		delete(e, "import")
	}
	if doc["history"], err = json.Marshal(hist); err != nil {
		t.Fatal(err)
	}
	if b, err = json.Marshal(doc); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	h = h.ReopenCold(ownRepo)

	out := importOut(t, h, `{"file":"`+v2+`"}`)
	if got := actions(out); got["h1"] != "update" || got["h2"] != "kept" || got["h3"] != "kept" || out.Kept != 2 || out.Updated != 1 {
		t.Fatalf("re-import after edits %+v", out)
	}
	if txt := opstest.Text(h.MustYes(settings.OpResImport, `{"file":"`+v2+`","dry_run":true}`)); !strings.Contains(txt, "kept 2 rows edited in lore since the last import") {
		t.Errorf("kept line %q", txt)
	}
	show := func(n string) string { return opstest.Text(h.Must(settings.OpResShow, `{"id":"`+n+`"}`)) }
	if !strings.Contains(show("h1"), "os = linux-2") || !strings.Contains(show("h2"), "os = edited") || !strings.Contains(show("h3"), "os = mac\n") {
		t.Fatalf("after re-import: %q / %q / %q", show("h1"), show("h2"), show("h3"))
	}
	if out = importOut(t, h, `{"file":"`+v2+`","overwrite":true}`); out.Updated != 3 || out.Kept != 0 {
		t.Fatalf("--overwrite %+v", out)
	}
	if !strings.Contains(show("h2"), "os = bsd-2") || !strings.Contains(show("h3"), "os = mac-2") {
		t.Fatalf("after --overwrite: %q / %q", show("h2"), show("h3"))
	}

	// Another source names the same row: not a re-import.
	csv := write("hosts.csv", "name,os\nh1,win\n")
	_, err = h.RunYes(settings.OpResImport, `{"file":"`+csv+`","kind":"host"}`)
	if !errs.Has(err, errs.ResourceBatch) || !strings.Contains(err.Error(), string(errs.ResourceTaken)) {
		t.Fatalf("csv after json: %v", err)
	}
	if !strings.Contains(show("h1"), "os = linux-2") {
		t.Fatalf("refused csv wrote: %q", show("h1"))
	}

	// A client repo: the first import keeps no import slot, so the same file
	// again is not recognised and never overwrites.
	c := opstest.New(t)
	mustAll(t, c,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`},
	)
	importOut(t, c, `{"file":"`+v1+`"}`)
	if _, err = c.RunYes(settings.OpResImport, `{"file":"`+v2+`"}`); err == nil || !strings.Contains(err.Error(), string(errs.ResourceTaken)) {
		t.Fatalf("client re-import: %v", err)
	}
}
