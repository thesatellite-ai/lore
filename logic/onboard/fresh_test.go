package onboard

import (
	"testing"
	"time"
)

// FreshChecks: only results of the same answers, younger than the TTL and
// with a kept status are reused; a failure, a stale or a future-dated
// result, or changed answers run again.
func TestFreshChecks(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	ans := Answers{QIDStoreURL: {Text: "git@github.com:octo-user/lore-me.git"}}
	at := func(ago time.Duration) time.Time { return now.Add(-ago) }
	prev := Progress{Answers: ans, Checks: []CheckResult{
		{Check: CheckStoreRead, Status: StatusOK, CheckedAt: at(12 * time.Second)},
		{Check: CheckStoreWrite, Status: StatusFail, CheckedAt: at(5 * time.Second)},
		{Check: CheckStoreMarker, Status: StatusWarn, CheckedAt: at(30 * time.Second)},
		{Check: CheckStorePrivate, Status: StatusOK, CheckedAt: at(CheckFreshTTL)},
		{Check: CheckStoreURL, Status: StatusOK, CheckedAt: now.Add(time.Minute)},
		{Check: CheckSetupConfig, Status: StatusOK},
	}}
	got := FreshChecks(prev, ans, now, StatusOK, StatusSkip, StatusWarn)
	if len(got) != 2 || got[CheckStoreRead].Status != StatusOK || got[CheckStoreMarker].Status != StatusWarn {
		t.Fatalf("fresh = %+v", got)
	}
	if got := FreshChecks(prev, ans, now, StatusOK); len(got) != 1 {
		t.Errorf("ok only = %+v", got)
	}
	changed := Answers{QIDStoreURL: {Text: "git@github.com:octo-user/other.git"}}
	if got := FreshChecks(prev, changed, now, StatusOK); len(got) != 0 {
		t.Errorf("changed answers reused %+v", got)
	}
	if got := FreshChecks(Progress{Checks: prev.Checks}, nil, now, StatusOK); len(got) != 1 {
		t.Errorf("nil vs empty answers: %+v", got)
	}
	// A list answer that went through the Store as [] equals one held as nil.
	listNil := Answers{QIDFolders: {IsList: true}}
	listEmpty := Answers{QIDFolders: {IsList: true, List: []string{}}}
	if got := FreshChecks(Progress{Answers: listEmpty, Checks: prev.Checks}, listNil, now, StatusOK); len(got) != 1 {
		t.Errorf("[] vs nil list: %+v", got)
	}
}

func TestStoreURLMissing(t *testing.T) {
	a := func(choice, url string) Answers {
		return Answers{QIDPersonalStore: {Text: choice}, QIDStoreURL: {Text: url}}
	}
	for _, c := range []struct {
		step StepID
		a    Answers
		want bool
	}{
		{StepPersonalStore, a(PersonalStoreExisting, ""), true},
		{StepPersonalStore, a(PersonalStoreCreate, "  "), false}, // create proposes a repo (CreateWithoutURL)
		{StepPersonalStore, a(PersonalStoreExisting, "git@github.com:octo-user/lore-me.git"), false},
		{StepPersonalStore, a(PersonalStoreInherit, ""), false},
		{StepPersonalStore, a(PersonalStoreLocal, ""), false},
		{StepTeam, a(PersonalStoreExisting, ""), false},
	} {
		if got := StoreURLMissing(c.step, c.a); got != c.want {
			t.Errorf("%s %+v = %v", c.step, c.a, got)
		}
	}
	if !IsStoreCheck(CheckStoreRead) || IsStoreCheck(CheckGitHubSSHAuth) {
		t.Error("IsStoreCheck")
	}
}

func TestCreateWithoutURL(t *testing.T) {
	a := func(choice, url string) Answers {
		return Answers{QIDPersonalStore: {Text: choice}, QIDStoreURL: {Text: url}}
	}
	for _, c := range []struct {
		a    Answers
		want bool
	}{
		{a(PersonalStoreCreate, ""), true},
		{a(PersonalStoreCreate, "  "), true},
		{a(PersonalStoreCreate, "git@github.com:octo-user/lore-me.git"), false},
		{a(PersonalStoreExisting, ""), false},
		{a(PersonalStoreLocal, ""), false},
		{Answers{}, false},
	} {
		if got := CreateWithoutURL(c.a); got != c.want {
			t.Errorf("CreateWithoutURL(%v) = %v, want %v", c.a, got, c.want)
		}
	}
}
