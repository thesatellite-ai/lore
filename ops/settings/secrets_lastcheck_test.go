package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/wire"
)

// verifiedEvents counts the secret.verified lines of the repo's audit log
// (one per vault entry a check opened: the Reveal count).
func verifiedEvents(t *testing.T, h *opstest.H) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.Repo, filepath.FromSlash(wire.AuditFile)))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Count(string(b), `"action":"secret.verified"`)
}

// TestSecretsLastCheck (later 50, SECRETS 2.6/15, FA-AGENT-3): before any
// check a plain list shows no state; `secrets check` stores present per
// entry; a later plain list shows "present (checked <date>)" from that
// stored state without opening a vault (no new secret.verified event, and
// still present after the entry went away); `list --missing` opens the
// vault and stores missing, which the next plain list then shows. The
// stored date is the command clock's (LORE_NOW in a test build).
func TestSecretsLastCheck(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	t.Setenv("LORE_TEST_LASTCHECK", secretDummy)
	// The check stamps the command clock, never the wall clock (BUGFIX-14):
	// a fixed instant years away from today shows any time.Now leak.
	at := time.Date(2031, 3, 4, 10, 0, 0, 0, time.UTC)
	h := multiStore(t, opstest.Fixed(at)) // a store that may hold secret references (a repo may be a client repo)
	h.MustYes("memory.add", `{"body":"db: ${secret:env://LORE_TEST_LASTCHECK}","to":"kb"}`)
	today := at.Format(time.DateOnly)

	before := secretsOf(t, h, settings.OpSecretsList, ``)
	if txt := listText(h); before.Count != 1 || before.Data[0].State != "" || before.Data[0].CheckedAt != "" || strings.Contains(txt, "last lore secrets check") {
		t.Fatalf("list before any check: %+v\n%s", before.Data, txt)
	}
	if n := verifiedEvents(t, h); n != 0 {
		t.Fatalf("a plain list opened a vault: %d secret.verified", n)
	}
	if chk := secretsOf(t, h, settings.OpSecretsCheck, ``); chk.Data[0].State != "present" {
		t.Fatalf("check: %+v", chk.Data)
	}
	opened := verifiedEvents(t, h)
	if opened != 1 {
		t.Fatalf("check opened %d entries, want 1", opened)
	}

	t.Setenv("LORE_TEST_LASTCHECK", "") // the entry is gone now; only a check would see it
	list := secretsOf(t, h, settings.OpSecretsList, ``)
	if txt := listText(h); list.Data[0].State != "present" || list.Data[0].CheckedAt != today ||
		!strings.Contains(txt, "present (checked "+today+")") || !strings.Contains(txt, "last lore secrets check on this machine, "+today) {
		t.Fatalf("plain list after check: %+v\n%s", list.Data, txt)
	}
	if n := verifiedEvents(t, h); n != opened {
		t.Fatalf("a plain list opened a vault: %d secret.verified, had %d", n, opened)
	}

	if miss := secretsOf(t, h, settings.OpSecretsList, `{"missing":true}`); miss.Count != 1 || miss.Data[0].State != "missing" {
		t.Fatalf("--missing: %+v", miss.Data)
	}
	if after, txt := secretsOf(t, h, settings.OpSecretsList, ``), listText(h); after.Data[0].State != "missing" || !strings.Contains(txt, "missing (checked "+today+")") {
		t.Fatalf("plain list after --missing: %+v\n%s", after.Data, txt)
	}
}

// listText is the human form of a plain `lore secrets list`.
func listText(h *opstest.H) string { return opstest.Text(h.Must(settings.OpSecretsList, ``)) }
