package setup

// discover_again.go — Home's suggestion for a GitHub account whose setup
// was deleted for good (issue 6, option a, decided 2026-11-08 on the
// owner's delegation): setup.discover leaves the account's card in, but
// marks it purged_by + covered, so Home hides "Create <name>" and shows the
// Covered line instead; setup.suggest_again records the person's "Suggest
// again" for that purge, and the card is suggested as before.
//
//	setup.discover ─▶ MergeDiscovery ─▶ per card without used_by:
//	                    AccountCoverer.AccountCover(login, setup_name)
//	                    purged and not suggested again ─▶ PurgedBy, Covered
//	setup.suggest_again <login> ─▶ AccountCoverer.SuggestAgain ─▶ ~/.lore/state (per purge)
//
// The choice is kept per purge (login + purged_at), so purging a newer
// setup for the same account hides it again. A live setup (used_by) and a
// trashed one (Home's "Restore") come first; only the purged state hides.

import (
	"context"
	"regexp"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// OpSuggestAgain is the op name (page allowlist).
const OpSuggestAgain = "setup.suggest_again"

// CoverPurged is AccountCoverFact.State of a setup deleted for good (the
// only state that hides a suggestion; adapters/importscan.AccountPurged).
const CoverPurged = "purged"

// reGitHubLogin is a GitHub login (alphanumerics and single hyphens, at
// most 39; adapters/probe's rule): the state file never holds anything
// else.
var reGitHubLogin = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)

// AccountCoverFact is the setup that covers one GitHub account.
type AccountCoverFact struct {
	// State is live, trashed or purged (adapters/importscan.AccountState).
	State string
	Setup string
	// PurgedAt is a purged setup's purge time ("" otherwise).
	PurgedAt string
	// Line is the Covered line naming the setup and the way back.
	Line string
	// Again: the person chose "Suggest again" for this purge.
	Again bool
}

// AccountCoverer is the optional Onboard upgrade behind both (wire's App
// has it): config.yaml's setups, trash and tombstones, and the recorded
// choices.
type AccountCoverer interface {
	// AccountCover is the setup that covers login (setupName is the name
	// Home would suggest: a tombstone written before github_sha matches by
	// it); ok false when none.
	AccountCover(ctx context.Context, login, setupName string) (AccountCoverFact, bool)
	// RecordSuggestAgain remembers "Suggest again" for login's purge at
	// purgedAt (idempotent).
	RecordSuggestAgain(ctx context.Context, login, purgedAt string) error
}

// SuggestAgainIn is setup.suggest_again's input.
type SuggestAgainIn struct {
	Login string `json:"login" arg:"0" desc:"the GitHub login Home stopped suggesting a setup for"`
	Setup string `json:"setup,omitempty" flag:"setup" desc:"the setup name Home suggested (needed only for a setup purged by a lore before 0.1.18; the Covered line names it)"`
}

// SuggestAgainOut is setup.suggest_again's output.
type SuggestAgainOut struct {
	Login string `json:"login"`
	// Purged is the deleted setup that had hidden the suggestion.
	Purged   string `json:"purged"`
	PurgedAt string `json:"purged_at"`
}

// SuggestAgain is setup.suggest_again.
func (e engine) SuggestAgain(ctx context.Context, in SuggestAgainIn) (SuggestAgainOut, error) {
	login := strings.TrimSpace(in.Login)
	if !reGitHubLogin.MatchString(login) {
		return SuggestAgainOut{}, errs.New(errs.InvalidInput, "setup suggest-again: login must be a GitHub login").
			WithHint("lore setup discover --json lists the accounts; the hidden one has purged_by")
	}
	c, ok := e.env.(AccountCoverer)
	if !ok {
		return SuggestAgainOut{}, errs.New(errs.Unsupported, "this lore cannot remember a suggestion choice")
	}
	f, found := c.AccountCover(ctx, login, strings.TrimSpace(in.Setup))
	if !found || f.State != CoverPurged {
		return SuggestAgainOut{}, errs.New(errs.NotFound, "setup suggest-again: no setup deleted for good used GitHub account "+login).
			WithHint("Home hides only an account whose setup was purged; lore setup discover --json shows purged_by on that card")
	}
	if err := c.RecordSuggestAgain(ctx, login, f.PurgedAt); err != nil {
		return SuggestAgainOut{}, err
	}
	return SuggestAgainOut{Login: login, Purged: f.Setup, PurgedAt: f.PurgedAt}, nil
}

// markPurged sets PurgedBy and Covered on every card a setup deleted for
// good covers and no "Suggest again" brought back. A card some live setup
// uses (UsedBy) is left alone, and so is one a trashed or live setup
// covers (AccountCover answers the strongest state).
func markPurged(ctx context.Context, c AccountCoverer, cards []DiscoveredAccount) {
	for i := range cards {
		if cards[i].UsedBy != "" {
			continue
		}
		f, ok := c.AccountCover(ctx, cards[i].Login, cards[i].SetupName)
		if !ok || f.State != CoverPurged || f.Again {
			continue
		}
		cards[i].PurgedBy, cards[i].Covered = f.Setup, f.Line
	}
}
