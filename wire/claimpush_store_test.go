package wire

import (
	"errors"
	"strings"
	"testing"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/model/errs"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
)

// TestStoreClaimNeedsPush is devco:D-580: a claim in a linked repo went to
// the store's signals ref on the store's remote without --push, although
// --push says a claim there is refused without it. It is refused now,
// writing nothing, and --push (or the standing go-ahead) lets it through.
func TestStoreClaimNeedsPush(t *testing.T) {
	t.Setenv(envPushClaims, "")
	t.Setenv(envLiveAutopushSwitch, "")
	w := &coordWorld{spot: sigSpot{store: true, label: "acme/web", ref: "refs/lore/signals/main", remote: &gitremote.Git{}}}
	err := w.requirePushAsked(false, pushAsk{what: "a claim", rerun: []string{"task", "claim", "--id", "tsk_x"}})
	var e *errs.Error
	if err == nil || !errors.As(err, &e) || e.Code != errs.Refused {
		t.Fatalf("a store claim without --push = %v, want E_REFUSED", err)
	}
	if msg := err.Error() + " " + e.Hint; !strings.Contains(msg, "store acme/web") || !strings.Contains(msg, "nothing was written") || !strings.Contains(msg, "--push") {
		t.Fatalf("refusal = %q", msg)
	}
	if err := w.requirePushAsked(true, pushAsk{what: "a claim", rerun: []string{"task", "claim", "--id", "tsk_x"}}); err != nil {
		t.Fatalf("with --push: %v", err)
	}
	t.Setenv(envPushClaims, envPushClaimsOn)
	if err := w.requirePushAsked(false, pushAsk{what: "a claim", rerun: []string{"task", "claim", "--id", "tsk_x"}}); err != nil {
		t.Fatalf("with LORE_PUSH_CLAIMS=1: %v", err)
	}
}

// TestPushRefusalPrintsTheRealCommand is devco:D-669: a hold refused for
// want of --push printed "run the command again with --push" and "`lore
// task start --id <id>` works on a task without a claim": a placeholder
// where the id was known, and a task step in a hold refusal. Each refusal
// now prints the person's own command with --push, every flag kept and the
// value quoted for a shell, and only a claim of one task names task start
// with that task's id.
func TestPushRefusalPrintsTheRealCommand(t *testing.T) {
	t.Setenv(envPushClaims, "")
	t.Setenv(envLiveAutopushSwitch, "")
	w := &coordWorld{remoteName: "origin", spot: sigSpot{remote: &gitremote.Git{}}}
	cases := []struct {
		name, want, without string
		ask                 pushAsk
	}{
		{"hold", "`lore hold claim --name 'ci suite' --ttl 30m0s --push`", "",
			pushAsk{what: "a hold", rerun: holdRerun(opscoord.HoldClaimIn{Name: "ci suite", TTL: 30 * time.Minute})}},
		{"claim", "`lore task claim --id tsk_01abc --owns web/ --strict --push`", "`lore task start --id tsk_01abc`",
			claimAsk("a claim", claimRerun(opscoord.ClaimIn{ID: "tsk_01abc", Owns: []string{"web/"}, Strict: true}), "tsk_01abc")},
		{"next", "`lore task next --claim --mission msn_01m --push`", "",
			pushAsk{what: "the claim task next takes", rerun: nextRerun(opscoord.NextIn{Claim: true, Mission: "msn_01m"})}},
		{"reassign", "`lore task reassign --id tsk_01abc --assign @bo --push`", "",
			pushAsk{what: "a reassign of a claimed task", rerun: reassignRerun(opscoord.ReassignIn{ID: "tsk_01abc", Assign: "@bo"})}},
	}
	for _, c := range cases {
		var e *errs.Error
		if err := w.requirePushAsked(false, c.ask); !errors.As(err, &e) || e.Code != errs.Refused {
			t.Fatalf("%s: %v, want E_REFUSED", c.name, err)
		}
		if !strings.Contains(e.Hint, c.want) {
			t.Errorf("%s: hint %q does not print %s", c.name, e.Hint, c.want)
		}
		if strings.ContainsAny(e.Hint, "<>") {
			t.Errorf("%s: hint %q prints a placeholder", c.name, e.Hint)
		}
		if c.without == "" && strings.Contains(e.Hint, "task start") {
			t.Errorf("%s: hint %q names task start, which is no next step here", c.name, e.Hint)
		}
		if c.without != "" && !strings.Contains(e.Hint, c.without) {
			t.Errorf("%s: hint %q lacks %s", c.name, e.Hint, c.without)
		}
		if e.Next == nil || e.Next.Command != "lore" || e.Next.Args[len(e.Next.Args)-1] != "--push" {
			t.Errorf("%s: next = %+v, want the command with --push", c.name, e.Next)
		}
	}
}

