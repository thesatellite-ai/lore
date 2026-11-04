package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceImportExportRepointClone: a json import with refs between
// its rows in any order (3.20), all or nothing, --upsert-by name, a csv
// export that masks sensitive values and whose ••• is refused on
// re-import, repoint moving refs and archiving the old row, and clone.
func TestResourceImportExportRepointClone(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"ip","type":"ip","sensitive":true}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"service"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"runs_on","type":"ref","ref_kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"port","type":"number"}`)
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	doc := write("infra.json", `{"resources":[
		{"kind":"service","name":"api","fields":{"runs_on":"h1","port":8080}},
		{"kind":"host","name":"h1","fields":{"ip":"203.0.113.10"}}]}`)
	if _, err := h.Run(settings.OpResImport, `{"file":"`+doc+`"}`); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("import must ask: %v", err)
	}
	if got := opstest.Text(h.Must(settings.OpResImport, `{"file":"`+doc+`","dry_run":true}`)); !strings.Contains(got, "create  service api") {
		t.Fatalf("dry run %q", got)
	}
	got := opstest.Text(h.MustYes(settings.OpResImport, `{"file":"`+doc+`"}`))
	if !strings.Contains(got, "imported 2 rows (2 created, 0 updated)") {
		t.Fatalf("import %q", got)
	}
	if _, err := h.RunYes(settings.OpResImport, `{"file":"`+doc+`"}`); !errs.Has(err, errs.ResourceInvalid) {
		t.Fatalf("re-import without upsert: %v", err)
	}
	h.MustYes(settings.OpResImport, `{"file":"`+write("up.json", `{"resources":[{"kind":"service","name":"api","fields":{"port":9090}}]}`)+`","upsert_by":"name"}`)
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"api"}`)); !strings.Contains(s, "port = 9090") || !strings.Contains(s, "runs_on = h1") {
		t.Fatalf("upsert show %q", s)
	}
	csvOut := opstest.Text(h.Must(settings.OpResExport, `{"kind":"host","format":"csv"}`))
	if csvOut != "name,ip\nh1,•••\n" {
		t.Fatalf("export %q", csvOut)
	}
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h2","set":["ip=203.0.113.11"]}`)
	if _, err := h.RunYes(settings.OpResImport, `{"file":"`+write("hosts.csv", strings.Replace(csvOut, "h1", "h3", 1))+`","kind":"host"}`); !errs.Has(err, errs.ResourceInvalid) {
		t.Fatalf("masked re-import: %v", err)
	}
	if s := opstest.Text(h.MustYes(settings.OpResRepoint, `{"old":"h1","new":"h2"}`)); !strings.Contains(s, "repointed 1 rows from h1 to h2") {
		t.Fatalf("repoint %q", s)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"api"}`)); !strings.Contains(s, "runs_on = h2") {
		t.Fatalf("after repoint %q", s)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h1"}`)); !strings.Contains(s, "archived") {
		t.Fatalf("old not archived %q", s)
	}
	h.MustYes(settings.OpResClone, `{"from":"api","name":"api2","set":["port=1"]}`)
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"api2"}`)); !strings.Contains(s, "runs_on = h2") || !strings.Contains(s, "port = 1") {
		t.Fatalf("clone %q", s)
	}
}
