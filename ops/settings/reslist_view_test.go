package settings

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestResListNarrowView: devco D-584, the no-kind list prints KIND NAME
// and the project/url columns some row sets, CHECKED only when a row was
// checked, and cuts a long value instead of padding every row to it.
func TestResListNarrowView(t *testing.T) {
	long := strings.Repeat("purpose text ", 40)
	out := ResListOut{every: true, Data: []Resource{
		{Kind: "code_repo", Name: "acme_app", Values: map[string]string{"project": "sync", "url": "github.com/acme/sync-go", "purpose": long}},
		{Kind: "host", Name: "db1", Values: map[string]string{}},
	}}
	txt := out.Text()
	head := strings.Fields(strings.SplitN(txt, "\n", 2)[0])
	if strings.Join(head, " ") != "KIND NAME PROJECT URL" {
		t.Fatalf("head %q, want KIND NAME PROJECT URL:\n%s", head, txt)
	}
	out.Data[1].LastChecked = "2026-11-01T10:00:00Z"
	if !strings.Contains(out.Text(), "CHECKED") {
		t.Fatalf("a checked row must show CHECKED:\n%s", out.Text())
	}
	kind := ResListOut{columns: []string{"purpose"}, Data: out.Data[:1]}
	for _, line := range strings.Split(kind.Text(), "\n") {
		if len([]rune(line)) > 120 {
			t.Fatalf("a %d-char line; the cell must be cut:\n%s", len([]rune(line)), kind.Text())
		}
	}
	if !strings.Contains(kind.Text(), "…") {
		t.Fatalf("a cut cell ends with …:\n%s", kind.Text())
	}
}

// TestFolderScopeKeeps: devco D-583, a folder's list keeps the resources
// stored in it and the company folder's whose project names the folder.
func TestFolderScopeKeeps(t *testing.T) {
	sc := FolderScope{Source: "acme/sync", Folder: "sync"}
	org := store.Source("acme/org")
	for _, c := range []struct {
		src  store.Source
		proj string
		want bool
	}{{"acme/sync", "", true}, {org, "sync", true}, {org, "acme/sync", true}, {org, "tools", false}, {org, "", false}} {
		if got := sc.keeps(c.src, Resource{Values: map[string]string{"project": c.proj}}); got != c.want {
			t.Fatalf("keeps(%s, project=%q) = %v, want %v", c.src, c.proj, got, c.want)
		}
	}
	n := sc.note(2, []store.Source{org})
	for _, want := range []string{"folder acme/sync", "those of acme/org whose project is sync", "2 more resources", "lore resource list --store acme"} {
		if !strings.Contains(n, want) {
			t.Fatalf("note %q lacks %q", n, want)
		}
	}
}
