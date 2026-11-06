package wire

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
)

// TestOriginProjectOwnVsClient (G-ORGMIG-7, PROVENANCE 5.1/8.1): a row
// created in our own repo records the checkout's project (its
// _meta.json project_id) in `_o.project`, and `lore show --origin`
// prints it; a row created in a client repo has no `_o.project`; with no
// _meta.json project_id the key is left out.
func TestOriginProjectOwnVsClient(t *testing.T) {
	cases := []struct {
		name   string
		client bool
		seed   bool
		want   bool
	}{
		{"own repo", false, true, true},
		{"client repo", true, true, false},
		{"own repo without a _meta.json project", false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := gitRepo(t)
			client := c.client
			a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return client, nil }})
			withProject(t, a)
			ctx := background()
			pid, err := a.Project(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if c.seed {
				if err = a.SeedRepoMeta(ctx, pid); err != nil {
					t.Fatal(err)
				}
			}
			out, err := run(t, a, opsmemory.OpAdd, `{"body":"origin project"}`, true)
			if err != nil {
				t.Fatal(err)
			}
			var w struct {
				ID string `json:"id"`
			}
			if err = json.Unmarshal([]byte(out), &w); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(repo, ".lore", "data", schema.TableMemories, w.ID+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				O json.RawMessage `json:"_o"`
			}
			if err = json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			o, err := value.JSONOf(doc.O)
			if err != nil {
				t.Fatal(err)
			}
			got := origin.Text(o, origin.KeyProject)
			if (got == pid.String()) != c.want || (!c.want && got != "") {
				t.Fatalf("_o.project = %q, want present=%v (%s)", got, c.want, doc.O)
			}
			show, err := run(t, a, "row.show", `{"id":"`+w.ID+`","origin":true}`, false)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(show, `"project":{"id":"`+pid.String()) != c.want {
				t.Fatalf("show --origin project, want %v: %s", c.want, show)
			}
		})
	}
}
