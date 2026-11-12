package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// fiftyIDs is a list of 50 made-up lore ids, ", " joined.
func fiftyIDs() []string {
	out := make([]string, 50)
	for i := range out {
		out[i] = fmt.Sprintf("rul_%032x", i)
	}
	return out
}

// TestListsCappedInTextOnly: a text output and an error line naming 50
// ids show the first 5 and "… and 45 more (`<command> --json` lists
// all)"; the --json output and the --json error keep all 50.
func TestListsCappedInTextOnly(t *testing.T) {
	ids := fiftyIDs()
	body := "links " + strings.Join(ids, ", ")
	f := newFake(t)
	human := execute(Build(f.reg, f), "", "memory", "add", body)
	if human.exit != 0 {
		t.Fatalf("exit %d stderr %q", human.exit, human.stderr)
	}
	want := strings.Join(ids[:5], ", ") + " … and 45 more (`lore memory add --json` lists all)"
	if !strings.Contains(human.stdout, want) || strings.Contains(human.stdout, ids[5]) {
		t.Errorf("text output not capped:\n%s", human.stdout)
	}
	asJSON := execute(Build(f.reg, f), "", "memory", "add", body, "--json")
	if asJSON.exit != 0 || !strings.Contains(asJSON.stdout, ids[49]) || strings.Contains(asJSON.stdout, "more (") {
		t.Errorf("--json lost items: %q", asJSON.stdout)
	}

	err := errs.New(errs.InvalidInput, "rows "+strings.Join(ids, ", ")+" are stale").WithHint("lore doctor --fix repairs " + strings.Join(ids, ", "))
	var out, eout bytes.Buffer
	printError(&out, &eout, err, false, Palette{}, "lore doctor")
	line := eout.String()
	if strings.Count(line, "… and 45 more (`lore doctor --json` lists all)") != 2 || strings.Contains(line, ids[5]) || !strings.Contains(line, " are stale") {
		t.Errorf("error line not capped: %q", line)
	}
	out.Reset()
	printError(&out, &eout, err, true, Palette{}, "lore doctor")
	if !strings.Contains(out.String(), ids[49]) {
		t.Errorf("--json error lost items: %q", out.String())
	}
}
