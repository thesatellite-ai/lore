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

// TestResourceImportYAMLAndCycle (3.20, OV19): a yaml import with refs by
// name in any order writes every row; two rows that name each other (a
// cycle) still import as one batch, both refs resolved (RES 3.20; a ref
// cycle is valid data, RES 3.4 and section 10).
func TestResourceImportYAMLAndCycle(t *testing.T) {
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"peer","type":"ref","ref_kind":"host"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"tags","type":"list"}`},
	)
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	y := write("hosts.yaml", "resources:\n"+
		"  - kind: host\n    name: web\n    fields:\n      os: linux\n      peer: db\n      tags: [a, b]\n"+
		"  - kind: host\n    name: db\n    fields:\n      os: \"bsd ✓\"\n")
	if s := opstest.Text(h.MustYes(settings.OpResImport, `{"file":"`+y+`"}`)); !strings.Contains(s, "imported 2 rows (2 created, 0 updated) from yaml") {
		t.Fatalf("yaml import %q", s)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"web"}`)); !strings.Contains(s, "peer = db") || !strings.Contains(s, "tags = a, b") {
		t.Errorf("web %q", s)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"db"}`)); !strings.Contains(s, "os = bsd ✓") {
		t.Errorf("db %q", s)
	}
	cyc := write("cycle.json", `{"resources":[
		{"kind":"host","name":"a","fields":{"peer":"b"}},
		{"kind":"host","name":"b","fields":{"peer":"a"}}]}`)
	// RESOURCES 3.20 (refs between rows of one file in any order) and
	// 3.4/10 (a ref cycle is valid data): the cycle imports as one batch.
	res, err := h.RunYes(settings.OpResImport, `{"file":"`+cyc+`"}`)
	if err != nil {
		t.Fatalf("cycle import refused: %v (hosts left: %d)", err, countHosts(t, h))
	}
	if s := opstest.Text(res); !strings.Contains(s, "imported 2 rows") {
		t.Fatalf("cycle import %q", s)
	}
	for n, peer := range map[string]string{"a": "b", "b": "a"} {
		if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"`+n+`"}`)); !strings.Contains(s, "peer = "+peer) {
			t.Errorf("%s %q", n, s)
		}
	}
}

// TestResourcesAllowedIn (RQ2, OV19): resources.allowed_in refuses a place
// it leaves out on every write that creates a resource there (add, clone,
// import), naming the level that set it, and an allowed place still works.
func TestResourcesAllowedIn(t *testing.T) {
	h := opstest.New(t, ownRepo)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h1"}`)
	h.MustYes(settings.OpPolicySet, setIn("resources.allowed_in", "store", "repo"))
	dir := t.TempDir()
	p := filepath.Join(dir, "h.json")
	if err := os.WriteFile(p, []byte(`{"resources":[{"kind":"host","name":"h9"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, w := range []struct{ op, in string }{
		{settings.OpResAdd, `{"kind":"host","name":"h2"}`},
		{settings.OpResClone, `{"from":"h1","name":"h3"}`},
		{settings.OpResImport, `{"file":"` + p + `"}`},
	} {
		msg := h.Fails(w.op, w.in, errs.ResourcePlace)
		if !strings.Contains(msg, "resources.allowed_in: store") || !strings.Contains(msg, "set at level repo") {
			t.Errorf("%s: %s", w.op, msg)
		}
	}
	if n := countHosts(t, h); n != 1 {
		t.Fatalf("refused writes left %d hosts", n)
	}
	h.MustYes(settings.OpPolicySet, setIn("resources.allowed_in", "store,repo", "repo"))
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h2"}`)
}
