package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResourceNamingRemote: a resource row naming the repo's remote in any
// spelling (ssh, https, nested in a list) is found with its name; one
// naming another repo is not (devco D-556).
func TestResourceNamingRemote(t *testing.T) {
	dir := t.TempDir()
	mine := map[string]bool{"github.com/acme-co/web": true}
	write := func(name, body string) string {
		p := filepath.Join(dir, name+".json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		body, name, project string
		ok                  bool
	}{
		{`{"id":"res_1","name":"web_app","fields":{"url":"git@github.com:acme-co/web.git"}}`, "web_app", "", true},
		{`{"id":"res_2","name":"mirror","fields":{"urls":["https://example.org/x","https://github.com/acme-co/web"]}}`, "mirror", "", true},
		{`{"id":"res_3","name":"api","fields":{"url":"git@github.com:acme-co/api.git"}}`, "", "", false},
		{`not json`, "", "", false},
		// devco:D-588: the stored {name, v} entries; project names the folder
		{`{"id":"res_5","name":"web_mcp","fields":{"fld_1":{"name":"url","v":"git@github.com:acme-co/web.git"},"fld_2":{"name":"project","v":"sync"}}}`, "web_mcp", "sync", true},
	}
	for i, c := range cases {
		p := write(strings.Repeat("r", i+1), c.body)
		name, remote, project, ok := resourceNamingRemote(p, mine, nil)
		if ok != c.ok || name != c.name || project != c.project || (ok && remote != "github.com/acme-co/web") {
			t.Errorf("case %d: got (%q, %q, %q, %v), want (%q, …, %q, %v)", i, name, remote, project, ok, c.name, c.project, c.ok)
		}
	}
	// devco:D-643: a select-valued project (OQ-AV: the val_ id in v, the
	// name in v_name) is the value's name, never the id and never ""
	// (which made the hint name the folder holding the row)
	sel := write("sel", `{"id":"res_6","name":"web_sel","fields":{"fld_1":{"name":"url","v":"git@github.com:acme-co/web.git"},"fld_2":{"name":"project","v":"val_9","v_name":"sync"}}}`)
	if _, _, project, ok := resourceNamingRemote(sel, mine, nil); !ok || project != "sync" {
		t.Errorf("select project with no field row = %q, %v; want the v_name sync", project, ok)
	}
	// the field row's value_ids win over a stale v_name (a renamed value)
	store := t.TempDir()
	if err := os.MkdirAll(filepath.Join(store, "org", "kind_fields"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "org", "kind_fields", "fld_2.json"), []byte(`{"id":"fld_2","name":"project","value_ids":[{"id":"val_9","name":"syncly"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, project, _ := resourceNamingRemote(sel, mine, storeValueNames(store, []string{"sync", "org"})); project != "syncly" {
		t.Errorf("select project by value_ids = %q, want syncly", project)
	}
	held := resourceFolder{Org: "acme", Folder: "sync", Holder: "org", Resource: "web_mcp", Remote: "github.com/acme-co/web"}.note()
	for _, w := range []string{"read acme/sync", "web_mcp (in acme/org)", "--project sync --own"} {
		if !strings.Contains(held, w) {
			t.Errorf("note %q lacks %q", held, w)
		}
	}
	note := resourceFolder{Org: "acme", Folder: "sync", Resource: "web_app", Remote: "github.com/acme-co/web"}.note()
	for _, w := range []string{"read acme/sync", "lore org link --org acme --project sync --own"} {
		if !strings.Contains(note, w) {
			t.Errorf("note %q lacks %q", note, w)
		}
	}
}
