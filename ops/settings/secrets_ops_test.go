package settings_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/wire"
)

// secretDummy is a dummy value the env manager reveals (never a real one).
const secretDummy = "fake-sunflower-tractor-42"

const (
	holderA = "mch_0cd630f0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	holderB = "mch_7a21b9e4bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func secretsOf(t *testing.T, h *opstest.H, name, in string) settings.SecretsOut {
	t.Helper()
	var out settings.SecretsOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(name, in))), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSecretsOffboarding(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	t.Setenv("LORE_TEST_DB", secretDummy)
	h := multiStore(t)
	ref := "env://LORE_TEST_DB?holder=" + holderA + "&label=mira.studio_mac"
	mem := h.ID(h.MustYes("memory.add", `{"body":"db login: ${secret:`+ref+`} and token ${secret:unstored}","to":"kb"}`))

	all := secretsOf(t, h, settings.OpSecretsList, ``)
	if all.Count != 2 {
		t.Fatalf("list = %+v", all.Data)
	}
	if got := secretsOf(t, h, settings.OpSecretsList, `{"holder":"mira"}`); got.Count != 1 || got.Data[0].Locator != "LORE_TEST_DB" {
		t.Fatalf("--holder mira = %+v", got.Data)
	}
	if got := secretsOf(t, h, settings.OpSecretsList, `{"unstored":true}`); got.Count != 1 || got.Data[0].At != 1 || got.Data[0].Row != mem {
		t.Fatalf("--unstored = %+v", got.Data)
	}

	// reveal asks every time: no --yes → E_NEEDS_CONFIRM, never the value.
	msg := h.Fails(settings.OpSecretsShow, `{"id":"env://LORE_TEST_DB","reveal":true}`, errs.NeedsConfirm)
	if strings.Contains(msg, secretDummy) || !strings.Contains(msg, "env (LORE_TEST_DB)") {
		t.Fatalf("question %q", msg)
	}
	res, err := h.RunYes(settings.OpSecretsShow, `{"id":`+opstest.Quote(mem)+`,"reveal":true}`)
	if err != nil {
		t.Fatal(err)
	}
	var shown settings.SecretsOut
	if err = json.Unmarshal([]byte(h.JSON(res)), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.Revealed != 1 || shown.Values[0].Value != secretDummy || shown.Forced {
		t.Fatalf("reveal = %+v", shown)
	}
	forced := secretsOf(t, h, settings.OpSecretsShow, `{"id":"env://LORE_TEST_DB","reveal":true,"force":true}`)
	if !forced.Forced || !strings.HasPrefix(forced.Warning, "W_REVEAL_FORCED") {
		t.Fatalf("forced = %+v", forced)
	}
	h.Fails(settings.OpSecretsShow, `{"id":"env://LORE_NOT_SET","reveal":true,"force":true}`, errs.VaultEntryUnavailable)
	h.Fails(settings.OpSecretsShow, `{"id":"nope://x"}`, errs.RefScheme)

	// the audit has the events, never the value.
	b, err := os.ReadFile(filepath.Join(h.Repo, filepath.FromSlash(wire.AuditFile)))
	if err != nil {
		t.Fatal(err)
	}
	log := string(b)
	if strings.Contains(log, secretDummy) || !strings.Contains(log, `"action":"secret.revealed"`) ||
		!strings.Contains(log, `"secret_forced_by":"flag"`) || !strings.Contains(log, `"action":"secret.reveal_failed"`) {
		t.Fatalf("audit log:\n%s", log)
	}

	// check: present; the unstored row gets its store-then-rotate task once.
	chk := secretsOf(t, h, settings.OpSecretsCheck, ``)
	if chk.Data[0].State != "present" || len(chk.Tasks) != 1 {
		t.Fatalf("check = %+v\n%s", chk.Data, chk.Human)
	}
	if again := secretsOf(t, h, settings.OpSecretsCheck, ``); len(again.Tasks) != 0 {
		t.Fatalf("second check added another task:\n%s", again.Human)
	}
	t.Setenv("LORE_TEST_DB", "")
	if miss := secretsOf(t, h, settings.OpSecretsList, `{"missing":true}`); miss.Count != 1 || miss.Data[0].State != "missing" {
		t.Fatalf("--missing = %+v", miss.Data)
	}

	// move asks, then rewrites the holder in one batch.
	h.Fails(settings.OpSecretsMove, `{"from":"mira","to_holder":"`+holderB+`"}`, errs.NeedsConfirm)
	h.MustYes(settings.OpSecretsMove, `{"from":"mira","to_holder":"`+holderB+`"}`)
	if got := secretsOf(t, h, settings.OpSecretsList, `{"holder":"`+holderB+`"}`); got.Count != 1 {
		t.Fatalf("after move = %+v", all.Data)
	}
	h.Must(settings.OpSecretsRenameEntry, `{"manager":"env","from":"LORE_TEST_DB","to_entry":"LORE_TEST_DB2"}`)
	if got := secretsOf(t, h, settings.OpSecretsList, `{"manager":"env"}`); got.Count != 1 || got.Data[0].Locator != "LORE_TEST_DB2" || got.Data[0].Holder != holderB {
		t.Fatalf("after rename = %+v", got.Data)
	}
	h.Fails(settings.OpSecretsLink, `{"id":`+opstest.Quote(mem)+`,"at":2,"ref":"env://X"}`, errs.NotFound)
	h.Must(settings.OpSecretsLink, `{"id":`+opstest.Quote(mem)+`,"at":1,"ref":"env://LORE_TOKEN"}`)
	if got := secretsOf(t, h, settings.OpSecretsList, `{"unstored":true}`); got.Count != 0 {
		t.Fatalf("after link = %+v", got.Data)
	}
	if got := secretsOf(t, h, settings.OpSecretsList, ``); got.Count != 2 {
		t.Fatalf("after link list = %+v", got.Data)
	}
}

// TestExposeOneCaller: Secret.Expose is referenced (called or taken as a
// method value) from exactly one function in lore's whole source, the --reveal printer in ops/internal/reveal
// (SECRETS_AND_REFERENCES 10.2).
func TestExposeOneCaller(t *testing.T) {
	t.Parallel()
	n, where := 0, []string{}
	root := os.DirFS(filepath.Join("..", ".."))
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() && p != "." && (d.Name() == "vendor" || d.Name() == "tmp" || strings.HasPrefix(d.Name(), ".")) {
			return fs.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, rerr := fs.ReadFile(root, p)
		if rerr != nil {
			return rerr
		}
		// every selector named Expose (a call, a method value, split over
		// lines), not a text match a reformat could slip past
		f, perr := parser.ParseFile(token.NewFileSet(), p, b, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(nd ast.Node) bool {
			if sel, ok := nd.(*ast.SelectorExpr); ok && sel.Sel.Name == "Expose" {
				n++
				where = append(where, p)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || where[0] != "ops/internal/reveal/reveal.go" {
		t.Fatalf(".Expose() called %d times (%v), want 1 in ops/internal/reveal (expose)", n, where)
	}
}

func TestSecretsShowTrashed(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := multiStore(t)
	mem := h.ID(h.MustYes("memory.add", `{"body":"k ${secret:env://LORE_X}","to":"kb"}`))
	h.MustYes("memory.delete", `{"id":`+opstest.Quote(mem)+`}`)
	h.Fails(settings.OpSecretsShow, `{"id":`+opstest.Quote(mem)+`}`, errs.InTrash)
}

// TestRowShowReveal: lore show --reveal (2.7) asks every time, fills the
// manager reference in clear, keeps ${secret:unstored} masked, and marks a
// --force reveal; --force alone is refused.
func TestRowShowReveal(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	t.Setenv("LORE_TEST_DB", secretDummy)
	h := multiStore(t)
	mem := h.ID(h.MustYes("memory.add", `{"body":"db: ${secret:env://LORE_TEST_DB} t ${secret:unstored}","to":"kb"}`))
	id := opstest.Quote(mem)
	if msg := h.Fails("row.show", `{"id":`+id+`,"reveal":true}`, errs.NeedsConfirm); strings.Contains(msg, secretDummy) {
		t.Fatalf("question leaks: %q", msg)
	}
	h.Fails("row.show", `{"id":`+id+`,"force":true}`, errs.InvalidInput)
	if j := h.JSON(h.Must("row.show", `{"id":`+id+`,"expand":true}`)); strings.Contains(j, secretDummy) {
		t.Fatalf("--expand leaks: %s", j)
	}
	res, err := h.RunYes("row.show", `{"id":`+id+`,"reveal":true}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Expanded map[string]string `json:"expanded"`
		Revealed int               `json:"revealed"`
		Forced   bool              `json:"forced"`
	}
	if err = json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Revealed != 1 || out.Forced || out.Expanded["body"] != "db: "+secretDummy+" t ••••••" {
		t.Fatalf("reveal = %+v", out)
	}
	if j := h.JSON(h.Must("row.show", `{"id":`+id+`,"reveal":true,"force":true}`)); !strings.Contains(j, `"forced":true`) || !strings.Contains(j, "W_REVEAL_FORCED") {
		t.Fatalf("forced = %s", j)
	}
}
