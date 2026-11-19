package wire

// open_noaccess.go — a write to an org store this person's git login
// cannot reach (devco D-726). The org link reader skips such a store
// quietly (adapters/orgcfg/noaccess.go); the backend's first open of it
// refuses with git.AccessRefused. Only wire knows which org lists the
// store, so the refusal is told here in the org's terms: no access to
// <store>, ask an admin of <org>, then the one line to run.

import (
	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
)

// orgNoAccessError rewrites err, the open failure of the store at url,
// when the forge refused this login and an org of this machine lists
// the store; any other error comes back as it is.
func orgNoAccessError(home, url string, err error) error {
	if err == nil || !git.AccessRefused(err) {
		return err
	}
	org := orgOfURL(home, url)
	if org == "" {
		return err
	}
	return errs.Wrap(errs.StoreUnreachable, orgNoAccessMessage(org, url), err).
		WithHint(orgNoAccessHint(org) + " and write again (nothing written)")
}

// orgNoAccessMessage names the store and the true cause: a login without
// access (or a store that is gone), not an outage.
func orgNoAccessMessage(org, url string) string {
	short := orgconf.Store{URL: url}.ShortName()
	return "no access to " + short + " (" + repourl.ShownStore(url) + "): your git login cannot read it, or it is gone; " +
		org + "'s other stores read and write as normal"
}

// orgNoAccessHint is the next step: who to ask, and the line to run after.
func orgNoAccessHint(org string) string {
	return "Ask an admin of " + org + " for read access to it, or to put you in the store's audience; then run `lore sync`"
}
