package reposync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkdirs creates every folder under base.
func mkdirs(t *testing.T, base string, rels ...string) {
	t.Helper()
	for _, r := range rels {
		if err := os.MkdirAll(filepath.Join(base, filepath.FromSlash(r)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
}

// workflowFiles lists the files in the repository's workflow folder.
func workflowFiles(t *testing.T, top string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(top, ".github", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// projectDir resolves the project from any folder inside it, nearest
// .lore/ first, and leaves a folder outside every project as given.
func TestProjectDir(t *testing.T) {
	t.Parallel()
	top := gitInitRepo(t)
	mkdirs(t, top, ".lore", "src/deep", "svc/api/.lore", "svc/api/internal/x", "docs")
	for _, tc := range []struct{ dir, want string }{
		{".", "."},
		{"src", "."},
		{"src/deep", "."},
		{"svc", "."},
		{"svc/api", "svc/api"},
		{"svc/api/internal/x", "svc/api"},
		{"docs", "."},
	} {
		got := projectDir(top, filepath.Join(top, filepath.FromSlash(tc.dir)))
		if want := filepath.Join(top, filepath.FromSlash(tc.want)); got != want {
			t.Errorf("projectDir(%s) = %s, want %s", tc.dir, got, want)
		}
	}
	// No .lore/ anywhere up to the work tree root: dir as given.
	bare := gitInitRepo(t)
	mkdirs(t, bare, "a/b")
	in := filepath.Join(bare, "a", "b")
	if got := projectDir(bare, in); got != in {
		t.Fatalf("no project: %s, want %s", got, in)
	}
	// Outside the work tree: as given.
	other := t.TempDir()
	if got := projectDir(top, other); got != other {
		t.Fatalf("outside: %s, want %s", got, other)
	}
}

// E-FATES-6: install-action run from a plain subfolder writes (and on a
// re-run updates) the project's one workflow, never a second file named
// after the subfolder; the same for a project in a subfolder of the repo.
func TestInstallFromSubfolderWritesNoSecondFile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, project, sub, file string }{
		{"root project", ".", "src/deep", "lore-sync-merge.yml"},
		{"subfolder project", "svc/api", "svc/api/internal", "lore-sync-merge-svc-api.yml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			top := gitInitRepo(t)
			mkdirs(t, top, tc.project+"/.lore", tc.sub)
			sub := filepath.Join(top, filepath.FromSlash(tc.sub))
			first := installAt(t, sub, syncActionOptions{})
			if filepath.Base(first) != tc.file {
				t.Fatalf("installed %s, want %s", first, tc.file)
			}
			again := installAt(t, filepath.Join(top, filepath.FromSlash(tc.project)), syncActionOptions{})
			if again != first {
				t.Fatalf("from the project folder: %s, from the subfolder: %s", again, first)
			}
			if got := workflowFiles(t, top); len(got) != 1 || got[0] != tc.file {
				t.Fatalf("workflow files = %v, want only %s", got, tc.file)
			}
			// The pin refresh from the subfolder moves that same file.
			r := refresh(t, sub, "0.1.12")
			if r.Path != first || r.To != "v0.1.12" {
				t.Fatalf("refresh from the subfolder = %+v, want %s moved", r, first)
			}
			if got := workflowFiles(t, top); len(got) != 1 {
				t.Fatalf("workflow files after refresh = %v", got)
			}
		})
	}
}

// Peek and a promote dry run from a plain subfolder of the project read the
// project's rows (E-FATES-6), at the root and in a subfolder project.
func TestPeekPromoteFromSubfolder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, proj := range []string{"", "svc/api"} {
		t.Run("project at "+proj, func(t *testing.T) {
			t.Parallel()
			_, dir := branchRepo(t, proj)
			mkdirs(t, dir, "internal/x")
			sub := filepath.Join(dir, "internal", "x")
			rows, err := Peek(ctx, sub, "feature/x", "tasks")
			if err != nil || len(rows) != 1 || rows[0].ID != "tsk_c" {
				t.Fatalf("peek from a subfolder: %+v %v", rows, err)
			}
			res, err := Promote(ctx, PromoteOptions{Dir: sub, IDs: []string{"rul_a"}, Branch: "feature/x", DryRun: true})
			want := strings.TrimSuffix(proj+"/", "/")
			if want != "" {
				want += "/"
			}
			if err != nil || len(res.Paths) != 1 || res.Paths[0] != want+".lore/data/rules/rul_a.json" {
				t.Fatalf("promote from a subfolder: %+v %v", res, err)
			}
		})
	}
}

// Port of v0.1.17 TestUC_MiscDisplayPath.
func TestDisplayPath(t *testing.T) {
	t.Parallel()
	root := filepath.Join(string(filepath.Separator), "repo", "proj")
	for _, tc := range []struct{ path, want string }{
		{filepath.Join(root, ".github", "workflows", "lore-sync-merge.yml"), ".github/workflows/lore-sync-merge.yml"},
		{root, "."},
		{filepath.Join(string(filepath.Separator), "elsewhere", "x.yml"), filepath.Join(string(filepath.Separator), "elsewhere", "x.yml")},
		{"", "lore-sync-merge workflow"},
	} {
		if got := DisplayPath(root, tc.path); got != tc.want {
			t.Fatalf("DisplayPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
