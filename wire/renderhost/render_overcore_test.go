package renderhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOverCoreWarnNamesTheFixCommands guards devco D-573: the private
// context's over-budget warning said "unpin some or raise the key" with no
// command for either.
func TestOverCoreWarnNamesTheFixCommands(t *testing.T) {
	p := &privatePlan{budget: 8000, coreBytes: 20285, pins: []privatePin{
		{id: "rul_small", kind: privatePinRule, text: strings.Repeat("r", 100)},
		{id: "hfx_big", kind: privatePinHotfix, text: strings.Repeat("h", 9000)},
		{id: "rul_big", kind: privatePinRule, text: strings.Repeat("r", 5000)},
	}}
	w := privateOverCoreWarn(p)
	for _, want := range []string{
		"20285 bytes, over context.budget.internal 8000",
		"The largest: hfx_big (9000 bytes), rul_big (5000 bytes), rul_small (100 bytes).",
		"`lore rule edit --id rul_big --severity should`", // the largest listed rule, not the first found (devco D-602)
		"`lore hotfix edit --id hfx_big --severity medium`",
		"`lore policy set --key context.budget.internal --value 22000 --yes`",
	} {
		if !strings.Contains(w, want) {
			t.Errorf("warning lacks %q:\n%s", want, w)
		}
	}
	if strings.Contains(w, "unpin some") {
		t.Errorf("warning still says unpin with no command:\n%s", w)
	}
	if l := leftOutLine(3, 8000, 20285); !strings.Contains(l, "lore policy set --key context.budget.internal --value 22000 --yes") {
		t.Errorf("left-out line names no command: %s", l)
	}
}

// TestOverCoreWarnOncePerChange guards devco D-573: the warning repeated on
// every `lore sync`; it is printed only when a private file changes.
func TestOverCoreWarnOncePerChange(t *testing.T) {
	f := privateFile{path: filepath.Join(t.TempDir(), "acme.md"), body: "pinned\n"}
	if !privateFilesChanged([]privateFile{f}) {
		t.Fatal("a missing private file is not a change")
	}
	if err := os.WriteFile(f.path, []byte(f.body), 0o600); err != nil {
		t.Fatal(err)
	}
	if privateFilesChanged([]privateFile{f}) {
		t.Fatal("an unchanged private file counts as a change: the warning would repeat on every sync")
	}
	f.body = "pinned and more\n"
	if !privateFilesChanged([]privateFile{f}) {
		t.Fatal("a changed private file is not a change")
	}
}
