package wire

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
)

// B4-17: the identity notice is one short line (it was ~400 chars): the
// clone under ~, the identity and who it is; the long why stays in doctor.
func TestCloneIdentityLineShort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".lore", "stores", "pat", "lore-me")
	id := git.CloneIdentity{Signature: git.Signature{Name: "Pat Doe", Email: "pat@example.test"},
		Why: "your identity (identity: in ~/.lore/config.yaml), the owner of this personal store, the one lore signs this store's commits with", Who: "personal store owner"}
	got := cloneIdentityLine(context.Background(), dir, id)
	want := "store clone ~/.lore/stores/pat/lore-me: git user set to Pat Doe <pat@example.test> (personal store owner)"
	if !strings.HasSuffix(got, want) || len(got) > 120 {
		t.Fatalf("line (%d chars) %q, want …%q under 120", len(got), got, want)
	}
	id.Was = "old@example.test"
	if got := cloneIdentityLine(context.Background(), dir, id); !strings.HasSuffix(got, "(personal store owner, was old@example.test)") {
		t.Fatalf("with was: %q", got)
	}
}
