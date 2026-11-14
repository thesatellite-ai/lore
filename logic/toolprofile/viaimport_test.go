package toolprofile

import (
	"strings"
	"testing"
	"testing/fstest"
)

// readProbe is fsProbe that also reads files (Reader).
type readProbe struct{ fsProbe }

func (p readProbe) Read(rel string) (string, bool) {
	f, ok := p.fs[rel]
	if !ok {
		return "", false
	}
	return string(f.Data), true
}

// TestResolveSkipsImporter pins I-14: CLAUDE.md that imports @AGENTS.md
// already loads AGENTS.md's pointer, so a fresh repo is wired into
// AGENTS.md alone (claude_code noted as reading it through the import),
// and a setup naming both does the same; a wired AGENTS.md never offers
// that CLAUDE.md for wiring. Without the import both are wired, as before.
func TestResolveSkipsImporter(t *testing.T) {
	reg, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	importing := fstest.MapFS{"CLAUDE.md": file("@AGENTS.md\n\nnotes\n"), "AGENTS.md": file("# agents\n")}
	sel := reg.Resolve(readProbe{fsProbe{fs: importing}})
	if got := names(sel.Profiles); got != NameCodex || sel.Reason != ReasonDetected {
		t.Fatalf("Resolve = %s (%s), want %s", got, sel.Reason, NameCodex)
	}
	if len(sel.ViaImport) != 1 || sel.ViaImport[0].Profile.Name != NameClaudeCode || sel.ViaImport[0].Via != "AGENTS.md" {
		t.Fatalf("ViaImport = %+v", sel.ViaImport)
	}
	if n := sel.ViaImport[0].Note(); !strings.Contains(n, "CLAUDE.md imports @AGENTS.md") {
		t.Errorf("note = %q", n)
	}
	setup := reg.FromSetup([]string{NameClaudeCode, NameCodex}, sel, readProbe{fsProbe{fs: importing}})
	if got := names(setup.Profiles); got != NameCodex || len(setup.ViaImport) != 1 || len(setup.Unwired) != 0 {
		t.Fatalf("FromSetup = %s, via %+v, unwired %+v", got, setup.ViaImport, setup.Unwired)
	}
	codex, _ := reg.Lookup(NameCodex)
	wired := codex.StitchPointer("# agents\n", Link{FromPointer: ".lore/LORE.md", FromRoot: ".lore/LORE.md"})
	w := reg.Resolve(readProbe{fsProbe{fs: fstest.MapFS{"CLAUDE.md": file("@AGENTS.md\n"), "AGENTS.md": file(wired)}}})
	if w.Reason != ReasonWired || len(w.Unwired) != 0 {
		t.Fatalf("wired AGENTS.md: %s, unwired %+v", w.Reason, w.Unwired)
	}
	plain := reg.Resolve(readProbe{fsProbe{fs: fstest.MapFS{"CLAUDE.md": file("notes\n"), "AGENTS.md": file("# agents\n")}}})
	if got := names(plain.Profiles); got != NameClaudeCode+","+NameCodex || len(plain.ViaImport) != 0 {
		t.Fatalf("no import: %s, via %+v", got, plain.ViaImport)
	}
}
