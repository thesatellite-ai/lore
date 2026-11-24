//go:build journeys && !windows

package journeys

// folderkinds.go — devco D-965: the content kinds whose list and search
// keep rows by project (ops/content ProjectScope), with an add command
// each, so the journey (solo shape) and story WW (team shape) cells write
// and read the same kinds.

import "strings"

// FolderKind is one content kind of the D-965 cells: its add arguments
// (%t is the target folder, %s the row's mark) and whether it has a
// search op. Story WW reads the same kinds in the team shape.
type FolderKind struct {
	Kind   string
	Add    []string
	Search bool
}

// FolderKinds are every content kind whose list and search keep rows by
// project (ops/content ProjectScope).
var FolderKinds = []FolderKind{
	{"architecturenote", []string{"--to", "%t", "--title", "%s", "--body", "as built"}, true},
	{"behaviour", []string{"--to", "%t", "--title", "%s", "--body", "be brief"}, true},
	{"cookbookrecipe", []string{"--to", "%t", "--title", "%s", "--body", "step one"}, true},
	{"external-source", []string{"--to", "%t", "--kind", "github", "--name", "%s"}, false},
	{"incident", []string{"--to", "%t", "--title", "%s", "--body", "it broke"}, true},
	{"pattern", []string{"--to", "%t", "--title", "%s", "--body", "retry"}, true},
	{"playbook", []string{"--to", "%t", "--title", "%s", "--body", "run it"}, true},
	{"prompt", []string{"--to", "%t", "--title", "%s", "--body", "ask it"}, true},
	{"snapshot", []string{"--to", "%t", "--title", "%s", "--body", "state"}, true},
	{"suggestion", []string{"--to", "%t", "--title", "%s", "--body", "try it"}, true},
	{"tastepref", []string{"--to", "%t", "--body", "%s"}, true},
	{"techdoc", []string{"--to", "%t", "--name", "%s", "--description", "the docs"}, true},
	{"workflow", []string{"--to", "%t", "--title", "%s", "--body", "flow"}, true},
	{"workspace", []string{"--to", "%t", "--title", "%s", "--body", "space"}, true},
}

// AddArgs is the kind's add command aimed at folder, its row marked mark.
func (k FolderKind) AddArgs(folder, mark string) []string {
	args := []string{k.Kind, "add", "--yes"}
	for _, a := range k.Add {
		args = append(args, strings.NewReplacer("%t", folder, "%s", mark).Replace(a))
	}
	return args
}

// Mark is the made-up word the kind's row of a D-965 cell carries.
func (k FolderKind) Mark() string { return "d965" + strings.ReplaceAll(k.Kind, "-", "") }
