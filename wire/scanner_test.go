package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// TestRepoGitleaksAllow pins D8 end to end: an allow regex in the repo's
// .gitleaks.toml lets a write through that the same text without the
// file is refused for; a [[rules]] table in it adds nothing.
func TestRepoGitleaksAllow(t *testing.T) {
	t.Parallel()
	key := "AKIA" + strings.Repeat("Q", 16)
	repo := gitRepo(t)
	file := "[[rules]]\nid = \"x\"\nregex = '''docsonly'''\n\n[[allowlists]]\nregexes = ['''^AKIAQQQQ''']\n"
	if err := os.WriteFile(filepath.Join(repo, repoGitleaksFile), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, repo)
	withProject(t, a)
	if _, err := run(t, a, opsmemory.OpAdd, `{"body":"deploy with `+key+`"}`, false); err != nil {
		t.Fatalf("an allow pattern of the repo .gitleaks.toml must let the write through: %v", err)
	}
	if _, err := run(t, a, opsmemory.OpAdd, `{"body":"the word docsonly is no rule"}`, false); err != nil {
		t.Fatalf("a [[rules]] table of .gitleaks.toml must be ignored: %v", err)
	}
	other := "AKIA" + strings.Repeat("Z", 16)
	if _, err := run(t, a, opsmemory.OpAdd, `{"body":"deploy with `+other+`"}`, false); err == nil {
		t.Fatalf("a value the allow does not match must still be refused")
	}
}
