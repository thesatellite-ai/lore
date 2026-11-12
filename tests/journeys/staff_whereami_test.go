//go:build journeys && !windows

package journeys

// staff_whereami_test.go — what a lead reads in plain text (B1-3, B1-4).
// Lee starts the org; in a plain folder under the setup's folders (not a
// git repo) whoami names the place instead of calling it a client repo,
// and the setup page's steps, saved again without --json, print readable
// tables: no JSON object in a cell, no nanosecond time.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// tlNanoTime is a time printed to the nanosecond.
var tlNanoTime = regexp.MustCompile(`T\d\d:\d\d:\d\d\.\d{6,9}`)

func TestJourneyStaffWhereAmI(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)

	w.Step("day1/whoami-in-a-plain-folder", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		plain := filepath.Join(lee.Home, "code", "scratch")
		if err := os.MkdirAll(plain, 0o755); err != nil {
			t.Fatal(err)
		}
		lee.Lore(plain, "whoami").OK(t).
			Says(t, "org        "+tlOrg, "repo       not a git repo  (treated as a client repo: guards on)").
			Never(t, "repo       client repo")
		tt.lore(tlLee, "whoami").OK(t).Never(t, "not a git repo", "no hosted remote")
	})

	w.Step("day1/setup-steps-in-plain-text", func() {
		r := lee.Lore(tt.repo[tlLee.Email].Dir, "setup", "apply", tlLeadSetup, "--step", "kind", "--answers", `{"kind":"admin","name":"`+tlLeadSetup+`"}`, "--yes").OK(t).
			Says(t, "checks:", "FIX", "CHECKED_AT").
			Never(t, `{"text"`, `"runnable"`, "null")
		if tlNanoTime.MatchString(r.Stdout) {
			t.Fatalf("a time to the nanosecond in human output:\n%s", r.Stdout)
		}
	})
}
