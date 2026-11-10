package formatgate

// notice.go — the read-only notice of FORMAT_EVOLUTION rule 2 ("read-only
// on every path"): a read command run by a lore that may not write the
// project prints one stderr line and succeeds. The text is one function
// here so both binaries print the same line (saas/cmd/cli format_gate.go
// readOnlyLine, cmd/lore through wire.App.ReadOnlyNotice).

import "strings"

// ReadOnlyLine is the notice for v ("" when v is writable): which
// features or floor block, and the fix (`<binary> upgrade`).
func ReadOnlyLine(v Verdict, binary string) string {
	if v.Writable {
		return ""
	}
	var why string
	switch {
	case len(v.Unknown) > 0:
		why = "feature " + strings.Join(v.Unknown, ", ") + " is unknown to this lore"
		if v.Floor != "" {
			why += "; needs lore " + v.Floor
		}
	default:
		why = "needs lore " + v.Floor + "; this is " + strings.TrimPrefix(strings.TrimSpace(v.Binary), "v")
	}
	return "lore: read-only here (" + why + "); reading works, writing is off until you run `" + binary + " upgrade`"
}
