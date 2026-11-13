package onboard

import "testing"

// TestStoreChecksNotApplicable (W-6): the personal_store step shows no
// store checks for local and none; every other choice keeps them.
func TestStoreChecksNotApplicable(t *testing.T) {
	ans := func(c string) Answers { return Answers{QIDPersonalStore: {Text: c}} }
	for _, c := range []struct {
		step   StepID
		choice string
		want   bool
	}{
		{StepPersonalStore, PersonalStoreLocal, true},
		{StepPersonalStore, PersonalStoreNone, true},
		{StepPersonalStore, PersonalStoreExisting, false},
		{StepPersonalStore, PersonalStoreInherit, false},
		{StepIdentity, PersonalStoreLocal, false},
	} {
		if got := StoreChecksNotApplicable(c.step, ans(c.choice)); got != c.want {
			t.Errorf("%s/%s: %v, want %v", c.step, c.choice, got, c.want)
		}
	}
}
