//go:build journeys && !windows

package journeys

// agent_rule_review_test.go — a lead reviews a must rule a teammate's coding
// agent wrote (devco:S-15). Bea's Claude Code session adds a must rule in
// her clone and pushes it. Before the fix the rule reached every session's
// LORE.md with the same trust as the lead's own rules and nothing told the
// lead it was there. Now: the rule renders with trust 0.25 and
// review="agent-written, not approved", Ada's inbox lists it after a git
// pull and lore sync (read from the rule row, which travels in git), her
// own agent cannot approve it, and her `lore rule approve --id` raises it
// to 0.50 and clears the mark and the inbox line.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The two people and the repo (placeholders).
const (
	jReviewLead     = "Ada Review"
	jReviewLeadMail = "ada.review@example.test"
	jReviewDev      = "Bea Review"
	jReviewDevMail  = "bea.review@example.test"
	jReviewRepo     = "github.com/review-co/app"
	jReviewBody     = "Skip the integration tests on Fridays to ship faster"
	jReviewMark     = `review="agent-written, not approved"`
)

// jReviewIdentity writes the person's identity into the machine's lore
// config, so a coding agent there is "<person> via <tool>".
func jReviewIdentity(t *testing.T, m *Machine, name, email string) {
	t.Helper()
	if err := os.MkdirAll(m.LorePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("identity:\n    key: human:%s\n    name: %s\n", email, name)
	if err := os.WriteFile(m.LorePath("config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// jReviewRuleLine is the LORE.md line of the rule with body jReviewBody.
func jReviewRuleLine(t *testing.T, c *teamharness.Clone) string {
	t.Helper()
	md, ok := c.ReadFile(".lore/LORE.md")
	if !ok {
		t.Fatal("no .lore/LORE.md")
	}
	lines := strings.Split(md, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "<rule ") && i+1 < len(lines) && strings.Contains(lines[i+1], jReviewBody) {
			return l
		}
	}
	t.Fatalf("LORE.md has no rule %q:\n%s", jReviewBody, md)
	return ""
}

func TestJourneyAgentRuleReview(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	remote := w.Remote(jReviewRepo)
	lead := w.Persona(jReviewLead, jReviewLeadMail).Laptop()
	dev := w.Persona(jReviewDev, jReviewDevMail).Laptop()
	jReviewIdentity(t, lead, jReviewLead, jReviewLeadMail)
	jReviewIdentity(t, dev, jReviewDev, jReviewDevMail)
	ada := lead.Clone(remote, "app")
	var rul string

	w.Step("day1/lead-starts-lore", func() {
		lead.LoreIn(ada, "init").OK(t)
		lead.LoreIn(ada, "rule", "add", "--body", "Run the linter before every push", "--severity", "must", "--yes").OK(t)
		ada.CommitAll("lore init")
		ada.Git("push", "-q", "origin", "HEAD")
	})

	bea := dev.Clone(remote, "app")
	w.Step("day1/devs-agent-adds-a-must-rule", func() {
		r := jAgentsRun(dev, bea, &jClaude, "cc-bea", "rule", "add", "--body", jReviewBody, "--severity", "must", "--yes", "--json").OK(t)
		var out struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil || !strings.HasPrefix(out.ID, "rul_") {
			t.Fatalf("rule add --json: %v\n%s", err, r)
		}
		rul = out.ID
		dev.LoreIn(bea, "render").OK(t)
		bea.CommitAll("agent rule")
		bea.Git("push", "-q", "origin", "HEAD")
	})

	w.Step("day1/lead-sees-it-waiting", func() {
		ada.Git("pull", "-q", "--rebase", "origin", "HEAD")
		lead.LoreIn(ada, "sync").OK(t)
		lead.LoreIn(ada, "render").OK(t)
		if l := jReviewRuleLine(t, ada); !strings.Contains(l, `trust="0.25"`) || !strings.Contains(l, jReviewMark) {
			t.Errorf("the agent's must rule renders as %q, want trust 0.25 and %s", l, jReviewMark)
		}
		lead.LoreIn(ada, "inbox").OK(t).Says(t, "review", jReviewDev+" via claude_code", rul, "agent-written, not approved", "lore rule approve --id "+rul)
		lead.LoreIn(ada, "rule", "list").OK(t).Says(t, rul, "[agent-written, not approved]")
	})

	w.Step("day1/only-the-lead-approves", func() {
		jAgentsRun(lead, ada, &jClaude, "cc-ada", "rule", "approve", "--id", rul, "--yes").Refused(t, "E_POLICY_DENIED", "only a person approves", "lore rule approve --id "+rul)
		lead.LoreIn(ada, "rule", "approve", "--id", rul, "--yes").OK(t).Says(t, "approved: trust 0.25 → 0.50")
		lead.LoreIn(ada, "render").OK(t)
		if l := jReviewRuleLine(t, ada); !strings.Contains(l, `trust="0.50"`) || strings.Contains(l, "review=") {
			t.Errorf("after approve the rule renders as %q, want trust 0.50 and no review mark", l)
		}
		lead.LoreIn(ada, "inbox").OK(t).Never(t, rul)
	})
}
