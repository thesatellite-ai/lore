package stores

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestStoreDoctorPrintsEveryStoreThenFails (devco D-483): store doctor
// prints a line per store (✓ ok, ⚠ warn, ✗ fail) with the lines under it
// and a summary, then fails only when a store failed; a branch the store
// does not hold is a warning (exit 0), never the one line of the run.
func TestStoreDoctorPrintsEveryStoreThenFails(t *testing.T) {
	t.Parallel()
	ok := StoreCheck{Store: "notes", Source: SourceStoresFile, Kind: StoreKindFolder, URL: "file:///placeholder/notes", Reachable: true}
	side := CloneView{URL: "git@git.example.test:acme/ops.git", Entries: []string{"acme"}, Branches: []string{"main"}, Cloned: true,
		Unpushed: []string{"refs/heads/backup-test"},
		Warnings: []string{"unpushed work: refs/heads/backup-test (branch backup-test is not on the store: push it with `git -C /c/ops push origin backup-test`, or delete it with `git -C /c/ops branch -D backup-test`)"}}
	f := &fakeEnv{checked: []StoreCheck{ok}, clones: ClonesOut{Stores: []CloneView{side}}}

	js, txt, err := invoke(t, f, OpDoctor, `{}`)
	if err != nil {
		t.Fatalf("a warning alone must not fail the doctor: %v", err)
	}
	for _, want := range []string{
		"✓ notes (stores.yaml, folder) file:///placeholder/notes\n",
		"⚠ git@git.example.test:acme/ops.git  (entries acme; branches main)\n",
		"    warn: unpushed work: refs/heads/backup-test (branch backup-test is not on the store: push it with `git -C /c/ops push origin backup-test`, or delete it with `git -C /c/ops branch -D backup-test`)\n",
		"summary: 2 store(s) checked: 1 ok, 1 warn, 0 fail\n",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("doctor text lacks %q:\n%s", want, txt)
		}
	}
	var doc struct {
		Summary DoctorSummary `json:"summary"`
		Error   string        `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(js), &doc); jerr != nil || doc.Summary != (DoctorSummary{Checked: 2, OK: 1, Warn: 1}) || doc.Error != "" {
		t.Fatalf("json summary %+v error %q (%v)", doc.Summary, doc.Error, jerr)
	}

	// a failing store: every line still prints, then the run fails
	bad := ok
	bad.Store, bad.Problems = "team", []string{"marker missing"}
	f.checked = []StoreCheck{ok, bad}
	js, txt, err = invoke(t, f, OpDoctor, `{}`)
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "1 store(s) need attention: team: marker missing") {
		t.Fatalf("a failing store must fail the run: %v", err)
	}
	for _, want := range []string{
		"✓ notes (stores.yaml, folder)",
		"✗ team (stores.yaml, folder)",
		"    fail: marker missing\n",
		"⚠ git@git.example.test:acme/ops.git",
		"    warn: unpushed work: refs/heads/backup-test",
		"summary: 3 store(s) checked: 1 ok, 1 warn, 1 fail\n",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("failing doctor text lacks %q:\n%s", want, txt)
		}
	}
	if !strings.Contains(js, `"error": "1 store(s) need attention: team: marker missing"`) && !strings.Contains(js, `"error":"1 store(s) need attention: team: marker missing"`) {
		t.Errorf("--json must carry the failure: %s", js)
	}
}
