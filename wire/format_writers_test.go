package wire

// format_writers_test.go — later 416: `lore migrate enable` on cmd/lore
// lists the teammates whose newest stamped save (`_w`) is below the
// feature's Since (writersBelow, FORMAT_EVOLUTION rule 2 step 4, FQ2):
// they would turn read-only. A dev-build stamp, a stamp at or above Since
// and an unstamped row list nobody; a code-repo save (no machine) names
// itself; --yes declares the feature.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

func TestMigrateEnableListsWritersBelow(t *testing.T) {
	const mate = "act_0e45506e64357c94a87e70ef1c6b0ae7"
	const machine = "mch_93aa33bc2e8a70b1b3d8110909fcc62f"
	cases := []struct {
		name  string
		stamp map[string]string // the teammate row's _w; nil = no stamp
		want  string            // "" = no confirm needed
	}{
		{"older release on a machine", map[string]string{"v": "0.1.17", "m": machine}, mate + " on mch_93aa33bc (lore 0.1.17)"},
		{"older release, code repo save", map[string]string{"v": "0.1.17"}, mate + " on a code repo save (lore 0.1.17)"},
		{"unparsable version counts as below", map[string]string{"v": "garbage", "m": machine}, mate + " on mch_93aa33bc (lore garbage)"},
		{"same release", map[string]string{"v": "0.1.18", "m": machine}, ""},
		{"newer release", map[string]string{"v": "0.2.0", "m": machine}, ""},
		{"dev build", map[string]string{"v": "dev", "m": machine}, ""},
		{"no stamp", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := gitRepo(t)
			a := newTestApp(t, repo)
			withProject(t, a)
			out, err := run(t, a, opsmemory.OpAdd, `{"body":"mine"}`, false)
			if err != nil {
				t.Fatal(err)
			}
			mine := idOf(t, out)
			// what `lore init` leaves: the checkout's _meta.json
			if err = os.WriteFile(filepath.Join(repo, ".lore", "data", "_meta.json"), []byte(`{"_v":2}`), 0o600); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(repo, ".lore", "data", schema.TableMemories)
			var doc map[string]any
			if err = json.Unmarshal(mustRead(t, filepath.Join(dir, mine+".json")), &doc); err != nil {
				t.Fatal(err)
			}
			other := mine[:len(mine)-4] + "beef"
			doc["id"], doc["updated_by"], doc["created_by"] = other, mate, mate
			delete(doc, "_w")
			if c.stamp != nil {
				doc["_w"] = c.stamp
			}
			b, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, other+".json"), b, 0o600); err != nil {
				t.Fatal(err)
			}
			fresh := newTestApp(t, repo) // a new run scans the teammate's row
			in := `{"feature":"` + formatgate.Time + `"}`
			_, err = run(t, fresh, opsmigrate.OpEnable, in, false)
			if c.want == "" {
				if err != nil {
					t.Fatalf("no writer below: %v", err)
				}
				return
			}
			var e *errs.Error
			if !errors.As(err, &e) || e.Code != errs.NeedsConfirm || !strings.Contains(e.Message, c.want) || strings.Contains(e.Message, testActor) {
				t.Fatalf("want E_NEEDS_CONFIRM naming %q only: %#v", c.want, err)
			}
			if declared(repo, formatgate.Time) {
				t.Fatal("a refused enable declared the feature")
			}
			if _, err = run(t, fresh, opsmigrate.OpEnable, in, true); err != nil || !declared(repo, formatgate.Time) {
				t.Fatalf("--yes: %v", err)
			}
		})
	}
}
