package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// TestToolSessionsAreDistinct (devco:M-8): two Claude Code windows of one
// person resolve to one auto agent but two signals sessions, taken from
// each tool's own session id, so each holds its own claims; the same tool
// session always gives the same session, LORE_SESSION still wins, and the
// raw tool session id never reaches the shared signals ref. Before the fix
// both windows were `<stable_key>@<host>`: one WIP slot between them, and
// the second window "resumed" the first one's claim.
func TestToolSessionsAreDistinct(t *testing.T) {
	t.Setenv(identity.EnvSession, "")
	agent := func(sid string) identity.Resolved {
		return identity.Resolved{Kind: identity.KindAgent, StableKey: "agent:auto/claude_code/abcdef",
			Detection: &agentdetect.Result{Tool: "claude_code", SessionID: sid}}
	}
	a1, a2 := coordSession(agent("sess-a1-0000")), coordSession(agent("sess-a2-0000"))
	if a1 == a2 {
		t.Fatalf("two tool sessions share signals session %q", a1)
	}
	if again := coordSession(agent("sess-a1-0000")); again != a1 {
		t.Fatalf("one tool session gave %q then %q", a1, again)
	}
	for _, s := range []string{a1, a2} {
		if strings.Contains(s, "sess-a") || !strings.HasPrefix(s, "agent:auto/claude_code/abcdef#") {
			t.Fatalf("session %q: want the agent key and a hash, never the raw tool id", s)
		}
	}
	if got := coordSession(agent("")); !strings.HasPrefix(got, "agent:auto/claude_code/abcdef"+humanSessionSeparator) {
		t.Fatalf("no tool session id: %q, want the key@host session", got)
	}
	t.Setenv(identity.EnvSession, "named-1")
	if got := coordSession(agent("sess-a1-0000")); got != "named-1" {
		t.Fatalf("LORE_SESSION set: %q, want named-1", got)
	}
}
