package main

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/formatgate"
)

func TestReadOnlyLine(t *testing.T) {
	if got := readOnlyLine(formatgate.Check("0.1.18", formatgate.Declared{Features: []string{formatgate.Live}})); got != "" {
		t.Fatalf("writable: %q", got)
	}
	unk := readOnlyLine(formatgate.Check("0.1.18", formatgate.Declared{Features: []string{"zz_future"}}))
	if !strings.Contains(unk, "zz_future") || !strings.HasPrefix(unk, "lore: read-only here") {
		t.Fatalf("unknown: %q", unk)
	}
	floor := readOnlyLine(formatgate.Check("0.1.18", formatgate.Declared{MinLoreVersion: "9.0.0"}))
	if !strings.Contains(floor, "needs lore 9.0.0") {
		t.Fatalf("floor: %q", floor)
	}
	if readNoticeVerb("restore") || !readNoticeVerb("list") || readNoticeVerb("add") {
		t.Fatal("readNoticeVerb")
	}
}
