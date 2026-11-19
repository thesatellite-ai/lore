package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/auditfill"
)

// TestActorNamerUnknown: the reserved unknown actor (a row written before
// audit fields, filled by auditfill) shows as "unknown" in human text,
// never as its raw act_0000… id; no store holds a row for it, so a store
// lookup alone printed the id (devco K-2).
func TestActorNamerUnknown(t *testing.T) {
	t.Parallel()
	a := openWith(t, gitRepo(t), Options{})
	withProject(t, a)
	if got := a.ActorNamer(background())(auditfill.UnknownActor().String()); got != auditfill.UnknownName {
		t.Fatalf("ActorNamer(unknown) = %q, want %q", got, auditfill.UnknownName)
	}
}
