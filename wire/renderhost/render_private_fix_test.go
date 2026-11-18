package renderhost

import (
	"strings"
	"testing"
)

// TestOverCoreFixNamesTheListedRows: the over-budget warning lists the
// largest pinned rows and its fix must name those, largest first, not the
// first pinned rule found (devco D-602: the warning listed three large
// rules and the fix named a fourth, smaller one).
func TestOverCoreFixNamesTheListedRows(t *testing.T) {
	pins := []privatePin{
		{id: "rul_small", kind: privatePinRule, text: strings.Repeat("s", 10)},
		{id: "hfx_small", kind: privatePinHotfix, text: strings.Repeat("h", 20)},
		{id: "rul_big", kind: privatePinRule, text: strings.Repeat("b", 300)},
		{id: "hfx_big", kind: privatePinHotfix, text: strings.Repeat("H", 200)},
		{id: "rul_mid", kind: privatePinRule, text: strings.Repeat("m", 100)},
	}
	list := largestPins(pins, largestPinsShown)
	if want := " The largest: rul_big (300 bytes), hfx_big (200 bytes), rul_mid (100 bytes)."; list != want {
		t.Fatalf("largestPins = %q, want %q", list, want)
	}
	fix := overCoreFix(630, pins)
	for _, want := range []string{"`lore rule edit --id rul_big --severity should`", "`lore hotfix edit --id hfx_big --severity medium`"} {
		if !strings.Contains(fix, want) {
			t.Errorf("fix = %q, want it to name %s", fix, want)
		}
	}
	for _, never := range []string{"rul_small", "hfx_small"} {
		if strings.Contains(fix, never) {
			t.Errorf("fix = %q names %s, a row the warning did not list", fix, never)
		}
	}
	if pins[0].id != "rul_small" {
		t.Fatalf("overCoreFix reordered its input: %v", pins[0].id)
	}
}
