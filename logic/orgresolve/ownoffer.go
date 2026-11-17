package orgresolve

// ownoffer.go — the own: pattern guided setup offers an admin (devco
// D-526): an org's own repo that no own: pattern names is a client repo by
// the fail-closed default (D-525), so the admin who sets the team up from
// inside it is offered its remote's owner as an own: pattern.
//
//	admin? ── no ─▶ no offer (members never change lore-org.yaml)
//	hosted remote (host/owner/repo)? ── no ─▶ no offer
//	a repos: or own: pattern, or any link of the org, names it ─▶ no offer
//	                (an explicit client listing is never overridden; own already)
//	else ─▶ offer host/owner/*

import (
	"strings"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// OwnOffer is the own: pattern an admin is offered for an unlisted repo.
type OwnOffer struct {
	// Org is the org entry the pattern goes into.
	Org string
	// Remote is the repo's hosted remote (host/owner/repo).
	Remote string
	// Pattern is the owner's pattern (host/owner/*).
	Pattern string
}

// OwnOfferFor is the own: pattern to offer for a repo with remotes in org
// o, ok=false when there is none to offer. Why each refusal: a member
// cannot change the org's patterns; a remote that is not host/owner/repo
// has no owner to name; a repos: pattern or client link is an explicit
// client listing, which wins over own: and must never be talked away; an
// own: pattern or own link already makes it an own repo.
func OwnOfferFor(o *orgconf.Org, links []Link, remotes []string, admin bool) (OwnOffer, bool) {
	if !admin || o == nil {
		return OwnOffer{}, false
	}
	if ClassifyRemote(o, remotes).Role != RoleNone || len(linksFor(links, o.Name)) > 0 {
		return OwnOffer{}, false
	}
	for _, r := range remotes {
		if p, ok := hostedOwnerPattern(r); ok {
			return OwnOffer{Org: o.Name, Remote: r, Pattern: p}, true
		}
	}
	return OwnOffer{}, false
}

// hostedOwnerPattern is ownerPattern for a hosted remote host/owner/repo; ok is
// false for any other shape (a local path, a nested group, a pattern).
func hostedOwnerPattern(remote string) (string, bool) {
	parts := strings.Split(remote, "/")
	if len(parts) != 3 || !strings.Contains(parts[0], ".") {
		return "", false
	}
	for _, p := range parts {
		if p == "" || strings.ContainsAny(p, "*?[] \\:") {
			return "", false
		}
	}
	return ownerPattern(remote), true
}

// OwnOfferCommand is the command that adds the offered pattern by hand.
func OwnOfferCommand(o OwnOffer) string {
	return "lore org config set --org " + o.Org + " --key own --add " + o.Pattern + " --yes"
}
