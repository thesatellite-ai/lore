package tasklist_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestTasklistDeleteWhereStatus: `tasklist delete --where` filters the
// status by the name its flags use ("status"; the stored status_str still
// filters, unlisted), and the refusal for an unknown key lists "status",
// not "status_str". keys × {match, no match} on a dry run.
func TestTasklistDeleteWhereStatus(t *testing.T) {
	h := opstest.New(t)
	active := addID(t, h, `{"title":"Sprint 1","body":"the list"}`)
	done := addID(t, h, `{"title":"Sprint 0","body":"old"}`)
	h.Must("tasklist.edit", `{"id":"`+done+`","status":"done"}`)
	for _, key := range []string{"status", "status_str"} {
		cases := []struct {
			value     string
			has, lack string
		}{
			{"active", active, done},
			{"done", done, active},
		}
		for _, c := range cases {
			out := opstest.Text(h.Must("tasklist.delete", `{"where":["`+key+`=`+c.value+`"],"dry_run":true}`))
			if !strings.Contains(out, c.has) || strings.Contains(out, c.lack) {
				t.Errorf("--where %s=%s: want %s and not %s in %q", key, c.value, c.has, c.lack, out)
			}
		}
	}
	msg := h.Fails("tasklist.delete", `{"where":["nope=1"],"dry_run":true}`, errs.InvalidInput)
	if !strings.Contains(msg, " status,") || strings.Contains(msg, "status_str") {
		t.Errorf("unknown key refusal should list status and not status_str: %q", msg)
	}
}
