package onboard

// fresh.go — when a step's earlier check results may stand in for a probe
// (owner 2026-11-07: Save & next re-ran all five store checks seconds
// after a fix had re-checked them). Pure: the caller supplies the clock.

import (
	"bytes"
	"strings"
	"time"
)

// CheckFreshTTL is how long a check result counts as fresh for the same
// answers. Short on purpose: a reused result never hides a change on disk
// or on GitHub for longer than this; past it the check runs again.
const CheckFreshTTL = 2 * time.Minute

// FreshChecks is the results of prev that may be reused now for answers:
// prev ran against the same answers, ran less than CheckFreshTTL ago (and
// not in the future), and its status is one of keep. A failed or errored
// check is never in keep for a save gate (it runs again: the cause may be
// fixed). Keyed by check id; empty when nothing may be reused.
func FreshChecks(prev Progress, answers Answers, now time.Time, keep ...Status) map[CheckID]CheckResult {
	out := map[CheckID]CheckResult{}
	if len(prev.Checks) == 0 || !sameAnswers(prev.Answers, answers) {
		return out
	}
	for _, c := range prev.Checks {
		age := now.Sub(c.CheckedAt)
		if c.CheckedAt.IsZero() || age < 0 || age >= CheckFreshTTL {
			continue
		}
		for _, k := range keep {
			if c.Status == k {
				out[c.Check] = c
				break
			}
		}
	}
	return out
}

// sameAnswers compares two answer sets by their JSON form (what the
// progress row stores), so a list that went through the Store as [] and
// one held as nil, or nil and empty sets, are the same answers.
func sameAnswers(a, b Answers) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		ja, errA := va.MarshalJSON()
		jb, errB := vb.MarshalJSON()
		if errA != nil || errB != nil || !bytes.Equal(ja, jb) {
			return false
		}
	}
	return true
}

// SkipNoStoreURL is the detail of a store check left unrun because the
// step's repo url field is empty.
const SkipNoStoreURL = "pick or paste the repo URL first"

// SkipNoStoreNeeded is the detail of a store check skipped because the
// personal_store choice (local, none) has no store url to check (W-6).
const SkipNoStoreNeeded = "not needed: this choice has no store url to check"

// SkipJoinedAlready is the detail of a join-step check of a member setup
// that already joined its team, checked or saved again with no invite (an
// edit): the invite line is used once and never kept, so there is nothing
// to check, and `lore doctor --all` checks the team store itself.
const SkipJoinedAlready = "joined already: the invite was used when this setup joined; lore doctor --all checks the team store"

// StoreURLMissing reports a personal store step answered "use an existing
// repo" with an empty repo url: its store checks must not run (they would
// probe a url the person cannot see, e.g. one saved earlier, while the
// field looks empty; owner 2026-11-07). "create" with an empty url is not
// missing: the checks run against the repo lore proposes to create
// (CreateWithoutURL), whose store.read fix is Run it: gh repo create.
func StoreURLMissing(step StepID, a Answers) bool {
	return step == StepPersonalStore && a[QIDPersonalStore].Text == PersonalStoreExisting &&
		strings.TrimSpace(a[QIDStoreURL].Text) == ""
}

// StoreChecksNotApplicable reports a personal_store choice no store url
// belongs to: a local folder lore makes itself, or none (W-6). Its store.*
// checks are skipped with SkipNoStoreNeeded rather than "no store url
// yet", which read as something missing.
func StoreChecksNotApplicable(step StepID, a Answers) bool {
	if step != StepPersonalStore {
		return false
	}
	switch strings.TrimSpace(a[QIDPersonalStore].Text) {
	case PersonalStoreLocal, PersonalStoreNone:
		return true
	}
	return false
}

// CreateWithoutURL reports answers that pick "create a new private repo"
// and leave the repo url empty: lore proposes the repo (<user>/lore-me,
// else the next free lore-me-N) as the store checks' target.
func CreateWithoutURL(a Answers) bool {
	return a[QIDPersonalStore].Text == PersonalStoreCreate && strings.TrimSpace(a[QIDStoreURL].Text) == ""
}

// IsStoreCheck reports a check of the store.* family (store.url, read,
// write, marker, private).
func IsStoreCheck(id CheckID) bool { return strings.HasPrefix(string(id), "store.") }
