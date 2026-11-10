package importscan

// account.go — which setup covers a GitHub account Home would suggest a
// new setup for (issue 6, decided 2026-11-08 on the owner's delegation:
// option a). The same three states the import proposals know, by login:
//
//	live     a setup in setups[] works as the login     Home suggests nothing (DiscoveredAccount.UsedBy)
//	trashed  a trash.setups entry works as it           Home offers "Restore <name>"
//	purged   a trash.purged entry's github_sha is it    Home hides the suggestion with a Covered line
//	         (an entry from before github_sha: the      and a "Suggest again" action
//	         same name as the suggested setup)
//
// A live setup wins over a trashed one, a trashed one over a purged one:
// what can still be used or restored is offered before what is gone.
// Read-only: config.yaml is read, nothing is written.

import (
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// AccountState is how a setup covers a GitHub account (closed set).
type AccountState string

// Account states, strongest first.
const (
	AccountLive    AccountState = "live"
	AccountTrashed AccountState = "trashed"
	AccountPurged  AccountState = "purged"
)

// AccountCover is the setup that covers one account and the Covered line
// that names it.
type AccountCover struct {
	State AccountState
	// Setup is the covering setup's name; PurgedAt the purge time of a
	// purged one (RFC 3339, "" otherwise: the "Suggest again" choice is
	// kept per purge, so a later purge of a new setup hides it again).
	Setup    string
	PurgedAt string
	Line     string
}

// AccountCovers is config.yaml's setups, trash and tombstones, ready to
// answer For.
type AccountCovers struct {
	setups []existingSetup
	purged []purgedSetup
}

// ReadAccountCovers reads <loreHome>/config.yaml; a missing or broken file
// covers nothing (setup.list and doctor report it).
func ReadAccountCovers(loreHome string) AccountCovers {
	var res Result
	cfg := readConfig(filepath.Join(loreHome, ConfigFile), &res)
	return AccountCovers{setups: cfg.setups, purged: cfg.purged}
}

// For is the setup that covers login, Home's suggested setup name being
// setupName; ok false when none does. A live setup comes before a trashed
// one, a trashed one before a purged one; within a state the first listed
// setup, and of the tombstones the newest (the last appended).
func (c AccountCovers) For(login, setupName string) (AccountCover, bool) {
	login = strings.TrimSpace(login)
	if login == "" {
		return AccountCover{}, false
	}
	for _, want := range []bool{false, true} {
		for _, s := range c.setups {
			if s.trashed == want && worksAs(s, login) {
				if want {
					return AccountCover{State: AccountTrashed, Setup: s.name, Line: s.coveredLine("works as GitHub account " + login)}, true
				}
				return AccountCover{State: AccountLive, Setup: s.name, Line: s.coveredLine("works as GitHub account " + login)}, true
			}
		}
	}
	if p, ok := purgedAccount(c.purged, login, setupName); ok {
		return AccountCover{State: AccountPurged, Setup: p.Name, PurgedAt: p.PurgedAt, Line: purgedAccountLine(p, login, p.GitHubSHA == "")}, true
	}
	return AccountCover{}, false
}

// worksAs reports a setup that works as login: its github.user, else (a
// solo setup with no account) its identity handle, as wire's setupUsers
// reads it.
func worksAs(s existingSetup, login string) bool {
	if u := strings.TrimSpace(s.githubUser); u != "" {
		return strings.EqualFold(u, login)
	}
	return s.kind == string(onboard.KindSolo) && strings.EqualFold(strings.TrimSpace(s.handle), login)
}

// purgedAccount is the newest tombstone of a setup that worked as login:
// its github_sha, else (an entry written before github_sha) its name is
// the suggested setup's name. ok false when none.
func purgedAccount(purged []purgedSetup, login, setupName string) (purgedSetup, bool) {
	sha := onboard.GitHubFingerprint(login)
	for i := len(purged) - 1; i >= 0; i-- {
		p := purged[i]
		switch {
		case p.GitHubSHA != "" && p.GitHubSHA == sha:
			return p, true
		case p.GitHubSHA == "" && setupName != "" && p.Name == setupName:
			return p, true
		}
	}
	return purgedSetup{}, false
}

// SuggestAgainCommand is the CLI way back from a hidden suggestion.
const SuggestAgainCommand = "lore setup suggest-again "

// purgedAccountLine is the Covered line of an account a deleted setup
// used, in purgedLine's shape with this case's way back: "personal
// (deleted for good) used GitHub account alice; Home does not suggest a
// setup for it again (Suggest again, or: lore setup suggest-again alice)".
// byName (a tombstone from before github_sha, matched by its name) says
// "was named like" and adds --setup, which that match needs.
func purgedAccountLine(p purgedSetup, login string, byName bool) string {
	if byName {
		return p.Name + " (deleted for good) was named like the setup for GitHub account " + login +
			"; Home does not suggest a setup for it again (Suggest again, or: " + SuggestAgainCommand + login + " --setup " + p.Name + ")"
	}
	return p.Name + " (deleted for good) used GitHub account " + login +
		"; Home does not suggest a setup for it again (Suggest again, or: " + SuggestAgainCommand + login + ")"
}
