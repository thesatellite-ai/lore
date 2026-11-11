package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// Rules of the repo TestStoreAdoptRelinksOldSupersedes adopts: a rule
// replaced the lore 0.1.16 way (the link on the new row), and two rules
// that both claim to replace a third.
const (
	relinkOld    = "rul_01a1cb5d97ea7a29a7e500000000a001"
	relinkNew    = "rul_01a1cb5d97ea7a29a7e500000000a002"
	relinkShared = "rul_01a1cb5d97ea7a29a7e500000000a003"
	relinkTwinA  = "rul_01a1cb5d97ea7a29a7e500000000a004"
	relinkTwinB  = "rul_01a1cb5d97ea7a29a7e500000000a005"
)

// relinkRule is a v1 rule file created at hour h, linking to next.
func relinkRule(id, body, next string, h int) string {
	link := ""
	if next != "" {
		link = `"superseded_by_id": "` + next + `", `
	}
	at := "2026-10-0" + string(rune('1'+h)) + "T10:00:00.000000000Z"
	return `{"_table": "rules", "_v": 1, "activation": "always", "body": "` + body + `", "created_at": "` + at + `", "id": "` + id + `", "project_id": "` + adoptProject +
		`", "severity": "must", "source_kind": "manual", ` + link + `"trust_score": 0.5, "updated_at": "` + at + `"}`
}

