package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Seeds of the dryRunCases of devco:D-931 (H-192): the resource and
// metadata writes whose --dry-run returned its own preview before the
// write path ran. Each seed returns what the case inputs name as {{id}}.

// fakeAccessKey is a made-up cloud access key id, built at run time so no
// key-shaped literal sits in the source: the write's secret scan refuses
// a value holding it, and the old previews never ran that scan.
var fakeAccessKey = "AKIA" + strings.Repeat("Q", 16)

// seedHosts is the host kind (text field provider, text field port, ref
// field parent to another host) and hosts h_one (provider x, port 22)
// and h_two (provider x, parent h_one).
func seedHosts(t *testing.T, a *App) string {
	t.Helper()
	for _, s := range []struct{ name, in string }{
		{"resource.kind.add", `{"kind":"host"}`},
		{"resource.field.add", `{"kind":"host","field":"provider","type":"text"}`},
		{"resource.field.add", `{"kind":"host","field":"port","type":"text"}`},
		{"resource.field.add", `{"kind":"host","field":"parent","type":"ref","ref_kind":"host"}`},
		{"resource.add", `{"kind":"host","name":"h_one","set":["provider=x","port=22"]}`},
		{"resource.add", `{"kind":"host","name":"h_two","set":["provider=x","parent=h_one"]}`},
		{"resource.add", `{"kind":"host","name":"h_three","set":["provider=z"]}`},
	} {
		if _, err := run(t, a, s.name, s.in, true); err != nil {
			t.Fatalf("%s %s: %v", s.name, s.in, err)
		}
	}
	return ""
}

// seedHostFiles is seedHosts plus a folder holding good.csv (two new
// hosts) and bad.csv (one good host, one naming a field the kind does
// not have); the folder is {{id}}.
func seedHostFiles(t *testing.T, a *App) string {
	t.Helper()
	seedHosts(t, a)
	dir := t.TempDir()
	files := map[string]string{
		"good.csv": "name,provider\nh_four,y\nh_five,y\n",
		"bad.csv":  "name,provider,colour\nh_six,y,\nh_seven,y,blue\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// seedMetaFiles is seedStrictKey (a memory, strict key channel taking web
// or app) plus a folder holding good.csv (channel=web on the memory) and
// bad.csv (channel=fax, which the strict key refuses); the folder is {{id}}.
func seedMetaFiles(t *testing.T, a *App) string {
	t.Helper()
	id := seedStrictKey(t, a)
	dir := t.TempDir()
	for name, v := range map[string]string{"good.csv": "web", "bad.csv": "fax"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("id,key,value\n"+id+",channel,"+v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// seedSizeKey is a text key size set to "3" on a memory: retyping it to
// number converts the value, and the converted value is checked against
// the key as retyped (the staged key row), as the write checks it.
func seedSizeKey(t *testing.T, a *App) string {
	t.Helper()
	if _, err := run(t, a, "meta.add", `{"name":"size","type":"text"}`, true); err != nil {
		t.Fatal(err)
	}
	id := seedMemory(t, a)
	if _, err := run(t, a, "meta.set", `{"id":"`+id+`","pair":"size=3"}`, true); err != nil {
		t.Fatal(err)
	}
	return id
}
