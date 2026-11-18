package wire

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestHealthInputFollowsIncludesD605: the stored setup health is stale
// when the global git config, or a file it includes (include.path or an
// includeIf rule, a relative path resolved against the including file),
// changed since it was recorded (devco D-605); an untouched config, or a
// check recorded after the change, keeps it.
func TestHealthInputFollowsIncludesD605(t *testing.T) {
	dir := t.TempDir()
	top := filepath.Join(dir, "gitconfig")
	mid := filepath.Join(dir, "shared.gitconfig")
	org := filepath.Join(dir, "conf", "acme.gitconfig")
	writeFileT(t, top, "[user]\n\tname = Ada\n[include]\n\tpath = shared.gitconfig\n")
	writeFileT(t, mid, "[includeIf \"gitdir:~/code/acme/\"]\n\tpath = conf/acme.gitconfig\n")
	writeFileT(t, org, "[user]\n\temail = ada@acme.example\n")
	t.Setenv("GIT_CONFIG_GLOBAL", top)
	t.Setenv("HOME", dir)
	a := &App{opts: Options{Home: filepath.Join(dir, "lorehome")}}
	if got := a.HealthInputChanged(background(), "acme"); got != "" {
		t.Fatalf("no results recorded, got %q", got)
	}
	if err := a.RecordHealth(background(), "acme", []onboard.CheckResult{{Check: "identity.git_email", Status: onboard.StatusOK}}); err != nil {
		t.Fatal(err)
	}
	if got := a.HealthInputChanged(background(), "acme"); got != "" {
		t.Fatalf("nothing changed, got %q", got)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(org, later, later); err != nil {
		t.Fatal(err)
	}
	if got := a.HealthInputChanged(background(), "acme"); got != "~/conf/acme.gitconfig" {
		t.Fatalf("the included file changed, got %q", got)
	}
	if err := a.RecordHealth(background(), "acme", []onboard.CheckResult{{Check: "identity.git_email", Status: onboard.StatusOK}}); err != nil {
		t.Fatal(err)
	}
	if got := a.HealthInputChanged(background(), "acme"); got != "" {
		t.Fatalf("re-checked after the change, got %q", got)
	}
}

func writeFileT(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
