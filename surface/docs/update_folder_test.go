package docs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestUpdateOutFolder pins `lore spec docs|openapi --out` (1912ff42):
// openapi.json into a folder that does not exist yet creates the folder
// (owner-only, 0750) and the file, at any depth, and a second run writes
// nothing new; cli-commands.md is only ever updated between its markers,
// so a missing page is E_NOT_FOUND whose hint names both marker blocks
// and --out, and nothing is created for it.
func TestUpdateOutFolder(t *testing.T) {
	reg := registry(t, "Add a memory", false)
	for _, depth := range []string{"ref", "a/b/ref"} {
		t.Run("openapi into missing "+depth, func(t *testing.T) {
			base := t.TempDir()
			js := filepath.Join(base, filepath.FromSlash(depth), "openapi.json")
			if err := Update(reg, js); err != nil {
				t.Fatalf("Update into a missing folder: %v", err)
			}
			st, err := os.Stat(filepath.Dir(js))
			if err != nil || st.Mode().Perm() != newDirMode {
				t.Fatalf("folder %v mode %v, want %v", err, st.Mode().Perm(), newDirMode)
			}
			first, err := os.ReadFile(filepath.Clean(js))
			if err != nil || !strings.Contains(string(first), `"openapi"`) {
				t.Fatalf("openapi.json: %v %.80s", err, first)
			}
			if err = Update(reg, js); err != nil {
				t.Fatal(err)
			}
			if again, _ := os.ReadFile(filepath.Clean(js)); string(again) != string(first) {
				t.Fatal("a second run changed openapi.json")
			}
		})
	}
	t.Run("missing markdown page", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "nowhere")
		md := filepath.Join(dir, "cli-commands.md")
		err := Update(reg, md)
		if !errs.Has(err, errs.NotFound) {
			t.Fatalf("err = %v, want E_NOT_FOUND", err)
		}
		var le *errs.Error
		hint := ""
		if errors.As(err, &le) {
			hint = le.Hint
		}
		for _, want := range []string{markerBegin + BlockCommands + markerClose, markerBegin + BlockErrors + markerClose, "--out"} {
			if !strings.Contains(hint, want) {
				t.Fatalf("hint %q does not name %q", hint, want)
			}
		}
		if _, serr := os.Stat(dir); !os.IsNotExist(serr) {
			t.Fatalf("a refused markdown update created %s: %v", dir, serr)
		}
	})
}
