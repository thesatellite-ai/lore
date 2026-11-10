package onboard

// hints.go — two warnings a setup can be saved with but that make it work
// differently than the person expects (realmac findings N3 and NEW-4,
// owner "fix all" 2026-11-07). Both are pure decisions; wire's prober
// gathers the facts.
//
//	github.identity_email ─▶ IdentityEmailMatch: the identity email is one
//	                         of the GitHub account's emails (warn; "could
//	                         not read" when the token cannot list them)
//	folders.reachable     ─▶ FoldersReachable: lore can pick the setup by
//	                         itself (a folder, the default, or its org)
//
// Neither ever refuses a save: an identity that differs from the account,
// or a setup used only by name, can be intended.

import (
	"strings"
)

// IdentityEmailIn is what github.identity_email decides on.
type IdentityEmailIn struct {
	// Emails are the setup's identity emails, the identity key's first
	// (empty: nothing to compare, skip).
	Emails []string
	// User is the setup's GitHub account.
	User string
	// Readable reports whether the account's emails could be read (the gh
	// token has user:email and the API answered); AccountEmails are
	// they, noreply addresses included.
	Readable      bool
	AccountEmails []string
	// Why is the reason the emails could not be read (shown after
	// "could not read …'s emails"); "" for the generic one.
	Why string
	// Gitconfig are the emails the gitconfig ties to the account (an
	// includeIf identity linked to it, as its discovery card shows them).
	// With an unreadable list, an identity email among them is named as
	// the card's email, not as "could not read" alone (polish m6).
	Gitconfig []string
}

// identityEmailWhyDefault is the reason when the caller gives none.
const identityEmailWhyDefault = "the GitHub API did not list them"

// IdentityEmailMatch decides github.identity_email: StatusOK when one of
// the setup's identity emails is one of the account's (case-insensitive),
// StatusFail (a warning: the check's level is warn) when none is, and
// StatusSkip with a "could not read" detail when the account's emails are
// unknown, so an unreadable list never warns falsely.
func IdentityEmailMatch(in IdentityEmailIn) (Status, string) {
	user := strings.TrimSpace(in.User)
	emails := nonEmpty(in.Emails)
	switch {
	case len(emails) == 0:
		return StatusSkip, "no identity email yet"
	case user == "":
		return StatusSkip, "no GitHub account yet"
	case !in.Readable:
		why := in.Why
		if why == "" {
			why = identityEmailWhyDefault
		}
		for _, e := range emails {
			for _, g := range in.Gitconfig {
				if strings.EqualFold(strings.TrimSpace(g), e) {
					return StatusSkip, e + " is the email your gitconfig uses for " + user + "; GitHub's own list of " + user + "'s emails could not be read (" + why + "), so it is not confirmed there"
				}
			}
		}
		return StatusSkip, "could not read " + user + "'s GitHub emails (" + why + ")"
	}
	for _, e := range emails {
		for _, a := range in.AccountEmails {
			if strings.EqualFold(strings.TrimSpace(a), e) {
				return StatusOK, e + " is one of " + user + "'s GitHub emails"
			}
		}
	}
	listed := "none"
	if len(in.AccountEmails) > 0 {
		listed = strings.Join(in.AccountEmails, ", ")
	}
	return StatusFail, emails[0] + " is not one of " + user + "'s GitHub emails (" + listed + ")"
}

// nonEmpty drops blank entries and trims the rest.
func nonEmpty(list []string) []string {
	var out []string
	for _, s := range list {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// FoldersReachIn is what folders.reachable decides on.
type FoldersReachIn struct {
	Folders []string
	Default bool
	// Org is the setup's org ("" for a solo setup): lore picks a team
	// setup in the repos of its org even without folders (ONB 3.3 step 5).
	Org      string
	Disabled bool
}

// DetailNeverPicked is folders.reachable's warning text (the page shows
// the same words before the check runs).
const DetailNeverPicked = "no folder and not the default: lore never picks this setup by itself (only --setup or LORE_SETUP)"

// FoldersReachable decides folders.reachable: whether lore can choose the
// setup on its own (ONB 3.3: a folder match, its org, or default: true).
// A disabled setup matches nothing on purpose, so it is skipped.
func FoldersReachable(in FoldersReachIn) (Status, string) {
	switch {
	case in.Disabled:
		return StatusSkip, "the setup is disabled; it matches nothing until it is enabled"
	case len(nonEmpty(in.Folders)) > 0:
		return StatusOK, "lore picks it inside its folders"
	case in.Default:
		return StatusOK, "lore picks it when no folder matches (the default)"
	case strings.TrimSpace(in.Org) != "":
		return StatusOK, "lore picks it in repos of org " + strings.TrimSpace(in.Org)
	}
	return StatusFail, DetailNeverPicked
}
