package wire

import (
	"context"
	"path/filepath"
	"testing"
)

// TestDoctorReadsTeamVersionsOnce (D-739, public issue #7): under a plain
// doctor run's memo the team-versions read of a cache file runs once,
// however many checks ask (team_versions and actor_roster both did, each
// parsing every row of a 2,400-task project); without the memo every call
// reads the file. The cache is closed after the first read, so a second
// read would fail.
func TestDoctorReadsTeamVersionsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg, err := builtinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	c, err := cacheOpen(ctx, filepath.Join(t.TempDir(), "repo.db"), reg, plainText{})
	if err != nil {
		t.Fatal(err)
	}
	memo := withTeamVersionsMemo(ctx)
	if _, err := cacheTeamVersions(memo, c); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheTeamVersions(memo, c); err != nil {
		t.Fatalf("second read under the memo reached the cache file again: %v", err)
	}
	if _, err := cacheTeamVersions(ctx, c); err == nil {
		t.Fatal("without the memo the read must reach the (closed) cache file")
	}
}
