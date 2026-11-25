package gitcommit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestIsAgentSignature(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, email string
		agent       bool
	}{
		{"Alice Example via codex", "agent.auto/codex/abc123@lore.invalid", true},
		{"ci", "agent.ci@LORE.invalid", true},
		{"Alice via codex", "alice@example.com", true},
		{"Alice Example", "alice@example.com", false},
		{"Olivia", "olivia@example.com", false},
		{"lore", "lore@localhost", false},
	} {
		if got := IsAgentSignature(c.name, c.email); got != c.agent {
			t.Errorf("IsAgentSignature(%q, %q) = %v, want %v", c.name, c.email, got, c.agent)
		}
	}
}

// signerFiles are the only sources that put a commit author or committer
// into git's environment or config by hand (devco:D-983). Each one either
// applies IsAgentSignature to what it is given (gitref MakeCommit, the git
// backend's identityEnv/validSignature) or signs with a fixed identity of
// lore itself that never leaves a temp repo or a backup ref. A new file
// that signs commits another way must apply the same test and be added
// here, or the guard fails.
var signerFiles = []string{
	"adapters/backend/git/backup.go",        // lore <lore@localhost>, local backup refs
	"adapters/backend/git/identity.go",      // validSignature, identityEnv, hasIdentity
	"adapters/gitref/write.go",              // MakeCommit refuses ErrAgentSignature
	"adapters/probe/git.go",                 // a dry-run probe commit in a temp repo
	"internal/opstest/gitcommit.go",         // test helper package
	"internal/partialclone/partialclone.go", // test helper package: blobless clones in temp dirs
}

// agentWordFiles may spell the agent markers in a string: this package,
// and the doctor check that finds old commits carrying them.
var agentWordFiles = []string{
	"adapters/gitcommit/signature.go",
	"wire/doctor_agentcommits.go",
}

// TestNoSignatureBuiltAnotherWay is the guard of devco:D-983 (H-248): lore
// signed commits as "<person> via <tool>" <agent.auto/…@lore.invalid> and
// named the agent in a Lore-Agent trailer. Every non-test source of the
// module is parsed: a string literal that sets GIT_AUTHOR_*/GIT_COMMITTER_*
// or user.name= outside signerFiles, or one that holds lore.invalid or a
// Lore-Agent: trailer outside agentWordFiles, fails.
func TestNoSignatureBuiltAnotherWay(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	signs := []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "user.name=", "user.email="}
	agentWords := []string{AgentEmailDomain, "Lore-Agent:"}
	fset := token.NewFileSet()
	seen := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		if d.IsDir() {
			if rel == "tests" || rel == "docs" || strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || strings.Contains(rel, "test/") || strings.HasSuffix(filepath.Dir(rel), "test") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		seen++
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				return true
			}
			for _, w := range signs {
				if strings.Contains(v, w) && !slices.Contains(signerFiles, rel) {
					t.Errorf("%s: %q signs a commit outside the guarded signers (apply gitcommit.IsAgentSignature and add the file to signerFiles)", fset.Position(lit.Pos()), v)
				}
			}
			for _, w := range agentWords {
				if strings.Contains(v, w) && !slices.Contains(agentWordFiles, rel) {
					t.Errorf("%s: %q spells an agent signature or trailer; no lore commit names an agent (devco:D-983)", fset.Position(lit.Pos()), v)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 100 {
		t.Fatalf("parsed only %d source files under %s: the guard is not reading the module", seen, root)
	}
}
