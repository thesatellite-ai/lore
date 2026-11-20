//go:build journeys && !windows

package journeys

// orglink_firstwrite_agent_test.go — devco:D-738: in a fresh clone of a
// client's repo, right after `lore org link` made the repo's org folder,
// an agent session's first `lore memory add` hung for good at 0% CPU,
// holding its own store lock. The write's actor row was being written
// under the actor lock, its audit looked up the session row, and that
// lookup made the folder's project row through the same lock again: the
// process waited on itself. Now the first write lands in seconds, the
// project row is made once, and the session row is linked.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/testtime"
)

const (
	fwOrg    = "harbor"
	fwOwn    = "github.com/harbor-co/*"
	fwClient = "github.com/client-co/ledger"
	fwFolder = "client_ledger"
	fwBody   = "the staging ledger resets every sunday night"
)

func TestJourneyOrgLinkFirstAgentWrite(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	lena := w.Persona("Lena Ortiz", "lena@harbor.example").Laptop()
	store := w.Remote("github.com/harbor-co/lore-store")
	tools := jPlainRepo(t, lena, w.Remote("github.com/harbor-co/tools"), filepath.Join(lena.Home, "work", "tools"))
	app := jPlainRepo(t, lena, w.Remote(fwClient), filepath.Join(lena.Home, "work", "ledger"))

	w.Step("day1/org-and-link", func() {
		lena.LoreIn(tools, "store", "init", "--name", "harborstore", "--url", store.SSHURL()).OK(t)
		lena.LoreIn(tools, "org", "create", "--org", fwOrg, "--store", store.SSHURL(), "--own", fwOwn,
			"--key", "human:lena@harbor.example", "--name", "Lena", "--yes").OK(t)
		lena.LoreIn(app, "org", "link", "--org", fwOrg, "--project", fwFolder, "--yes").OK(t).Says(t, "as a CLIENT repo of "+fwOrg)
	})

	w.Step("day1/the-first-agent-write-lands-in-seconds", func() {
		start := time.Now()
		r := jAgentsRun(lena, app, &jClaude, "ses-ledger-1", "memory", "add", "--body", fwBody, "--yes")
		took := time.Since(start)
		r.OK(t).Says(t, fwOrg+"/"+fwFolder)
		if bar := testtime.SpeedBar(60 * time.Second); took > bar {
			t.Fatalf("[devco:D-738] the first agent write after org link took %s (it hung on its own actor lock before)", took)
		}
		out := jAgentsRun(lena, app, &jClaude, "ses-ledger-1", "memory", "list").OK(t).String()
		if !strings.Contains(out, fwBody) {
			t.Fatalf("[devco:D-738] the first agent write is not listed:\n%s", out)
		}
		ses := jAgentsRun(lena, app, &jClaude, "ses-ledger-1", "session", "list").OK(t).String()
		if !strings.Contains(ses, "claude_code") {
			t.Fatalf("[devco:D-738] the agent's first write recorded no session row:\n%s", ses)
		}
	})
}
