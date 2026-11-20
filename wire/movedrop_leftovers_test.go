package wire

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestMovedDropLeftoversKeepsRepoRows (devco D-731): in a new-format
// linked repo .lore/data also holds _meta.json, a policies row and the
// repos row; those are the repo's own and must not count as "other rows",
// so the actor and project rows nothing kept references go, while the
// project the repos row and _meta.json point at stays (named, with why).
func TestMovedDropLeftoversKeepsRepoRows(t *testing.T) {
	const (
		act   = "act_cb98f8b36b637507a4cfe91eca849f00"
		prj   = "prj_01a1f7820f317ea88e77193a2f5a98df"
		other = "prj_3bad73a087f37496880b48b17dc1990d"
		rep   = "rep_14e9dabe88917eaa8a89e329c48a117d"
		pol   = "pol_28ec0068b39a7d328f3afa05ea0064fa"
	)
	top := t.TempDir()
	data := filepath.Join(top, ".lore", "data")
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(data, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("_meta.json", `{"_v":2,"format":2,"project_id":"`+prj+`","repo_id":"`+rep+`"}`)
	write("policies/"+pol+".json", `{"_table":"policies","id":"`+pol+`","key":"branch.mode","scope":"repo","value":"main","created_by":"`+act+`"}`)
	write("repos/"+rep+".json", `{"_table":"repos","id":"`+rep+`","project_id":"`+prj+`","mount_name":"billing-app","created_by":"`+act+`","urls":[{"by":"`+act+`"}]}`)
	write("actors/"+act+".json", `{"_table":"actors","id":"`+act+`","created_by":"`+act+`"}`)
	write("projects/"+prj+".json", `{"_table":"projects","id":"`+prj+`","created_by":"`+act+`"}`)
	write("projects/"+other+".json", `{"_table":"projects","id":"`+other+`","created_by":"`+act+`"}`)

	remove, kept, err := movedDropLeftovers(top)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, k := range remove {
		got = append(got, k.ID.String())
	}
	sort.Strings(got)
	if strings.Join(got, ",") != act+","+other {
		t.Errorf("removed %v, want the actor and the unreferenced project (policies/repos/_meta are the repo's own, created_by is attribution)", got)
	}
	if len(kept) != 1 || !strings.Contains(kept[0], "projects/"+prj+" stays") || !strings.Contains(kept[0], "references it") {
		t.Errorf("kept = %q, want the project the repos row / _meta.json reference, with why", kept)
	}

	// a moved-table row still left: nothing goes
	write("memories/mem_01a1f783602b7cc2ac0cf74d538f3d50.json", `{"_table":"memories","id":"mem_01a1f783602b7cc2ac0cf74d538f3d50","project_id":"`+prj+`"}`)
	if remove, _, err = movedDropLeftovers(top); err != nil || len(remove) != 0 {
		t.Errorf("with a memory left: removed %v (err %v), want nothing", remove, err)
	}
}
