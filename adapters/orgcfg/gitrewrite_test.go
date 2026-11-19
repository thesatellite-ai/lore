package orgcfg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestGitRewrite (devco D-680): a store url is answered as git itself
// rewrites it from the person's git config: the longest insteadOf prefix
// wins, a dotted alias and the canonical spelling reach one mirror, and a
// url no rule names is kept.
func TestGitRewrite(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "gitconfig")
	body := "[url \"/srv/mirror/\"]\n\tinsteadOf = git@github.com:\n\tinsteadOf = git@github.com-work:\n" +
		"[url \"/srv/other/\"]\n\tinsteadOf = git@github.com-work:acme-co/special\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ctx := context.Background()
	for raw, want := range map[string]string{
		"git@github.com-work:acme-co/lore-team.git": "/srv/mirror/acme-co/lore-team.git",
		"git@github.com:acme-co/lore-team.git":      "/srv/mirror/acme-co/lore-team.git",
		"git@github.com-work:acme-co/special.git":   "/srv/other/.git",
		"https://github.com/acme-co/lore-team.git":  "https://github.com/acme-co/lore-team.git",
	} {
		if got := GitRewrite(ctx, raw); got != want {
			t.Errorf("GitRewrite(%s) = %s, want %s", raw, got, want)
		}
	}
}
