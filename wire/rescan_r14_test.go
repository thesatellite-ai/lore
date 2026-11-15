package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/rowscan"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/ids"
)

// TestRescanAllowLineUsesTheShownFingerprint is devco:R-14: a finding line
// of `lore check leaks --rows` shows the sha256 fingerprint, and its allow
// line must give the same form (it gave the 32-bit number, so one finding
// read as two hashes).
func TestRescanAllowLineUsesTheShownFingerprint(t *testing.T) {
	id, err := ids.ParseAny("mem_01a1a8fe9b48787184aa0b4b6386eef4")
	if err != nil {
		t.Fatal(err)
	}
	match := "made-up-" + strings.Repeat("z", 12)
	f := scan.Finding{RuleID: "github_token", Field: "body", Line: 1, Preview: scan.Mask(match), MatchHash: scan.HashMatch(match)}
	got := rescanReport([]rowscan.Hit{{Table: "memories", Source: "repo", ID: id, Finding: f}})
	if strings.Count(got, f.Preview) != 2 || !strings.Contains(got, "--match-hash "+f.Preview) {
		t.Fatalf("report:\n%s", got)
	}
}
