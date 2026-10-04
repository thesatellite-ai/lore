package lsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/merge3"
)

func writeTmp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func taskDoc(t *testing.T, kv map[string]any) string {
	t.Helper()
	doc := map[string]any{keyVersion: num(1), keyTable: "tasks", "id": "tsk_01a10237c9e87e4c843d8045973a65a1",
		"title": "t", "body": "b", "status": "todo", "updated_at": "2026-01-01T00:00:00.000000000Z"}
	for k, v := range kv {
		doc[k] = v
	}
	b, err := canonjson.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func runDriver(t *testing.T, base, ours, theirs, repoPath string) (MergeOutcome, map[string]any, string) {
	t.Helper()
	dir := t.TempDir()
	o := writeTmp(t, dir, "ours", ours)
	out, err := MergeFiles(context.Background(), writeTmp(t, dir, "base", base), o, writeTmp(t, dir, "theirs", theirs), repoPath)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(o)
	doc, _ := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	return out, doc, string(b)
}

func TestMergeDriver_DifferentFieldsClean(t *testing.T) {
	t.Parallel()
	base := taskDoc(t, nil)
	ours := taskDoc(t, map[string]any{"title": "ours title", "updated_at": "2026-02-01T00:00:00.000000000Z"})
	theirs := taskDoc(t, map[string]any{"status": "done", "updated_at": "2026-03-01T00:00:00.000000000Z"})
	out, doc, _ := runDriver(t, base, ours, theirs, ".lore/data/tasks/x.json")
	if out.Conflicted || doc["title"] != "ours title" || doc["status"] != "done" {
		t.Fatalf("out=%+v doc=%v", out, doc)
	}
	if doc["updated_at"] != "2026-03-01T00:00:00.000000000Z" {
		t.Fatalf("updated_at must take the max: %v", doc["updated_at"])
	}
}

func TestMergeDriver_StatusNewestWins(t *testing.T) {
	t.Parallel()
	base := taskDoc(t, nil)
	ours := taskDoc(t, map[string]any{"status": "in_progress", "updated_at": "2026-05-01T00:00:00.000000000Z"})
	theirs := taskDoc(t, map[string]any{"status": "done", "updated_at": "2026-04-01T00:00:00.000000000Z"})
	out, doc, _ := runDriver(t, base, ours, theirs, "x")
	if out.Conflicted || doc["status"] != "in_progress" || len(out.AutoResolved) == 0 {
		t.Fatalf("newer ours must win: %+v %v", out, doc)
	}
	if got := strings.Join(out.NewerWins(), ","); got != "status" {
		t.Fatalf("the losing status edit must be reported: %q", got)
	}
}

func TestMergeDriver_TextClashMarkedButValidJSON(t *testing.T) {
	t.Parallel()
	base := taskDoc(t, nil)
	out, doc, raw := runDriver(t, base, taskDoc(t, map[string]any{"body": "mine"}), taskDoc(t, map[string]any{"body": "yours"}), "x")
	if !out.Conflicted {
		t.Fatal("text clash must report a conflict")
	}
	if doc == nil {
		t.Fatalf("result must stay valid JSON: %s", raw)
	}
	if !merge3.ContainsConflictMarkers(doc["body"].(string)) {
		t.Fatal("markers missing")
	}
}

func TestMergeDriver_AddAddWithEmptyBase(t *testing.T) {
	t.Parallel()
	same := taskDoc(t, nil)
	out, doc, _ := runDriver(t, "", same, same, "x")
	if out.Conflicted || doc["title"] != "t" {
		t.Fatalf("identical add/add must merge cleanly: %+v", out)
	}
}

func TestMergeDriver_NonStringClashIsVisible(t *testing.T) {
	t.Parallel()
	base := `{"_v":1,"cfg":{"a":1}}`
	out, doc, _ := runDriver(t, base, `{"_v":1,"cfg":{"a":2}}`, `{"_v":1,"cfg":{"a":3}}`, "x")
	s, _ := doc["cfg"].(string)
	if !out.Conflicted || !merge3.ContainsConflictMarkers(s) {
		t.Fatalf("object clash hidden: %+v %v", out, doc)
	}
}

func TestMergeDriver_PurgedUnion(t *testing.T) {
	t.Parallel()
	a := `{"_v":1,"ids":["mem_a"]}`
	b := `{"_v":1,"ids":["mem_b"]}`
	out, doc, _ := runDriver(t, `{"_v":1,"ids":[]}`, a, b, ".lore/data/_purged.json")
	list, _ := doc["ids"].([]any)
	if out.Conflicted || len(list) != 2 {
		t.Fatalf("union failed: %+v %v", out, doc)
	}
}

func TestMergeDriver_FallbackToGitText(t *testing.T) {
	t.Parallel()
	out, _, raw := runDriver(t, "a\nb\nc\n", "A\nb\nc\n", "a\nb\nC\n", "x")
	if !out.Fallback || out.Conflicted || raw != "A\nb\nC\n" {
		t.Fatalf("text fallback: %+v %q", out, raw)
	}
	out, _, raw = runDriver(t, "a\n", "x\n", "y\n", "x")
	if !out.Fallback || !out.Conflicted || !strings.Contains(raw, "<<<<<<<") {
		t.Fatalf("text fallback conflict: %+v %q", out, raw)
	}
}

func TestMergeDriver_NewerFormatFallsBack(t *testing.T) {
	t.Parallel()
	out, _, _ := runDriver(t, `{"_v":1}`, `{"_v":99,"x":1}`, `{"_v":1,"x":2}`, "x")
	if !out.Fallback {
		t.Fatal("a newer-format file must not be rewritten by an older driver (E13)")
	}
}

func TestNewerWinsLeavesOutBookkeeping(t *testing.T) {
	t.Parallel()
	out := MergeOutcome{AutoResolved: []string{updatedAtColumn, "status", keyVersion, "priority", keyTable}}
	if got := strings.Join(out.NewerWins(), ","); got != "status,priority" {
		t.Fatalf("NewerWins = %q", got)
	}
	if got := (MergeOutcome{AutoResolved: []string{updatedAtColumn}}).NewerWins(); len(got) != 0 {
		t.Fatalf("updated_at alone is not a decision: %v", got)
	}
}
