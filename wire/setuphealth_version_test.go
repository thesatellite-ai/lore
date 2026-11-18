package wire

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// devco D-595: setup list kept a warning (with a fix) an older lore had
// recorded days before, until setup check ran again. Results made by
// another lore version, or recorded before lore stamped the version, read
// as not checked; results of this version still show.
func TestSetupHealthOtherLoreVersionIsNotCheckedD595(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	now := func() time.Time { return time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC) }
	old, cur := healthApp(home, now), healthApp(home, now)
	old.opts.Version, cur.opts.Version = "0.9.1", "0.9.2"
	run := []onboard.CheckResult{{Check: onboard.CheckIdentityGitEmail, Status: onboard.StatusFail, Level: onboard.LevelWarn, CheckedAt: now()}}
	if err := old.RecordHealth(context.Background(), "northwind", run); err != nil {
		t.Fatal(err)
	}
	if rs, ok := cur.SetupHealth(context.Background(), "northwind"); ok {
		t.Fatalf("results of lore 0.9.1 read by 0.9.2 = %+v, want not checked", rs)
	}
	if _, ok := old.SetupHealth(context.Background(), "northwind"); !ok {
		t.Fatal("results lost for the lore that made them")
	}
	// A file from before the version stamp reads as not checked.
	if err := os.WriteFile(cur.healthPath(), []byte(`{"setups":{"northwind":[{"check":"identity.git_email","status":"fail"}]},"saved":{"northwind":""}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if rs, ok := cur.SetupHealth(context.Background(), "northwind"); ok {
		t.Fatalf("unstamped results = %+v, want not checked", rs)
	}
}
