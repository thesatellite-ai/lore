//go:build loretest

package formatgate

// knownUpTo, set at link time in a test build (-X
// github.com/thesatellite-ai/lore/logic/formatgate.knownUpTo=0.1.19), makes
// the binary know only the features of that release and older. Why: a test
// that stamps this tree as an older release (main.version) to play an old
// lore must not know a newer feature, since effective counts a binary as
// at least the release of every feature it knows: a stamped 0.1.19 that
// knew verified_epochs counted as 0.1.57 and wrote past a 0.1.20 floor
// (devco:D-1010). Empty (every release build, and a test build without
// the flag) changes nothing.
var knownUpTo string

func init() {
	if !ValidVersion(knownUpTo) {
		return
	}
	var keep []Feature
	for _, f := range known {
		if !versionBelow(knownUpTo, f.Since) {
			keep = append(keep, f)
		}
	}
	known = keep
}