// TestSignalsCommitSignedByPerson is devco:D-581 and devco:D-983: a
// signals commit an agent wrote was authored "<person> via <tool>
// <agent.auto/…@lore.invalid>". The author is the person now, and when the
// person's key holds no address it is lore itself, never the agent; no
// commit names the agent (no Lore-Agent trailer either).
func TestSignalsCommitSignedByPerson(t *testing.T) {
	person := identity.Resolved{StableKey: identity.KindHuman + ":ada@example.com", DisplayName: "Ada Lane", Kind: identity.KindHuman}
	agent := identity.Resolved{StableKey: "agent:auto/codex/abc123", DisplayName: "Ada Lane via codex", Kind: identity.KindAgent,
		Owner: &person, Agent: "agent:auto/codex/abc123", Label: "codex"}
	w := &coordWorld{who: agent}
	sig := w.signalsSignature(t.Context())
	if sig.Name != "Ada Lane" || sig.Email != "ada@example.com" {
		t.Fatalf("signals author = %s <%s>, want the person", sig.Name, sig.Email)
	}
	// the owner's case: a person key with no address (or no owner at all)
	// fell back to the agent's display name and a made-up agent address
	bare := identity.Resolved{StableKey: identity.KindHuman + ":ada", DisplayName: "Ada Lane", Kind: identity.KindHuman}
	for name, who := range map[string]identity.Resolved{
		"no address": {StableKey: agent.StableKey, DisplayName: agent.DisplayName, Kind: identity.KindAgent, Owner: &bare, Agent: agent.Agent, Label: "codex"},
		"no owner":   {StableKey: agent.StableKey, DisplayName: agent.DisplayName, Kind: identity.KindAgent, Agent: agent.Agent, Label: "codex"},
		"ci":         {StableKey: "agent:ci", DisplayName: "ci", Kind: identity.KindAgent},
	} {
		sig := (&coordWorld{who: who}).signalsSignature(t.Context())
		if agentSignature(sig.Name, sig.Email) || sig.Name != gitbackend.DefaultAuthor.Name || sig.Email != gitbackend.DefaultAuthor.Email {
			t.Errorf("%s: signals author = %s <%s>, want %s <%s> (never the agent)", name, sig.Name, sig.Email, gitbackend.DefaultAuthor.Name, gitbackend.DefaultAuthor.Email)
		}
	}
	// FLAG4: git's identity of the repo wins over the lore identity
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.name", "Ada At Work")
	gitT(t, dir, "config", "user.email", "ada@work.example")
	repo, err := gitref.Open(t.Context(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	w = &coordWorld{who: agent, spot: sigSpot{repo: repo}}
	if sig := w.signalsSignature(t.Context()); sig.Name != "Ada At Work" || sig.Email != "ada@work.example" {
		t.Fatalf("signals author = %s <%s>, want git's identity of the repo", sig.Name, sig.Email)
	}
	// a repo whose git identity is an old agent one is not used
	gitT(t, dir, "config", "user.name", "Ada Lane via codex")
	gitT(t, dir, "config", "user.email", "agent.auto/codex/abc123@lore.invalid")
	if sig := w.signalsSignature(t.Context()); sig.Name != "Ada Lane" || sig.Email != "ada@example.com" {
		t.Fatalf("signals author = %s <%s>, want the person over an agent git identity", sig.Name, sig.Email)
	}
}
