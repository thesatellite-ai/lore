package wire

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestStoreClaimNeedsPush is devco:D-580: a claim in a linked repo went to
// the store's signals ref on the store's remote without --push, although
// --push says a claim there is refused without it. It is refused now,
// writing nothing, and --push (or the standing go-ahead) lets it through.
func TestStoreClaimNeedsPush(t *testing.T) {
	t.Setenv(envPushClaims, "")
	t.Setenv(envLiveAutopushSwitch, "")
	w := &coordWorld{spot: sigSpot{store: true, label: "acme/web", ref: "refs/lore/signals/main", remote: &gitremote.Git{}}}
	err := w.requirePushAsked(false, "a claim")
	var e *errs.Error
	if err == nil || !errors.As(err, &e) || e.Code != errs.Refused {
		t.Fatalf("a store claim without --push = %v, want E_REFUSED", err)
	}
	if msg := err.Error() + " " + e.Hint; !strings.Contains(msg, "store acme/web") || !strings.Contains(msg, "nothing was written") || !strings.Contains(msg, "--push") {
		t.Fatalf("refusal = %q", msg)
	}
	if err := w.requirePushAsked(true, "a claim"); err != nil {
		t.Fatalf("with --push: %v", err)
	}
	t.Setenv(envPushClaims, envPushClaimsOn)
	if err := w.requirePushAsked(false, "a claim"); err != nil {
		t.Fatalf("with LORE_PUSH_CLAIMS=1: %v", err)
	}
}

// TestSignalsCommitSignedByPerson is devco:D-581: a signals commit an agent
// wrote was authored "<person> via <tool> <agent.auto/…@lore.invalid>". The
// author is the person now and the agent is a Lore-Agent trailer.
func TestSignalsCommitSignedByPerson(t *testing.T) {
	person := identity.Resolved{StableKey: identity.KindHuman + ":ada@example.com", DisplayName: "Ada Lane", Kind: identity.KindHuman}
	agent := identity.Resolved{StableKey: "agent:auto/claude_code/abc123", DisplayName: "Ada Lane via claude_code", Kind: identity.KindAgent,
		Owner: &person, Agent: "agent:auto/claude_code/abc123", Label: "claude_code"}
	w := &coordWorld{who: agent}
	sig := w.signalsSignature(t.Context())
	if sig.Name != "Ada Lane" || sig.Email != "ada@example.com" {
		t.Fatalf("signals author = %s <%s>, want the person", sig.Name, sig.Email)
	}
	if agentSignature(sig.Name, sig.Email) {
		t.Fatalf("signals author is an agent signature: %s <%s>", sig.Name, sig.Email)
	}
	tr := w.signalsTrailers()
	if len(tr) != 1 || !strings.Contains(tr[0], "claude_code") {
		t.Fatalf("trailers = %q, want one Lore-Agent trailer", tr)
	}
	if got := (&coordWorld{who: person}).signalsTrailers(); got != nil {
		t.Fatalf("a person's signals commit has trailers %q", got)
	}
}
