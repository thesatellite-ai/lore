package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestActorProjectionKeepsAMigratedFullCopy (devco K-4): in a repo that is
// not client-class, a machine whose identity resolves no org runs the
// actor_projection step after an org member's migration. The format-2
// copy that migration wrote (handle, stable_key, origin_store,
// origin_version) is left as it is; a v1 copy (lore 0.1.17's stable_key)
// is still trimmed in full and a format-2 copy loses only `_o`.
// Break-verified: without projectionExtra the step trims all three and
// drops the four keys of the migrated copy.
func TestActorProjectionKeepsAMigratedFullCopy(t *testing.T) {
	t.Parallel()
	dir := gitRepo(t)
	a := openWith(t, dir, Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, a)
	const (
		full = "act_01a1cb5743e371b7bab946c6cf48ab02"
		v1   = "act_01a1cb5743e371b7bab946c6cf48ab03"
		oCpy = "act_01a1cb5743e371b7bab946c6cf48ab04"
	)
	files := map[string]string{
		full: `{"_hlc":"2026-11-03T10:00:00.000Z/0000/","_table":"actors","_v":2,"created_at":"2026-11-03T10:00:00.000000000Z","display_name":"Rosa Lind","handle":"rosa","id":"` + full + `","kind":"human","origin_store":"setup:widgets","origin_version":"3","stable_key":"human:rosa@example.test","updated_at":"2026-11-03T10:00:00.000000000Z"}`,
		v1:   `{"_table":"actors","_v":1,"created_at":"2026-11-03T10:00:00.000000000Z","display_name":"omar","id":"` + v1 + `","kind":"human","stable_key":"human:omar@example.test","updated_at":"2026-11-03T10:00:00.000000000Z"}`,
		oCpy: `{"_hlc":"2026-11-03T10:00:00.000Z/0000/","_table":"actors","_v":2,"_o":{"cmd":"memory add","store":"me"},"created_at":"2026-11-03T10:00:00.000000000Z","display_name":"Pia","handle":"pia","id":"` + oCpy + `","kind":"human","updated_at":"2026-11-03T10:00:00.000000000Z"}`,
	}
	adir := filepath.Join(dir, ".lore", "data", "actors")
	if err := os.MkdirAll(adir, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, doc := range files {
		if err := os.WriteFile(filepath.Join(adir, id+".json"), []byte(doc+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := a.migrateActorProjection(background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Upgraded, ",") != "actors/"+v1+",actors/"+oCpy && strings.Join(got.Upgraded, ",") != "actors/"+oCpy+",actors/"+v1 {
		t.Fatalf("trimmed %v, want only the v1 copy and the _o copy (steps %v)", got.Upgraded, got.Steps)
	}
	read := func(id string) string {
		b, err := os.ReadFile(filepath.Join(adir, id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, k := range []string{`"handle"`, `"stable_key"`, `"origin_store"`, `"origin_version"`} {
		if !strings.Contains(read(full), k) {
			t.Errorf("the migrated full copy lost %s:\n%s", k, read(full))
		}
	}
	if strings.Contains(read(v1), "stable_key") {
		t.Errorf("the v1 copy kept stable_key:\n%s", read(v1))
	}
	if b := read(oCpy); strings.Contains(b, `"_o"`) || !strings.Contains(b, `"handle"`) {
		t.Errorf("the _o copy: want only _o dropped:\n%s", b)
	}
	again, err := a.migrateActorProjection(background(), false)
	if err != nil || len(again.Steps) != 0 {
		t.Fatalf("a second run: %v %v, want nothing", again.Steps, err)
	}
}
