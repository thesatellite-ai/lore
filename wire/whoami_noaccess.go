package wire

// whoami_noaccess.go — whoami names each store of this folder's org that
// this machine's login could not reach at its last fetch (devco D-726):
// one quiet "skipped" row under the links row, never an error. The store
// is skipped by every read (adapters/orgcfg noaccess.go); the row says
// what to ask an admin, so a member who joined without access to one of
// the org's stores is not left guessing why its notes never show.

import (
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// whoamiNoAccessLabel labels the rows of the skipped stores.
const whoamiNoAccessLabel = "skipped"

// withNoAccessRows puts one "skipped" row per unreachable store of s's
// org after whoami's links row (at the end when there is none).
func withNoAccessRows(home string, s *orgcfg.Situation, lines []string) []string {
	if s == nil || s.Result.Org == nil {
		return lines
	}
	skipped := orgcfg.NoAccessStores(home, s.Config, map[string]bool{s.Result.Org.Name: true})
	if len(skipped) == 0 {
		return lines
	}
	rows := make([]string, 0, len(skipped))
	for i, n := range skipped {
		label := ""
		if i == 0 {
			label = whoamiNoAccessLabel
		}
		rows = append(rows, fmt.Sprintf("%-*s %s", whoamiLabelWidth, label, repourl.MaskText(orgcfg.NoAccessNote(n))))
	}
	at := len(lines)
	for i, l := range lines {
		if strings.HasPrefix(l, fmt.Sprintf("%-*s ", whoamiLabelWidth, "links")) {
			at = i + 1
			break
		}
	}
	return append(lines[:at:at], append(rows, lines[at:]...)...)
}
