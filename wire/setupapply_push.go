package wire

// setupapply_push.go — the push mode question of the personal store step
// (ONB 4.4) and the Start a team step (4.6), written where `lore store set`
// keeps it (storepushops.go):
//
//	personal store step, push batch|manual ─▶ config.yaml personal.push (the `me` settings)
//	team step, push batch|manual           ─▶ the team store's lore-store.yaml push key,
//	                                          one commit, after the admin setups[] entry exists
//	push now or no answer                  ─▶ nothing (now is the default)
//
// A mode other than now needs a git store: a local folder (file://) or no
// personal store has no remote, so the step is refused before it writes
// anything, naming the store choice.

import (
	"context"
	neturl "net/url"
	"strconv"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// stepPushSettings is the settings the step's own push answer asks for
// (none for now or no answer). Only the step's answers are read, never
// the merged ones of the whole setup: the personal store step and the
// team step both ask `push`, and one must not set the other's store.
func stepPushSettings(step onboard.StepID, a onboard.Answers) ([]storepush.Assignment, error) {
	raw := answerText(a, onboard.QIDPush)
	set, err := onboard.PushSettings(raw)
	if err != nil {
		return nil, errs.New(errs.InvalidInput, "setup apply "+string(step)+": "+err.Error()).
			WithHint("pick now, batch or manual for " + onboard.QIDPush)
	}
	return set, nil
}

// pushModeOf is the mode set asks for ("" when it asks none).
func pushModeOf(set []storepush.Assignment) string {
	for _, s := range set {
		if s.Key == storepush.KeyPush {
			return s.Value
		}
	}
	return ""
}

// refusePushWithoutRemote refuses a push mode other than now for a
// personal store that has no remote: none, or a file:// folder (url).
func refusePushWithoutRemote(set []storepush.Assignment, choice, url string) error {
	if len(set) == 0 {
		return nil
	}
	why := ""
	if url == "" {
		why = "this setup has no personal store (" + strconv.Quote(choice) + ")"
	} else if u, err := neturl.Parse(url); err == nil && u.Scheme == folder.Scheme {
		why = "the personal store " + url + " is a local folder"
	}
	if why == "" {
		return nil
	}
	return errs.New(errs.InvalidInput, "setup apply personal_store: push "+pushModeOf(set)+" needs a git repo to push to, but "+why+"; nothing was written").
		WithHint("pick push now (or leave it empty), or a repo: create, existing, or same as my other setups")
}

// applyPersonalPush writes set into config.yaml's personal: block unless
// it already reads that way; changed reports a write (or, on a dry run,
// one that would happen).
func (ap setupApplier) applyPersonalPush(set []storepush.Assignment, dryRun bool) (bool, error) {
	if len(set) == 0 {
		return false, nil
	}
	home := ap.a.opts.Home
	cur, err := personalPushRaw(home)
	if err != nil {
		return false, err
	}
	next, err := normalized(cur, set)
	if err != nil {
		return false, err
	}
	if next == cur {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	return true, writePersonalPush(home, next)
}

// applyTeamPush writes set into the lore-store.yaml of org's store at url
// (the store the team step just made), as `lore store set <store>` does:
// one commit, pushed. It runs after the admin setups[] entry is written,
// because only a machine holding an admin setup of org may change the
// store's shared settings (isOrgAdmin).
func (ap setupApplier) applyTeamPush(ctx context.Context, org, url string, set []storepush.Assignment) (bool, error) {
	if len(set) == 0 {
		return false, nil
	}
	home := ap.a.opts.Home
	if !isOrgAdmin(home, org) {
		return false, errNotAdmin("setup apply team", org, org)
	}
	src, err := teamStoreAt(home, org, url)
	if err != nil {
		return false, err
	}
	return ap.a.setOrgStorePushAt(ctx, src, org, set)
}

// teamStoreAt is org's store at url as config.yaml lists it (its branch
// included); the org's only store when none matches url exactly (the
// orgs[] entry may spell the url its own way).
func teamStoreAt(home, org, url string) (attach.Store, error) {
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil {
		return attach.Store{}, err
	}
	e := cfg.FindOrg(org)
	if e == nil || len(e.Stores) == 0 {
		return attach.Store{}, errs.New(errs.NotFound, "setup apply team: org "+org+" has no store in ~/.lore/"+ConfigFile+" to set push on").
			WithHint("run `lore store set --store <store> --settings push=…` once the team step is saved")
	}
	pick := -1
	for i := range e.Stores {
		if e.Stores[i].URL == url {
			pick = i
			break
		}
	}
	if pick < 0 && len(e.Stores) == 1 {
		pick = 0
	}
	if pick < 0 {
		return attach.Store{}, errs.New(errs.NotFound, "setup apply team: org "+org+" lists no store at "+url).
			WithHint("run `lore store set --store <store> --settings push=…` on the team store")
	}
	sc := &e.Stores[pick]
	return attach.Store{Name: store.Source(org), URL: sc.URL, Branch: attachBranch(sc)}, nil
}
