//go:build stories && !windows

package stories

// E_helpers_test.go — what story E needs on top of the shared harness: row
// counts on disk (nothing lost or duplicated across the migration), the
// migrate summary's count, and a 0.1.17 tag link that points at a deleted
// note (the orphan that used to abort the migration, D-51).

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// eCount is migrate run's headline count ("Would upgrade N rows" on a dry
// run, "Upgraded N rows" on the real one): the two must agree (D-54).
var eCount = regexp.MustCompile(`(?m)^(?:Would upgrade|Upgraded) (\d+) rows?$`)

// eV2Row matches a row file at format 2 or later.
var eV2Row = regexp.MustCompile(`"_v":\s*([2-9]|[1-9][0-9])\b`)

// eRowIDs lists every row id under dir's .lore/data (table/id), the
// migration's bookkeeping folders and _meta.json left out, so two counts
// taken before and after a migration compare the rows a person wrote.
func eRowIDs(t testing.TB, dir string) map[string]bool {
	t.Helper()
	data := filepath.Join(dir, ".lore", "data")
	out := map[string]bool{}
	tables, err := os.ReadDir(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range tables {
		if !tb.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(data, tb.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".json") {
				out[tb.Name()+"/"+strings.TrimSuffix(f.Name(), ".json")] = true
			}
		}
	}
	return out
}

// eMissing lists the ids of want that got does not hold, for tables that
// hold notes people wrote (entity_tags and tags fold into tag_ids, actors and
// repos are lore's own bookkeeping, so those may come and go).
func eMissing(want, got map[string]bool) []string {
	var out []string
	for id := range want {
		tb := strings.SplitN(id, "/", 2)[0]
		switch tb {
		case "entity_tags", "tags", "actors", "repos":
			continue
		}
		if !got[id] {
			out = append(out, id)
		}
	}
	return out
}

// eV2Files lists the row files under dir's .lore/data at format 2 or later.
func eV2Files(t testing.TB, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(dir, ".lore", "data"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if eV2Row.Match(b) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// eOrphanTag writes a 0.1.17 tag and a tag link to a memory that was
// deleted (its row file is gone), the shape that used to abort the
// migration with E_NOT_FOUND and leave the repo mixed (D-51). It returns
// the link's file name.
func eOrphanTag(t testing.TB, dir, project string) string {
	t.Helper()
	const stamp = `"created_at":"2026-01-01T09:00:00Z","updated_at":"2026-01-01T09:00:00Z"`
	const tag = "tag_0e45506e64357c94a87e70ef1c6b0e01"
	const link = "etg_0e45506e64357c94a87e70ef1c6b0e09"
	const gone = "mem_0e45506e64357c94a87e70ef1c6b0e99"
	data := filepath.Join(dir, ".lore", "data")
	writeFile(t, filepath.Join(data, "tags", tag+".json"), `{"_table":"tags","_v":1,`+stamp+`,"id":"`+tag+`","name":"legacy","project_id":"`+project+`"}`+"\n")
	writeFile(t, filepath.Join(data, "entity_tags", link+".json"), `{"_table":"entity_tags","_v":1,`+stamp+`,"entity_id":"`+gone+`","entity_table":"memories","id":"`+link+`","tag_id":"`+tag+`"}`+"\n")
	return link
}

// eRenderSkipped reports whether lore fix's render line says the render was
// skipped (the line that once said "skipped" while the files were written,
// D-56); other steps' "skipped" lines do not count.
func eRenderSkipped(out string) bool {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[1] == "render" && strings.Contains(l, "skipped") {
			return true
		}
	}
	return false
}
