package setup

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestHealthCountsEachCheckOnce (B1-25): a check two steps ran (the
// identity step's git email check, run again later) counts once, the
// newest result; Problems lists exactly what the cell counts, notes and
// passes left out.
func TestHealthCountsEachCheckOnce(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)
	warn := onboard.CheckResult{Check: onboard.CheckIdentityGitEmail, Status: onboard.StatusWarn, Level: onboard.LevelWarn, Detail: "git config user.email is x", CheckedAt: t0}
	again := warn
	again.CheckedAt = t0.Add(time.Minute)
	note := onboard.CheckResult{Check: onboard.CheckStorePrivate, Status: onboard.StatusWarn, Level: onboard.LevelInfo, CheckedAt: t0}
	ok := onboard.CheckResult{Check: onboard.CheckStoreRead, Status: onboard.StatusOK, Level: onboard.LevelError, CheckedAt: t0}
	rs := []onboard.CheckResult{warn, note, ok, again}
	if got := HealthCell(rs); got != healthWarn+" 1" {
		t.Fatalf("HealthCell = %q, want %q (one warning, counted once)", got, healthWarn+" 1")
	}
	p := Problems(Latest(rs))
	if len(p) != 1 || p[0].Check != onboard.CheckIdentityGitEmail || !p[0].CheckedAt.Equal(again.CheckedAt) {
		t.Fatalf("Problems = %+v, want the newest git email warning only", p)
	}
	if p := Problems([]onboard.CheckResult{ok, note}); p != nil {
		t.Fatalf("Problems of a healthy setup = %#v, want nil (no empty table in setup show)", p)
	}
}
