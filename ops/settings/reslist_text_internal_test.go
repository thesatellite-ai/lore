package settings

import (
	"strings"
	"testing"
)

// TestResourceListAlignsAndCounts guards devco D-572: resource list printed
// raw tab-separated rows and no count, unlike every other list.
func TestResourceListAlignsAndCounts(t *testing.T) {
	t.Parallel()
	out := ResListOut{columns: []string{"url"}, Data: []Resource{
		{Kind: "code_repo", Name: "a_very_long_repository_name", Values: map[string]string{"url": "github.com/acme/a"}},
		{Kind: "code_repo", Name: "b", Values: map[string]string{"url": "github.com/acme/b"}},
	}}.Text()
	if strings.Contains(out, "\t") {
		t.Fatalf("raw tabs in the list:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 || lines[3] != "2 resources" {
		t.Fatalf("want a head, 2 rows and a count line, got:\n%s", out)
	}
	col := strings.Index(lines[0], "URL")
	for _, l := range lines[1:3] {
		if strings.Index(l, "github.com/acme/") != col {
			t.Fatalf("the url column is not aligned:\n%s", out)
		}
	}
}
