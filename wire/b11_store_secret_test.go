package wire

// Port of saas/cmd/cli refuse_secrets_core_test.go (B11): a task note and
// a task done --lesson are scanned with the core's write-time rules, so a
// custom secret_rules row (which the v0.1.17 scanner never knew) stops the
// write, naming the rule id and never the value, and nothing is written.
// Under the default secrets.on_detect (ask) a write with yes and no
// secret choice is refused (FB-SEC-1: a bare --yes never stores a detected
// secret).

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

func TestNoteAndLessonUseTheCoreSecretRules(t *testing.T) {
	t.Parallel()
	a, _, id := standaloneTask(t)
	if _, err := run(t, a, opssettings.OpSecretRuleAdd, `{"id":"acme-token","regex":"ACMETOK-[0-9]{12}"}`, true); err != nil {
		t.Fatal(err)
	}
	token := "ACMETOK-" + strings.Repeat("7", 12)
	refused := func(what string, err error) {
		t.Helper()
		if !errs.Has(err, errs.NeedsConfirm) && !errs.Has(err, errs.SecretDetected) {
			t.Fatalf("%s = %v, want a secret refusal", what, err)
		}
		if !strings.Contains(err.Error(), "acme-token") || strings.Contains(err.Error(), token) {
			t.Fatalf("%s: the refusal must name acme-token and never the value: %v", what, err)
		}
	}
	_, err := run(t, a, opscoord.OpTaskNote, `{"id":"`+id+`","text":["deploy with `+token+`"]}`, true)
	refused("task note", err)
	if n := countRows(t, a, schema.TableComments); n != 0 {
		t.Fatalf("a refused note wrote %d comments", n)
	}
	_, err = run(t, a, opstask.OpDone, `{"id":"`+id+`","lesson":"the key is `+token+`"}`, true)
	refused("task done --lesson", err)
	if n := countRows(t, a, schema.TableMemories); n != 0 {
		t.Fatalf("a refused lesson wrote %d memories", n)
	}
	if _, err = run(t, a, opscoord.OpTaskNote, `{"id":"`+id+`","text":["plain text"]}`, true); err != nil {
		t.Fatalf("plain note: %v", err)
	}
}
