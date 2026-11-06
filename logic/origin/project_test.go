package origin

import "testing"

// TestBuildAndFilterProject (PROVENANCE 5.1, 8.1, G-ORGMIG-7): `_o.project`
// is written when the place has a project and left out when it has none;
// a client copy keeps it only when it names the client repo's own project.
func TestBuildAndFilterProject(t *testing.T) {
	const own, other = "prj_01a1a5aca7f57659be7320c5f4b70011", "prj_01a1a5aca7f57659be7320c5f4b70022"
	base := Facts{Cmd: "memory add", Store: "repo", Repo: "rep_01a1a5aca7f57659be7320c5f4b70033", Git: Git{State: GitBranch, Branch: "main", Commit: "abc"}}
	if o, err := Build(base); err != nil || Text(o, KeyProject) != "" {
		t.Fatalf("no project: %s %v", o.Raw(), err)
	}
	f := base
	f.Project = own
	o, err := Build(f)
	if err != nil || Text(o, KeyProject) != own {
		t.Fatalf("project not written: %s %v", o.Raw(), err)
	}
	cases := []struct {
		name   string
		client Client
		keep   bool
	}{
		{"client with no own project id", Client{OwnStore: "repo", RepoID: base.Repo, BornHere: true}, false},
		{"client's own project", Client{OwnStore: "repo", RepoID: base.Repo, ProjectID: own, BornHere: true}, true},
		{"another project", Client{OwnStore: "repo", RepoID: base.Repo, ProjectID: other, BornHere: true}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ferr := ClientFilter(o, c.client)
			if ferr != nil {
				t.Fatal(ferr)
			}
			if (Text(got, KeyProject) == own) != c.keep {
				t.Fatalf("project kept = %v, want %v: %s", Text(got, KeyProject) == own, c.keep, got.Raw())
			}
		})
	}
}
