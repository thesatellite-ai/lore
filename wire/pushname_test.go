package wire

import (
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
)

// TestPushName (bug hunt W-14): a push is named by the folders whose rows
// it carried, not by the folder that happened to push.
func TestPushName(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, own string
		folders   []string
		want      string
	}{
		{"acme/core", "core", nil, "acme/core"},
		{"acme/core", "core", []string{"core"}, "acme/core"},
		{"acme/core", "core", []string{"sync"}, "acme/sync"},
		// devco D-601: several folders are one push of the whole clone
		{"acme/core", "core", []string{"core", "sync"}, "acme (the store clone: folders core, sync)"},
		{"acme/org", "org", []string{".", "org", "web"}, "acme (the store clone: folders the store root, org, web)"},
		{"acme", ".", []string{"."}, "acme"},
		{"acme/org", "org", []string{"."}, "acme"},
	} {
		if got := pushName(c.name, c.own, c.folders); got != c.want {
			t.Errorf("pushName(%q, %q, %v) = %q, want %q", c.name, c.own, c.folders, got, c.want)
		}
	}
}

// TestPushLineCounts (devco D-601): the push line carries the commit and
// row-file counts git gave for the push, and none when git gave none.
func TestPushLineCounts(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		p    gitbackend.Pushed
		want string
	}{
		{gitbackend.Pushed{Commit: "6228e83aa", Branch: "main", Commits: 18, Rows: 25}, "lore: pushed store acme: commit 6228e83 to branch main (18 commits, 25 row files)"},
		{gitbackend.Pushed{Commit: "6228e83aa", Branch: "main", Commits: 1, Rows: 1}, "lore: pushed store acme: commit 6228e83 to branch main (1 commit, 1 row file)"},
		{gitbackend.Pushed{Commit: "6228e83aa", Branch: "main", Commits: 2}, "lore: pushed store acme: commit 6228e83 to branch main (2 commits)"},
		{gitbackend.Pushed{Commit: "6228e83aa", Branch: "main"}, "lore: pushed store acme: commit 6228e83 to branch main"},
	} {
		if got := PushLine("acme", c.p); got != c.want {
			t.Errorf("PushLine(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
}
