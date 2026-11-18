package orgresolve

// classify.go — the one rule that says whether a repo of an org is one of
// its own repos or a client's (devco D-428).
//
// Why one function: `lore org link` decided "own" from the own: patterns
// alone while F10 rule 5 (whoami, the write path) let a repos: client
// entry win, so linking a client repo listed under repos: but also
// matching a wide own: pattern (github.com/agency/* with
// github.com/agency/shopapp a client's) wrote an own link and turned the
// client leak guards off. Every path that sets or reads own/client (link,
// relink on a new remote, the store link reader, F10) asks ClassifyRemote
// or EffectiveOwn, so linking can never weaken the guards (decision D5:
// an explicit client entry wins; ambiguous is client, fail closed).

import (
	"errors"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// RemoteClass is what an org's patterns say about a set of remotes.
type RemoteClass struct {
	// Role is RoleClient when a repos: pattern matches (it wins over own:),
	// RoleOwn when only an own: pattern matches, RoleNone when neither does.
	Role Role
	// Pattern is the pattern that decided ("" for RoleNone).
	Pattern string
}

// ClassifyRemote applies o's repos: and own: patterns to remotes: a repos:
// (client) match wins over an own: match, so a repo the org lists as a
// client's stays a client repo however wide its own: patterns are.
func ClassifyRemote(o *orgconf.Org, remotes []string) RemoteClass {
	if o == nil {
		return RemoteClass{Role: RoleNone}
	}
	if p := firstMatch(o.Repos, remotes); p != "" {
		return RemoteClass{Role: RoleClient, Pattern: p}
	}
	if p := firstMatch(o.Own, remotes); p != "" {
		return RemoteClass{Role: RoleOwn, Pattern: p}
	}
	return RemoteClass{Role: RoleNone}
}

// EffectiveOwn is whether a stored link of o at remote counts as an own
// link: recorded own and not listed under o's repos:. A link written as
// own before the client entry was added (or by a lore older than D-428)
// is read as a client link, so the guards come back on without anyone
// rewriting the store.
func EffectiveOwn(o *orgconf.Org, remote string, recorded bool) bool {
	return recorded && ClassifyRemote(o, []string{remote}).Role != RoleClient
}

// ErrLinkClientListed is the sentinel of a `--own` link refused because
// the org lists the repo as a client's (errors.Is through LinkRefusal).
var ErrLinkClientListed = errors.New("--own is refused for a repo the org lists as a client's (repos: pattern)")

// clampLinks applies EffectiveOwn to every link of a known entry, so F10
// decides by the same rule whatever LinkSource produced the links.
func clampLinks(cfg *orgconf.Config, links []Link) []Link {
	if len(links) == 0 {
		return links
	}
	out := make([]Link, len(links))
	for i, l := range links {
		if l.Own {
			if o := cfg.FindOrg(l.Entry); o != nil {
				l.Own = EffectiveOwn(o, l.Remote, true)
			}
		}
		out[i] = l
	}
	return out
}

// overlapShown is how many overlapped repos entries an overlap line names
// before it says how many more there are.
const overlapShown = 3

// OwnRepoOverlaps is one line per own: pattern that also matches entries
// of repos: (devco D-585, owner use pass 2 Weak 13): an org whose own
// github.com/acme/* covers 65 exact client entries classified each of
// them right (ClassifyRemote: repos wins, fail closed) but never said so.
// The line names the pattern, how many repos entries it overlaps (a few
// by name) and which rule wins. A pattern that does not compile is
// skipped (org config set refuses it before it is written).
func OwnRepoOverlaps(own, repos []string) []string {
	var out []string
	for _, o := range own {
		p, err := repourl.Compile(o)
		if err != nil {
			continue
		}
		var hit []string
		for _, r := range repos {
			if p.Match(r) {
				hit = append(hit, r)
			}
		}
		if len(hit) == 0 {
			continue
		}
		names := strings.Join(hit[:min(len(hit), overlapShown)], ", ")
		if len(hit) > overlapShown {
			names += fmt.Sprintf(" and %d more", len(hit)-overlapShown)
		}
		entries := "entries"
		if len(hit) == 1 {
			entries = "entry"
		}
		out = append(out, fmt.Sprintf("own %s overlaps %d repos %s (%s); repos entries win (client), whether exact or a pattern", o, len(hit), entries, names))
	}
	return out
}
