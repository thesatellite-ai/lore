package identity

import "testing"

// TestDecideSourceOfTruth is SOURCE_OF_TRUTH row 1's decision: a top-level
// identity that the setup already holds is a leftover, one the setup lacks
// fields of is filled in, and two different values are a conflict that
// nothing resolves on its own.
func TestDecideSourceOfTruth(t *testing.T) {
	t.Parallel()
	pat := func(name string) Entry { return Entry{Key: "human:pat@example.com", Name: name} }
	for _, tc := range []struct {
		name     string
		top, own Entry
		want     SourceVerdict
	}{
		{"no top level", Entry{}, pat("Pat Two"), SourceNone},
		{"same value", pat("Pat Two"), pat("Pat Two"), SourceSame},
		{"key case only", Entry{Key: "human:PAT@example.com", Name: "Pat Two"}, pat("Pat Two"), SourceSame},
		{"setup has more", Entry{Key: "human:pat@example.com"}, pat("Pat Two"), SourceSame},
		{"empty setup", pat("Pat One"), Entry{}, SourceFill},
		{"key-less setup, same name", pat("Pat Two"), Entry{Name: "Pat Two"}, SourceFill},
		{"owner's case: two names, one key", pat("Pat One"), pat("Pat Two"), SourceConflict},
		{"two keys", Entry{Key: "human:pat@example.com"}, Entry{Key: "human:pat@work.example"}, SourceConflict},
		{"two handles", Entry{Key: "human:pat@example.com", Handle: "pat"}, Entry{Key: "human:pat@example.com", Handle: "pat2"}, SourceConflict},
		{"key-less setup, other name", pat("Pat One"), Entry{Name: "Pat Two"}, SourceConflict},
	} {
		if got := Decide(tc.top, tc.own); got != tc.want {
			t.Errorf("%s: Decide(%v, %v) = %s, want %s", tc.name, tc.top, tc.own, got, tc.want)
		}
	}
}

// TestMergeKeepsTheChosenSide: the kept side wins every field it sets; the
// other only fills what the kept side leaves empty.
func TestMergeKeepsTheChosenSide(t *testing.T) {
	t.Parallel()
	top := Entry{Key: "human:pat@example.com", Name: "Pat One", Handle: "pat"}
	own := Entry{Key: "human:pat@example.com", Name: "Pat Two"}
	if got, want := Merge(top, own, KeepSetup), (Entry{Key: "human:pat@example.com", Name: "Pat Two", Handle: "pat"}); got != want {
		t.Errorf("keep setup = %+v, want %+v", got, want)
	}
	if got, want := Merge(top, own, KeepTop), top; got != want {
		t.Errorf("keep top = %+v, want %+v", got, want)
	}
}

// TestSourceNamesTheBlock: identity show names the setup or org block.
func TestSourceNamesTheBlock(t *testing.T) {
	t.Parallel()
	if got := (Resolved{Step: StepSetup, Setup: "personal"}).Source(); got != "setup identity (config.yaml setups[personal].identity)" {
		t.Errorf("setup source = %q", got)
	}
	if got := (Resolved{Step: StepOrg, Org: "acme"}).Source(); got != "org entry (config.yaml orgs[acme].identity)" {
		t.Errorf("org source = %q", got)
	}
	if got := (Resolved{Step: StepTop}).Source(); got != string(StepTop) {
		t.Errorf("top source = %q", got)
	}
}
