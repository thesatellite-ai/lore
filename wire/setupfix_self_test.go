//go:build !windows

// The stand-in binaries are sh scripts.

package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/execfile"
)

// TestFixRunsTheServingBinary: a `lore …` fix (the page's Run it, Set up
// lore here) runs the binary serving the page, never a `lore` found on
// PATH: an older lore on PATH would init with old rules. The runner the
// App hands out resolves lore through os.Executable.
func TestFixRunsTheServingBinary(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := execfile.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	self := write("serving-lore", `echo serving "$@" "$PWD"`)
	pathDir := filepath.Join(dir, "path")
	if err := os.Mkdir(pathDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := execfile.WriteFile(filepath.Join(pathDir, FixProgramLore), []byte("#!/bin/sh\necho path-lore\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := t.TempDir()
	r := setupFixRunner{lore: func() (string, error) { return self, nil }}
	run, err := r.RunFixIn(context.Background(), repo, []string{FixProgramLore, "init", "--target", "claude_code"})
	if err != nil || run.Exit != 0 {
		t.Fatalf("run = %+v, %v", run, err)
	}
	if !strings.Contains(run.OutputTail, "serving init --target claude_code") || strings.Contains(run.OutputTail, "path-lore") {
		t.Fatalf("output %q: want the serving binary, never lore from PATH", run.OutputTail)
	}
	if !strings.Contains(run.OutputTail, filepath.Base(repo)) {
		t.Fatalf("output %q: want it run inside the repo %s", run.OutputTail, repo)
	}

	// The App's runner is wired to os.Executable.
	a := &App{}
	fr, err := a.SetupFixRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sr, ok := fr.(setupFixRunner)
	if !ok || sr.lore == nil {
		t.Fatalf("SetupFixRunner = %#v, want a setupFixRunner resolving lore itself", fr)
	}
	got, err := sr.lore()
	want, werr := os.Executable()
	if err != nil || werr != nil || got != want {
		t.Fatalf("runner lore = %q (%v), want os.Executable %q (%v)", got, err, want, werr)
	}
}
