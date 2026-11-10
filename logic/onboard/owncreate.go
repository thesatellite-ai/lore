package onboard

// owncreate.go — when a create-only check of the team step (4.6
// org.name_free, org.free) would only find what this same setup made.
//
// Those checks guard the team save: the org name must not be an orgs[]
// entry yet, the store must not hold an org yet. The save then makes both
// exist, so run again they fail on the setup's own org:
//
//	re-check of a saved team step      ─▶ setups[].org == the answer       ─▶ own
//	retry of a save that stopped midway ─▶ orgs[org] for the same store,
//	                                       no setups[] entry uses that org ─▶ own (saving again resumes, ops/team.ApplyAdmin)
//	anything else                       ─▶ a real clash: the check runs
//
// Pure: the caller reads config.yaml and passes what it found.

import "strings"

// OrgClaim is what is known about the org a team step names.
type OrgClaim struct {
	// Org and URL are the step's org and store_url answers.
	Org, URL string
	// SavedOrg is this setup's saved setups[].org ("" while being made).
	SavedOrg string
	// EntryURLs are the store urls of the orgs[Org] entry; EntryFound
	// reports that the entry exists.
	EntryURLs  []string
	EntryFound bool
	// ClaimedByOther reports another setups[] entry with org Org.
	ClaimedByOther bool
}

// Detail texts of an own org (shown as the skipped check's detail).
const (
	ownOrgSaved    = " is this setup's own team"
	ownOrgLeftover = " was left by an earlier save of this step that stopped midway; saving again finishes it"
)

// OwnOrg reports whether check id, a create-only team check, would only
// find this setup's own org (see the file comment), and why. False for
// any other check, and while the answers name no org.
func OwnOrg(id CheckID, c OrgClaim) (string, bool) {
	if !OrgCreateCheck(id) {
		return "", false
	}
	org := strings.TrimSpace(c.Org)
	if org == "" {
		return "", false
	}
	if c.SavedOrg == org {
		return org + ownOrgSaved, true
	}
	if !c.EntryFound || c.ClaimedByOther || strings.TrimSpace(c.URL) == "" {
		return "", false
	}
	for _, u := range c.EntryURLs {
		if SameStoreURL(u, c.URL) {
			return "orgs[" + org + "]" + ownOrgLeftover, true
		}
	}
	return "", false
}

// OrgCreateCheck reports the create-only checks about the team's org
// (org.name_free, org.free), the ones OwnOrg decides.
func OrgCreateCheck(id CheckID) bool { return id == CheckOrgNameFree || id == CheckOrgFree }

// gitSuffix is the optional ".git" ending of a repo url.
const gitSuffix = ".git"

// SameStoreURL compares two store urls, ignoring surrounding space, a
// trailing slash and a trailing .git (the same repo spelled two ways; the
// rule ops/team uses for an org entry's store).
func SameStoreURL(a, b string) bool {
	norm := func(s string) string {
		return strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(s), "/"), gitSuffix)
	}
	return norm(a) != "" && norm(a) == norm(b)
}
