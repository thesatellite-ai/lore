package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestSecretRefInJSONListField (class sweep of the type_history bug): a
// secret reference inside a JSON field that holds a list of objects (a
// task's time entries; the note is free text) is listed by secrets list
// and rewritten by secrets rename-entry, both on the row as written and
// after it is read back from its row file (where the list decodes as a
// list of JSON items, not one JSON value).
func TestSecretRefInJSONListField(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := multiStore(t) // a client repo refuses secret references: the task lives in kb
	task := h.ID(h.MustYes("task.add", `{"title":"rotate","to":"kb"}`))
	h.MustYes("task.time.log", `{"id":"`+task+`","duration":"30m","note":"used ${secret:env://LORE_LIST_OLD}"}`)
	for _, round := range []string{"as written", "read back"} {
		if round == "read back" {
			h = h.Reopen()
		}
		got := secretsOf(t, h, settings.OpSecretsList, ``)
		found := false
		for _, r := range got.Data {
			found = found || (r.Row == task && r.Locator == "LORE_LIST_OLD")
		}
		if !found {
			t.Fatalf("%s: secrets list misses the reference in a time entry note: %+v", round, got.Data)
		}
	}
	h.MustYes(settings.OpSecretsRenameEntry, `{"manager":"env","from":"LORE_LIST_OLD","to_entry":"LORE_LIST_NEW"}`)
	h = h.Reopen()
	var locs []string
	for _, r := range secretsOf(t, h, settings.OpSecretsList, ``).Data {
		if r.Row == task {
			locs = append(locs, r.Locator)
		}
	}
	if strings.Join(locs, ",") != "LORE_LIST_NEW" {
		t.Fatalf("rename-entry did not rewrite the time entry note: references %v", locs)
	}
}
