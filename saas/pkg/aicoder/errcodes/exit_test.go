package errcodes

import "testing"

// The exit statuses are a contract with agents and scripts (LORE_TEAM_SPEC
// §1 "Error codes"): this table is the spec's table, checked against the
// spec by hand, not copied from the implementation.
func TestExitCode_SpecTable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		code Code
		want int
	}{
		{NoWork, 10}, {Contended, 11}, {LostClaim, 12}, {Cancelled, 13}, {Reassigned, 14},
		{Offline, 15}, {AmbiguousID, 16}, {OrgAmbiguous, 17}, {OrgDisagrees, 17},
		{LinkUnknown, 18}, {NotAStore, 19}, {LeakBlocked, 19}, {Cycle, 19}, {OldLore, 19},
		// codes without a dedicated status keep the historical generic one
		{ConfigInvalid, ExitGeneric}, {NotFound, ExitGeneric}, {Internal, ExitGeneric},
		{Code("E_NOT_REGISTERED"), ExitGeneric},
	} {
		if got := ExitCode(tc.code); got != tc.want {
			t.Errorf("ExitCode(%s) = %d, want %d", tc.code, got, tc.want)
		}
	}
}

// Every dedicated status belongs to a registered code; a typo'd key in the
// map would otherwise silently never fire.
func TestExitCode_OnlyRegisteredCodes(t *testing.T) {
	t.Parallel()
	registered := map[Code]bool{}
	for _, c := range All() {
		registered[c] = true
	}
	for c := range exitCodes {
		if !registered[c] {
			t.Errorf("exit status mapped for unregistered code %s", c)
		}
	}
}
