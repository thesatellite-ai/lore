package wire

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// unstoredTaskTitle is ops/settings unstoredTitle (unexported there): the
// title of the store-then-rotate task a redacted write gets.
const unstoredTaskTitle = "Store the secret redacted from %s, then rotate it"

// TestRedactHookBound (OV51, OV51-5, D21, SECRETS 7): the App binds the
// engine's redact hook, so a write that secrets.on_detect: redact stores
// with ${secret:unstored} gets its store-then-rotate task in the same
// command, one per redacted row (the task names the row); a clean write
// and a refused write add no task. The engine side of the hook is pinned in
// engine; this pins the wiring (App.state's SetRedactHook).
func TestRedactHookBound(t *testing.T) {
	sid := "0f8e2c1a-77b4-4c55-9d0e-" + strings.Repeat("b", 12)
	secretBody := "resume with claude --resume " + sid + " later"
	cases := []struct {
		name   string
		mode   policy.OnDetect
		bodies []string
		tasks  int // store-then-rotate tasks after the writes
	}{
		{"redact, one write", policy.OnDetectRedact, []string{secretBody}, 1},
		{"redact, two rows", policy.OnDetectRedact, []string{secretBody, secretBody}, 2},
		{"redact, clean write", policy.OnDetectRedact, []string{"nothing secret here"}, 0},
		{"refuse", policy.OnDetectRefuse, []string{secretBody}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &sessionSink{ids: []string{sid}}
			a := newSessionApp(t, gitRepo(t), sink)
			withProject(t, a)
			addPolicy(t, a, policy.KeyOnDetect, `"`+string(c.mode)+`"`)
			var written []string
			for _, b := range c.bodies {
				out, err := run(t, a, opsmemory.OpAdd, memBody(t, b), true)
				if c.mode == policy.OnDetectRefuse {
					if !errs.Has(err, errs.SecretDetected) {
						t.Fatalf("refuse: err = %v, want %s", err, errs.SecretDetected)
					}
					continue // nothing stored, no task
				}
				if err != nil {
					t.Fatal(err)
				}
				written = append(written, idOf(t, out))
			}
			eng, err := a.Engine(background())
			if err != nil {
				t.Fatal(err)
			}
			rows, err := eng.List(background(), store.SourceRepo, schema.TableTasks)
			if err != nil {
				t.Fatal(err)
			}
			titles := map[string]bool{}
			for _, r := range rows {
				titles[r.Fields["title"].String()] = true
			}
			got := 0
			for _, id := range written {
				if titles[fmt.Sprintf(unstoredTaskTitle, id)] {
					got++
				}
			}
			if got != c.tasks || len(rows) != c.tasks {
				t.Fatalf("store-then-rotate tasks for written rows = %d (task rows %d), want %d", got, len(rows), c.tasks)
			}
		})
	}
}
