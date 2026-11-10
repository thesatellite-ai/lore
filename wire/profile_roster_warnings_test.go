package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestProfileRosterKeepsEngineWarnings: a write through the profile
// roster keeps the engine's warnings (here W_URL_CREDENTIALS: a *_url
// field lost a credential) for the op's output, instead of dropping them
// with the Written; the message never carries the removed text.
func TestProfileRosterKeepsEngineWarnings(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1") // a person runs it: profile writes are refused to agents (TS-4)
	a := orgPeriodsApp(t)
	r, done, err := a.Roster(background(), true)
	defer done()
	if err != nil {
		t.Fatal(err)
	}
	if ws := r.Warnings(); len(ws) != 0 {
		t.Fatalf("fresh roster warnings %q", ws)
	}
	mira, err := ids.ParseAny(periodsHere)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "s3cr3t-token-value"
	u, err := value.TextOf("https://mira:" + secret + "@photos.example.com/mira.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Update(background(), schema.TableActors, mira, map[string]value.Value{schema.FieldActorPhotoURL: u}); err != nil {
		t.Fatal(err)
	}
	ws := r.Warnings()
	if len(ws) != 1 || !strings.HasPrefix(ws[0], "W_URL_CREDENTIALS "+schema.FieldActorPhotoURL+": ") || strings.Contains(ws[0], secret) {
		t.Fatalf("warnings %q", ws)
	}
}