// TestStoreAdoptRelinksOldSupersedes is the owner's adopted v0.1.17 data
// (2026-11-12): a --supersedes link lore 0.1.16 wrote on the new row
// lands on the old row in the folder (said in the output), so doctor
// does not report it; two rules replacing one are named and left as they
// are; a rerun writes nothing.
func TestStoreAdoptRelinksOldSupersedes(t *testing.T) {
	t.Parallel()
	repo, storeDir, _, _ := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"northwind","folder":"client_r"}`, true); err != nil {
		t.Fatal(err)
	}
	src := gitRepo(t)
	files := map[string]string{
		"_meta.json":                      `{"_v": 1, "project_id": "` + adoptProject + `"}`,
		"rules/" + relinkOld + ".json":    relinkRule(relinkOld, "use tabs", "", 0),
		"rules/" + relinkNew + ".json":    relinkRule(relinkNew, "run gofmt", relinkOld, 1),
		"rules/" + relinkShared + ".json": relinkRule(relinkShared, "review changes", "", 2),
		"rules/" + relinkTwinA + ".json":  relinkRule(relinkTwinA, "review twice", relinkShared, 3),
		"rules/" + relinkTwinB + ".json":  relinkRule(relinkTwinB, "review in pairs", relinkShared, 4),
	}
	for p, doc := range files {
		full := filepath.Join(src, ".lore", "data", filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(doc+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	in := `{"target":"northwind/client_r","from":"` + filepath.ToSlash(src) + `"}`
	out, err := adoptRun(t, a, in, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Relinked != 1 || len(out.Conflicts) != 1 || !strings.Contains(out.Conflicts[0], relinkTwinA+" and "+relinkTwinB+" each claim to replace "+relinkShared) {
		t.Fatalf("adopt = relinked %d, conflicts %q", out.Relinked, out.Conflicts)
	}
	if !strings.Contains(out.Text(), "1 --supersedes link written by lore 0.1.16 or older moved onto the row each replaces") {
		t.Errorf("the output does not say what it relinked:\n%s", out.Text())
	}
	read := func(id string) string {
		b, rerr := os.ReadFile(filepath.Join(storeDir, "client_r", ".lore", "data", "rules", id+".json"))
		if rerr != nil {
			t.Fatal(rerr)
		}
		return string(b)
	}
	if doc := read(relinkOld); !strings.Contains(doc, `"superseded_by_id": "`+relinkNew+`"`) {
		t.Errorf("the old rule does not name its successor:\n%s", doc)
	}
	if doc := read(relinkNew); strings.Contains(doc, relinkOld) {
		t.Errorf("the new rule still links to the old one:\n%s", doc)
	}
	for _, id := range []string{relinkTwinA, relinkTwinB} {
		if doc := read(id); !strings.Contains(doc, relinkShared) {
			t.Errorf("%s lost its link (two into one is never fixed):\n%s", id, doc)
		}
	}
	again, err := adoptRun(t, a, in, true)
	if err != nil || again.Written != 0 {
		t.Fatalf("rerun = %+v, %v; want nothing written", again, err)
	}
}

// Actors of TestStoreAdoptMapsActorByKey: one person's two ids.
const (
	mapKept   = "act_541757c158dc7fb58c1c338b0a60f301"
	mapSecond = "act_541757c158dc7fb58c1c338b0a60f302"
	mapKey    = "human:una@example.test"
	mapMemory = "mem_01a1cb5d827d726cabe9000000003001"
)

// writeRepoFiles writes .lore/data files (path → doc) into a new git repo.
func writeRepoFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	src := gitRepo(t)
	for p, doc := range files {
		full := filepath.Join(src, ".lore", "data", filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(doc+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

// mapActorDoc is a v1 actor file with the shared key.
func mapActorDoc(id string) string {
	return `{"_table": "actors", "_v": 1, "display_name": "Una Tester", "id": "` + id + `", "kind": "human", "stable_key": "` + mapKey + `"}`
}

// TestStoreAdoptMapsActorByKey is the owner's second actor id
// (2026-11-12): a repo whose actor has the stable key of an actor the
// folder already holds under another id is adopted onto that actor: the
// second id's row is not written, the rows naming it name the kept actor,
// the output names the mapping, and a rerun writes nothing.
func TestStoreAdoptMapsActorByKey(t *testing.T) {
	t.Parallel()
	repo, storeDir, _, _ := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"northwind","folder":"client_m"}`, true); err != nil {
		t.Fatal(err)
	}
	meta := `{"_v": 1, "project_id": "` + adoptProject + `"}`
	first := writeRepoFiles(t, map[string]string{"_meta.json": meta, "actors/" + mapKept + ".json": mapActorDoc(mapKept)})
	if _, err := adoptRun(t, a, `{"target":"northwind/client_m","from":"`+filepath.ToSlash(first)+`"}`, true); err != nil {
		t.Fatal(err)
	}
	second := writeRepoFiles(t, map[string]string{"_meta.json": meta, "actors/" + mapSecond + ".json": mapActorDoc(mapSecond),
		"memories/" + mapMemory + ".json": `{"_table": "memories", "_v": 1, "body": "the cache warms at boot", "created_at": "2026-11-02T10:00:00.000000000Z", "created_by_actor_id": "` + mapSecond +
			`", "id": "` + mapMemory + `", "kind": "retrieved", "project_id": "` + adoptProject + `", "source_kind": "manual", "trust_score": 0.5, "updated_at": "2026-11-02T10:00:00.000000000Z"}`})
	in := `{"target":"northwind/client_m","from":"` + filepath.ToSlash(second) + `"}`
	out, err := adoptRun(t, a, in, true)
	if err != nil {
		t.Fatal(err)
	}
	want := mapSecond + " (" + mapKey + ") → " + mapKept
	if len(out.Actors) != 1 || out.Actors[0] != want || out.Written != 1 {
		t.Fatalf("adopt = actors %q, written %d; want [%s] and the memory only", out.Actors, out.Written, want)
	}
	if !strings.Contains(out.Text(), "actor "+want) {
		t.Errorf("the output does not name the mapping:\n%s", out.Text())
	}
	data := filepath.Join(storeDir, "client_m", ".lore", "data")
	if _, err := os.Stat(filepath.Join(data, "actors", mapSecond+".json")); err == nil {
		t.Error("the second id's actor row was written into the folder")
	}
	b, err := os.ReadFile(filepath.Join(data, "memories", mapMemory+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), mapSecond) || !strings.Contains(string(b), mapKept) {
		t.Errorf("the memory does not name the kept actor:\n%s", b)
	}
	again, err := adoptRun(t, a, in, true)
	if err != nil || again.Written != 0 {
		t.Fatalf("rerun = %+v, %v; want nothing written", again, err)
	}
}
