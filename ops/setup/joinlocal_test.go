package setup

import (
	"strings"
	"testing"
)

// TestJoinLinesSayLocalIsMachineOnly (H-17): the join lines pick the local
// personal store; the text then says that it lives on this machine only
// and how to keep personal notes in a private git repo instead.
func TestJoinLinesSayLocalIsMachineOnly(t *testing.T) {
	t.Parallel()
	local := JoinOut{Commands: []string{
		`lore setup apply --setup acme --step personal_store --answers '{"personal_store":"local"}'`,
		`lore setup apply --setup acme --step folders --answers '{"default":true}'`,
	}}
	got := local.Text()
	for _, want := range []string{"on this machine only", `"personal_store":"existing","store_url":"<private-repo-url>"`, "lore store set-url --name me"} {
		if !strings.Contains(got, want) {
			t.Errorf("join text lacks %q:\n%s", want, got)
		}
	}
	repo := JoinOut{Commands: []string{`lore setup apply --setup acme --step personal_store --answers '{"personal_store":"existing","store_url":"git@git.example.test:me/notes.git"}'`}}
	if strings.Contains(repo.Text(), "on this machine only") {
		t.Errorf("a git personal store got the local note:\n%s", repo.Text())
	}
}
