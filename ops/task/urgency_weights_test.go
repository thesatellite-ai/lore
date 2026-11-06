package task_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/urgency"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// infer is value.Infer of a JSON literal, failing the test.
func infer(t *testing.T, raw string) value.Value {
	t.Helper()
	v, err := value.Infer([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestUrgencyWeightShape: a tasks.urgency.* value is a signed number with
// at most two decimals and |v| <= 1000 (OV15B-URG); anything else stops
// the policy with E_CONFIG_INVALID (a broken row is never skipped).
func TestUrgencyWeightShape(t *testing.T) {
	t.Parallel()
	key := policy.UrgencyKey("next_tag", "")
	cases := []struct {
		raw  string
		want int64 // hundredths
		ok   bool
	}{
		{`12`, 1200, true},
		{`-3`, -300, true},
		{`0.5`, 50, true},
		{`0`, 0, true},
		{`12.25`, 1225, true},
		{`1000`, 100000, true},
		{`-1000`, -100000, true},
		{`1.234`, 0, false}, // three decimals
		{`1001`, 0, false},  // over the ceiling
		{`-1001`, 0, false}, // under the floor
		{`1e2`, 0, false},   // exponent form is not a two-decimal number
		{`"12"`, 0, false},  // text
		{`true`, 0, false},
		{`["1"]`, 0, false},
	}
	for _, c := range cases {
		set, err := policy.Resolve([]policy.Entry{{Level: policy.LevelRepo, Key: key, Value: infer(t, c.raw)}})
		if !c.ok {
			if !errs.Has(err, errs.ConfigInvalid) {
				t.Errorf("%s: err %v, want E_CONFIG_INVALID", c.raw, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.raw, err)
			continue
		}
		got, isSet := set.Weight(key)
		if !isSet || got != c.want {
			t.Errorf("%s: Weight = %d,%v want %d", c.raw, got, isSet, c.want)
		}
	}
	// Unset: Weight reports false, so the 13.2 default stands.
	if _, isSet := policy.Defaults().Weight(key); isSet {
		t.Error("an unset weight reports set")
	}
}

// TestUrgencyWeightNearestWins: weights do not take the stricter value;
// the nearest level that sets one wins (repo over folder over org),
// whatever order the entries arrive in.
func TestUrgencyWeightNearestWins(t *testing.T) {
	t.Parallel()
	key := policy.UrgencyKey("due", "")
	org := policy.Entry{Level: policy.LevelOrg, Key: key, Value: infer(t, `1`)}
	folder := policy.Entry{Level: policy.LevelFolder, Key: key, Value: infer(t, `2`)}
	repo := policy.Entry{Level: policy.LevelRepo, Key: key, Value: infer(t, `30`)}
	cases := []struct {
		name string
		in   []policy.Entry
		want int64
	}{
		{"repo, folder, org", []policy.Entry{repo, folder, org}, 3000},
		{"org, folder, repo", []policy.Entry{org, folder, repo}, 3000},
		{"folder over org", []policy.Entry{org, folder}, 200},
		{"org alone", []policy.Entry{org}, 100},
	}
	for _, c := range cases {
		set, err := policy.Resolve(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got, _ := set.Weight(key); got != c.want {
			t.Errorf("%s: %d want %d", c.name, got, c.want)
		}
	}
}

// TestUrgencyWeightsApplyEveryKey: UrgencyWeights maps each of the 12 keys
// onto its own Weights field (a mapping slip would move one term's weight
// onto another), and leaves the defaults when nothing is set.
func TestUrgencyWeightsApplyEveryKey(t *testing.T) {
	t.Parallel()
	keys := policy.UrgencyKeys()
	if len(keys) != 12 {
		t.Fatalf("%d urgency keys, want 12 (4 priority levels + 8 terms)", len(keys))
	}
	def, err := opstask.UrgencyWeights(policy.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if d := urgency.Default(); d.NextTag != def.NextTag || d.PriorityPoints[urgency.PriorityHigh] != def.PriorityPoints[urgency.PriorityHigh] {
		t.Errorf("defaults changed with no policy: %+v", def)
	}
	// Each key gets a distinct value (1.01, 1.02 …) so a crossed mapping
	// shows as the wrong number in the wrong field.
	var entries []policy.Entry
	want := map[policy.Key]int{}
	for i, k := range keys {
		h := 101 + i
		entries = append(entries, policy.Entry{Level: policy.LevelRepo, Key: k.Key, Value: infer(t, "1."+itoa2(i+1))})
		want[k.Key] = h
	}
	set, err := policy.Resolve(entries)
	if err != nil {
		t.Fatal(err)
	}
	w, err := opstask.UrgencyWeights(set)
	if err != nil {
		t.Fatal(err)
	}
	got := map[policy.Key]int{
		policy.UrgencyKey("priority", "low"):    w.PriorityPoints[urgency.PriorityLow],
		policy.UrgencyKey("priority", "medium"): w.PriorityPoints[urgency.PriorityMedium],
		policy.UrgencyKey("priority", "high"):   w.PriorityPoints[urgency.PriorityHigh],
		policy.UrgencyKey("priority", "urgent"): w.PriorityPoints[urgency.PriorityUrgent],
		policy.UrgencyKey("due", ""):            w.DueMax,
		policy.UrgencyKey("mission_target", ""): w.MissionMax,
		policy.UrgencyKey("blocking", ""):       w.BlockingEach,
		policy.UrgencyKey("in_progress", ""):    w.InProgress,
		policy.UrgencyKey("age", ""):            w.AgeMax,
		policy.UrgencyKey("sla_risk", ""):       w.SLARisk,
		policy.UrgencyKey("next_tag", ""):       w.NextTag,
		policy.UrgencyKey("proposed", ""):       w.Proposed,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: field holds %d, want %d", k, got[k], v)
		}
	}
	// The policy sets only the term's points; the windows stay 13.2's.
	if d := urgency.Default(); w.DueWindow != d.DueWindow || w.AgeWindow != d.AgeWindow || w.DueMin != d.DueMin {
		t.Errorf("windows changed: %+v", w)
	}
}

// itoa2 is n (0..99) as two digits.
func itoa2(n int) string {
	return fmt.Sprintf("%02d", n)
}

// TestUrgencyWeightsReachListAndWhy: a repo policy row changes the order of
// task list --sort urgency and the points task show --why prints (the
// seam from policy rows through the engine to the op).
func TestUrgencyWeightsReachListAndWhy(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	high := add(h, "high", `"priority":"high"`)
	low := add(h, "low", `"priority":"low"`)

	order := func() []string {
		t.Helper()
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpList, `{"sort":["urgency:desc"]}`))), &out); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, d := range out.Data {
			ids = append(ids, d.ID)
		}
		return ids
	}
	if got := order(); len(got) != 2 || got[0] != high {
		t.Fatalf("default order %v, want high first", got)
	}
	// Low priority now weighs 20 points, high 1: low ranks first.
	h.MustYes(settings.OpPolicySet, `{"key":"tasks.urgency.priority.low","value":"20"}`)
	h.MustYes(settings.OpPolicySet, `{"key":"tasks.urgency.priority.high","value":"1"}`)
	if got := order(); len(got) != 2 || got[0] != low {
		t.Errorf("weighted order %v, want low first", got)
	}
	why := opstest.Text(h.Must(opstask.OpShow, `{"id":"`+low+`","why":true}`))
	if !strings.Contains(why, "20") {
		t.Errorf("--why does not show the policy weight:\n%s", why)
	}
	// A broken weight is refused at policy set, before it can stop reads.
	if _, err := h.RunYes(settings.OpPolicySet, `{"key":"tasks.urgency.due","value":"1.234"}`); err == nil {
		t.Error("policy set took a three-decimal weight")
	}
}
