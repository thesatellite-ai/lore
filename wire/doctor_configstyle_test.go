package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// flowStyleConfig is a config.yaml with the setups list on one flow line, as a
// write before the block-style writer left it.
const flowStyleConfig = "# my machine\nidentity:\n  key: human:sam@example.test\nsetups: [{name: personal, kind: solo, identity: {key: 'human:sam@example.test'}}]\n"

// configStyleApp is an App over a temp ~/.lore holding raw as config.yaml.
func configStyleApp(t *testing.T, raw string) (*App, string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, ConfigFile)
	if raw != "" {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &App{opts: Options{Home: home, Local: LocalPorts{Config: standaloneConfig{home: home}}}}, path
}

// TestDoctorConfigStyle: a flow-style config.yaml is one warning naming
// the key and `lore config fmt`, and doctor leaves the file byte for byte;
// doctor --fix rewrites it in block style (comment kept) and the next run
// finds nothing. A block file, an absent file and one that does not parse
// give no finding.
func TestDoctorConfigStyle(t *testing.T) {
	ctx := context.Background()
	a, path := configStyleApp(t, flowStyleConfig)
	f, err := a.DoctorConfigStyle(ctx, false)
	if err != nil || len(f) != 1 || f[0].Level != DoctorWarning || f[0].Fixed {
		t.Fatalf("findings %+v %v", f, err)
	}
	line := f[0].Line()
	for _, w := range []string{"config_style: ", path + " has setups written on one line", "fix: lore config fmt"} {
		if !strings.Contains(line, w) {
			t.Fatalf("line %q lacks %q", line, w)
		}
	}
	if b, _ := os.ReadFile(path); string(b) != flowStyleConfig {
		t.Fatalf("doctor without --fix changed the file:\n%s", b)
	}
	f, err = a.DoctorConfigStyle(ctx, true)
	if err != nil || len(f) != 1 || !f[0].Fixed {
		t.Fatalf("--fix findings %+v %v", f, err)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "# my machine\n") || !strings.Contains(string(b), "setups:\n  - name: personal\n    kind: solo\n") {
		t.Fatalf("rewritten file:\n%s", b)
	}
	if f, err = a.DoctorConfigStyle(ctx, false); err != nil || len(f) != 0 {
		t.Fatalf("after the fix: %+v %v", f, err)
	}
	for name, raw := range map[string]string{
		"block":      "identity:\n  key: human:sam@example.test\n",
		"absent":     "",
		"flat lists": "tools: [claude, codex]\n",
		"broken":     "setups: [{name: a\n",
	} {
		a, path := configStyleApp(t, raw)
		if f, err := a.DoctorConfigStyle(ctx, true); err != nil || len(f) != 0 {
			t.Fatalf("%s: %+v %v", name, f, err)
		}
		if b, _ := os.ReadFile(path); string(b) != raw {
			t.Fatalf("%s: file changed:\n%s", name, b)
		}
	}
}

// TestDoctorConfigStyleDegrades: the doctor operation reports the warning
// and is no longer healthy.
func TestDoctorConfigStyleDegrades(t *testing.T) {
	a := newTestApp(t, gitRepo(t))
	if err := os.WriteFile(filepath.Join(a.opts.Home, ConfigFile), []byte(flowStyleConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runDoctorOp(t, a)
	found := false
	for _, w := range r.Warnings {
		found = found || strings.HasPrefix(w, string(DoctorCheckConfigStyle)+": ")
	}
	if !found || r.Status == opsdoctor.StatusHealthy {
		t.Fatalf("warnings %q status %s", r.Warnings, r.Status)
	}
}
