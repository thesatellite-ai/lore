package task

// devco:D-464: the status writer marks the open claim chain's before with
// status_set, so a release keeps the status (coord.Release reads it).

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// claimRow is a task row whose claim_log is logJSON ("" for none).
func claimRow(t *testing.T, logJSON string) row.Row {
	t.Helper()
	r := row.Row{Fields: map[string]value.Value{}}
	if logJSON != "" {
		v, err := value.JSONOf([]byte(logJSON))
		if err != nil {
			t.Fatal(err)
		}
		r.Fields[fieldClaimLog] = v
	}
	return r
}

// markedLog runs markClaimStatusSet and returns the patched log as decoded
// JSON, or nil when the patch carries no claim_log.
func markedLog(t *testing.T, logJSON string) []map[string]any {
	t.Helper()
	patch := map[string]value.Value{}
	if err := markClaimStatusSet(claimRow(t, logJSON), patch); err != nil {
		t.Fatal(err)
	}
	v, ok := patch[fieldClaimLog]
	if !ok {
		return nil
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var log []map[string]any
	if err := json.Unmarshal(raw, &log); err != nil {
		t.Fatal(err)
	}
	return log
}

func TestMarkClaimStatusSet(t *testing.T) {
	const (
		claimTodo = `{"at":"2026-11-02T10:00:00Z","event":"claim","actor_id":"act_a","epoch":1,"before":{"status":"todo"}}`
		claimBare = `{"at":"2026-11-02T10:00:00Z","event":"claim","actor_id":"act_a","epoch":1}`
		reclaim   = `{"at":"2026-11-02T11:00:00Z","event":"reclaim","actor_id":"act_b","prev_actor_id":"act_a","epoch":2}`
		release   = `{"at":"2026-11-02T12:00:00Z","event":"release","actor_id":"act_a","epoch":1}`
	)
	before := func(log []map[string]any, i int) map[string]any {
		b, _ := log[i]["before"].(map[string]any)
		return b
	}

	if markedLog(t, "") != nil || markedLog(t, "["+claimTodo+","+release+"]") != nil {
		t.Fatal("a row never claimed, or released, must get no claim_log write")
	}

	log := markedLog(t, "["+claimTodo+","+reclaim+"]")
	if b := before(log, 0); b["status_set"] != true || b["status"] != "todo" {
		t.Fatalf("the chain's claim (the one carrying before) must be marked, its status kept: %v", log)
	}
	if _, ok := log[1]["before"]; ok || log[1]["prev_actor_id"] != "act_a" || log[1]["epoch"] != float64(2) {
		t.Fatalf("the reclaim must be kept as it was: %v", log[1])
	}

	if log := markedLog(t, "["+claimBare+"]"); before(log, 0)["status_set"] != true {
		t.Fatalf("a claim an older binary wrote (no before) gets one: %v", log)
	}

	marked := `{"at":"2026-11-02T10:00:00Z","event":"claim","actor_id":"act_a","epoch":1,"before":{"status":"todo","status_set":true}}`
	if markedLog(t, "["+marked+"]") != nil {
		t.Fatal("an already marked claim needs no claim_log write")
	}
	if markedLog(t, `"not a log"`) != nil {
		t.Fatal("an unreadable claim_log is left alone")
	}
}
