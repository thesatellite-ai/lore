package wire

// setupprobe_reached.go — store.read's fix for a store this machine has
// read before: never "Create it" (journey solo: on a train, after a key
// switch, the personal store that exists answered "not found").

import (
	"net/url"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// reachedBefore adds the FixReachedBefore variant to a failed store.read
// of a git store this machine has fetched from before (its clone under
// ~/.lore/clones carries git's fetch stamp): the repo existed, so the
// default fix ("Create it: gh repo create …") is wrong whatever git said.
// A variant set already (unreachable, not GitHub) is kept: it is more
// precise. A store the person asks lore to create is never "reached".
func (sp setupProber) reachedBefore(res opssetup.ProbeResult, u string, r opssetup.ProbeRequest) opssetup.ProbeResult {
	if res.Status != onboard.StatusFail || res.Vars[onboard.VarFixVariant] != "" || creating(r) {
		return res
	}
	if storeFetchedBefore(sp.a.opts.Home, u) {
		res.Vars[onboard.VarFixVariant] = string(onboard.FixReachedBefore)
	}
	return res
}

// storeFetchedBefore reports whether the git store at raw has a clone on
// this machine that fetched from it at least once.
func storeFetchedBefore(home, raw string) bool {
	canon, err := gitbackend.CanonicalURL(raw)
	if err != nil {
		return false
	}
	u, err := url.Parse(canon)
	if err != nil {
		return false
	}
	rem, err := gitbackend.RemoteOf(u)
	if err != nil {
		return false
	}
	dir, _, err := gitbackend.CloneDirFor(home, rem, "")
	if err != nil {
		return false
	}
	return gitbackend.FetchedOnce(dir)
}
