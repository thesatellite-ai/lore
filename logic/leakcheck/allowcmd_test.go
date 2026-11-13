package leakcheck

import (
	"strings"
	"testing"
)

// TestAllowRowCommandOneSpelling (user reports A-8, B-6): every hint names
// `lore secrets allow` in one shape the command accepts: --row-id, --field,
// --rule, --match-hash, --reason, in that order.
func TestAllowRowCommandOneSpelling(t *testing.T) {
	t.Parallel()
	got := AllowRowCommand("tsk_01", "body", "generic-api-key", "sha256:6ea6")
	want := `lore secrets allow --row-id tsk_01 --field body --rule generic-api-key --match-hash sha256:6ea6 --reason "<why>"`
	if got != want {
		t.Fatalf("AllowRowCommand = %s\nwant %s", got, want)
	}
	if tmpl := AllowRowTemplate(); !strings.HasPrefix(tmpl, "lore secrets allow --row-id <row-id> --field <field> --rule <rule> --match-hash ") {
		t.Fatalf("AllowRowTemplate = %s", tmpl)
	}
}

// TestCIActorFindingNamesFileAndFix (user report C-7): the --ci finding of
// an actor copy with extra keys names its file and the one command that
// trims it; ActorCopyExtraKeys names the same keys the migrate step drops.
// Break-verified: with Path/Fix unset on the actor finding it fails.
func TestCIActorFindingNamesFileAndFix(t *testing.T) {
	t.Parallel()
	const p = ".lore/data/actors/act_01a1cb5743e371b7bab946c6cf48ab02.json"
	doc := []byte(`{"_table":"actors","_v":2,"id":"act_01a1cb5743e371b7bab946c6cf48ab02","display_name":"rosa","kind":"human","stable_key":"human:rosa@example.test","_o":{"store":"repo","project":"prj_01a1cb57169c7c73a3a5dd7f0e1b8c6d"}}`)
	fs := CheckCI(map[string][]byte{p: doc}, "prj_01a1cb57169c7c73a3a5dd7f0e1b8c6d")
	if len(fs) != 1 || fs[0].Path != p || fs[0].Fix != ActorCopyFix || !strings.Contains(fs[0].Detail, "stable_key") {
		t.Fatalf("CheckCI = %+v, want one actor finding naming %s and %q", fs, p, ActorCopyFix)
	}
	if got := strings.Join(ActorCopyExtraKeys(doc), ","); got != "_o,stable_key" {
		t.Fatalf("ActorCopyExtraKeys = %s, want _o,stable_key", got)
	}
	clean := []byte(`{"_table":"actors","_v":2,"id":"act_01a1cb5743e371b7bab946c6cf48ab02","display_name":"rosa","kind":"human"}`)
	if got := ActorCopyExtraKeys(clean); got != nil {
		t.Fatalf("a clean copy has extra keys %v", got)
	}
}
