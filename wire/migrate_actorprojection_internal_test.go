package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// TestTrimActorCopyPassesTheClientCheck (user reports B-5, C-7): a lore
// 0.1.17 actor copy (stable_key) and a copy carrying `_o` are trimmed by the
// actor_projection step's trimActorCopy to exactly the keys the client-repo
// guard and `lore check leaks --ci` accept, keeping id, display name, kind
// and the audit stamps. Break-verified: a trimActorCopy that drops nothing
// leaves stable_key and _o.
func TestTrimActorCopyPassesTheClientCheck(t *testing.T) {
	t.Parallel()
	doc := `{"_hlc":"2026-11-03T10:00:00.000Z/0000/","_table":"actors","_v":2,"created_at":"2026-11-03T10:00:00.000000000Z","display_name":"rosa","id":"act_01a1cb5743e371b7bab946c6cf48ab02","kind":"human",` +
		`"_o":{"cmd":"migrate run","project":"prj_01a1cb57169c7c73a3a5dd7f0e1b8c6d","store":"repo"},"stable_key":"human:rosa@example.test","updated_at":"2026-11-03T10:00:00.000000000Z"}`
	r, err := row.Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	extra := leakcheck.ActorCopyExtraKeys([]byte(doc))
	if strings.Join(extra, ",") != "_o,stable_key" {
		t.Fatalf("extra keys = %v", extra)
	}
	up, err := store.FromRow(trimActorCopy(r, extra))
	if err != nil {
		t.Fatal(err)
	}
	if left := leakcheck.ActorCopyExtraKeys(up.Doc); left != nil {
		t.Fatalf("the trimmed copy still holds %v:\n%s", left, up.Doc)
	}
	for _, want := range []string{`"display_name": "rosa"`, `"kind": "human"`, `"id": "act_01a1cb5743e371b7bab946c6cf48ab02"`, `"created_at"`} {
		if !strings.Contains(string(up.Doc), want) {
			t.Errorf("the trimmed copy lost %s:\n%s", want, up.Doc)
		}
	}
	if _, kept := r.Fields["stable_key"]; !kept {
		t.Fatal("trimActorCopy changed the caller's row")
	}
}
