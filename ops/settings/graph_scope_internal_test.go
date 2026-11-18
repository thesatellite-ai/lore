package settings

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// TestGraphWalkStopsOutsideTheFolder: devco D-618/D-621, a folder-scoped
// graph names a ref into a resource it left out (org-wide or another
// folder's) with its store, marks it outside and never walks past it;
// every node says which store it is in.
func TestGraphWalkStopsOutsideTheFolder(t *testing.T) {
	res := func(src store.Source, name string) resNode {
		v, err := value.TextOf(name)
		if err != nil {
			t.Fatal(err)
		}
		return resNode{kind: "code_repo", l: located{Src: src, Row: row.Row{Fields: map[string]value.Value{rName: v}}}}
	}
	idx := resIndex{
		nodes:   map[string]resNode{"web": res("acme/sync", "sync_web"), "go": res("acme/org", "acme_app")},
		outside: map[string]resNode{"ci": res("acme/org", "shared_ci")},
		out: map[string][]resEdge{
			"web": {{src: "web", dst: "go", field: "uses"}},
			"go":  {{src: "go", dst: "ci", field: "uses"}},
			"ci":  {{src: "ci", dst: "never", field: "uses"}},
		},
	}
	nodes, tree := idx.walk("web", walkOpts{depth: 5})
	if !strings.Contains(tree, "code_repo shared_ci (acme/org, outside this folder)") || strings.Contains(tree, "never") {
		t.Fatalf("tree must name the outside resource and stop there:\n%s", tree)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes %+v, want acme_app and shared_ci only", nodes)
	}
	if nodes[0].Store != "acme/org" || nodes[0].Outside || nodes[1].Store != "acme/org" || !nodes[1].Outside || nodes[1].Name != "shared_ci" {
		t.Fatalf("nodes %+v: each names its store, the left-out one is marked outside", nodes)
	}
}
