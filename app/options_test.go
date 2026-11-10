package app

// options_test.go — Run applies its Options before building the command:
// WithVersion is the version the binary reports, WithSetupUI the UI the
// setup page serves, and no option keeps "dev" and the fallback page
// (setupweb.DefaultUI, setupUI nil). The served page itself is proven out
// of process: tests/setupe2e TestBinaryServesDefaultUI for this module's
// cmd/lore, and an importing build's own suite for its UI.

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/thesatellite-ai/lore/surface/setupweb"
)

// optionsTestUI is a setupweb.UI that only identifies itself.
type optionsTestUI struct{}

func (optionsTestUI) FS() fs.FS       { return fstest.MapFS{"index.html": {Data: []byte("x")}} }
func (optionsTestUI) Version() string { return "options-test" }

// keepProcessState restores what Run sets, so other tests see the defaults.
func keepProcessState(t *testing.T) {
	t.Helper()
	oldVersion, oldUI := version, setupUI
	t.Cleanup(func() { version, setupUI = oldVersion, oldUI })
}

func TestRunAppliesOptions(t *testing.T) {
	keepProcessState(t)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	const v = "9.8.7"
	var ui setupweb.UI = optionsTestUI{}
	code, out, errOut := captureRun(t, func() int {
		return Run(context.Background(), []string{"version"}, WithVersion(v), WithSetupUI(ui))
	})
	if code != 0 || !strings.Contains(out, v) {
		t.Fatalf("lore version with WithVersion(%q): code %d\nstdout: %s\nstderr: %s", v, code, out, errOut)
	}
	if setupUI != ui {
		t.Fatalf("WithSetupUI: the page would serve %v, want the UI handed in", setupUI)
	}
}

func TestRunWithoutOptionsKeepsDefaults(t *testing.T) {
	keepProcessState(t)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	version, setupUI = "dev", optionsTestUI{} // as a previous Run in this process may have left them
	code, out, errOut := captureRun(t, func() int {
		return Run(context.Background(), []string{"version"}, WithVersion(""))
	})
	if code != 0 || !strings.Contains(out, "dev") {
		t.Fatalf("lore version with no version option: code %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if setupUI != nil {
		t.Fatalf("no WithSetupUI: setupUI is %v, want nil (setupweb.DefaultUI)", setupUI)
	}
}
