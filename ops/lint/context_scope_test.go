package lint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestScopeConflicts: a folder must rule that shares its topic with a wider
// must rule is flagged once; --overrides, an unrelated rule, a sibling
// folder and a should rule are not (CONTEXT_BUDGET 6.2.2, 6.12).
func TestScopeConflicts(t *testing.T) {
	booking, other := id(t, ids.PrefixScope, 1).String(), id(t, ids.PrefixScope, 2).String()
	paths := map[string]string{booking: "src/booking", other: "src/other"}
	rule := func(n byte, sev, body, scp, over string) sourced {
		f := map[string]value.Value{"severity": tx(t, sev), "body": tx(t, body)}
		if scp != "" {
			f[schema.FieldScope] = tx(t, scp)
		}
		if over != "" {
			f[schema.FieldOverrides] = tx(t, over)
		}
		return sourced{src: store.SourceRepo, r: row.Row{ID: id(t, ids.PrefixRule, n), Fields: f}}
	}
	wide := rule(1, "must", "Money amounts are stored as integer cents everywhere.", "", "")
	wideID := wide.r.ID.String()
	rules := []sourced{
		wide,
		rule(2, "must", "Money amounts are stored as decimal minor units here.", booking, ""),
		rule(3, "must", "Money amounts are stored as decimal minor units there.", booking, wideID),
		rule(4, "must", "Booking times are UTC.", booking, ""),
		rule(5, "must", "Money amounts are stored as decimal strings.", other, ""),
		rule(6, "should", "Money amounts are stored as decimal minor units.", booking, ""),
	}
	got := scopeConflicts(rules, paths)
	ids2 := map[string]int{}
	for _, f := range got {
		ids2[f.ID]++
	}
	if ids2[id(t, ids.PrefixRule, 2).String()] != 1 || ids2[id(t, ids.PrefixRule, 5).String()] != 1 || len(got) != 2 {
		t.Fatalf("conflicts = %+v", got)
	}
}

// topEnv is an Env whose only upgrade is RepoTop (contextScopeFindings
// calls nothing else); err makes RepoTop fail.
type topEnv struct {
	Env
	top string
	err error
}

func (e topEnv) RepoTop(context.Context) (string, error) { return e.top, e.err }

// TestScopeMissing (CONTEXT_BUDGET 6.2.3, 6.12, later:472): a scp_ row
// whose folder is gone, or is now a file, is flagged once with the
// `lore scope move` fix; an existing folder, a nested existing folder and a
// row with no path are not; without the RepoTop upgrade, or when RepoTop
// fails, the check is skipped (no false "missing" for every scope).
func TestScopeMissing(t *testing.T) {
	top := t.TempDir()
	for _, d := range []string{"src/booking", "src/a/b"} {
		if err := os.MkdirAll(filepath.Join(top, filepath.FromSlash(d)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(top, "README"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	n := byte(0)
	scp := func(path string) sourced {
		n++
		f := map[string]value.Value{}
		if path != "" {
			f[schema.FieldScopePath] = tx(t, path)
		}
		return sourced{src: store.SourceRepo, r: row.Row{ID: id(t, ids.PrefixScope, n), Fields: f}}
	}
	all := map[row.Table][]sourced{schema.TableScopes: {
		scp("src/booking"), scp("src/a/b"), scp(""), scp("src/gone"), scp("README"),
	}}
	missing := func(env Env) map[string]Finding {
		out := map[string]Finding{}
		for _, f := range (ops{env: env}).contextScopeFindings(context.Background(), all) {
			if f.Code == ScopeMissing {
				out[f.ID] = f
			}
		}
		return out
	}
	got := missing(topEnv{top: top})
	gone, file := id(t, ids.PrefixScope, 4).String(), id(t, ids.PrefixScope, 5).String()
	if len(got) != 2 || got[gone].ID == "" || got[file].ID == "" {
		t.Fatalf("scope_missing = %+v, want %s and %s", got, gone, file)
	}
	if f := got[gone]; f.Fix != "lore scope move --old src/gone --new <new-path>" || f.Store != string(store.SourceRepo) {
		t.Fatalf("finding = %+v", f)
	}
	if got := missing(topEnv{err: errors.New("no repo")}); len(got) != 0 {
		t.Fatalf("RepoTop error: %+v, want skipped", got)
	}
	if got := missing(nil); len(got) != 0 {
		t.Fatalf("no RepoTop upgrade: %+v, want skipped", got)
	}
}
