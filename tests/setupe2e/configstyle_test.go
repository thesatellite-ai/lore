//go:build setupe2e

package setupe2e

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	opsconfig "github.com/thesatellite-ai/lore/ops/config"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// TestConfigStyleRunIt (OWN7-6, owner 2026-11-10): a config.yaml an older
// lore saved with its setups on one line (doctor's config_style warning)
// shows on the page through setup.list's config_style, and the page's Run
// it, config.fmt on the page allowlist, rewrites it in block style through
// the page's own REST server (session cookie, CSRF, Origin). After it
// setup.list reports no config_style and the setup is still there.
func TestConfigStyleRunIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := Shape{GHAccounts: 1, GHActive: ActiveFirst, SSH: SSHDefaultOnly, Keys: KeysWithPub, LoreHome: HomeEmpty, Start: StartPlain}
	w, err := Build(ctx, filepath.Join(t.TempDir(), "w"), s, loreBin)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(w.Home, ".lore", "config.yaml")
	const flow = "# kept by lore config fmt\nsetups: [{name: personal, kind: solo, default: true, folders: [~/code/**]}]\n"
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(flow), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx, 0, loreBin); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	c, err := NewClient(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	list, err := Call[struct{}, opssetup.ListOut](ctx, c, "setup.list", struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(list.ConfigStyle.Keys, "setups") || !slices.Equal(list.ConfigStyle.Command, []string{"lore", "config", "fmt"}) {
		t.Fatalf("setup.list config_style = %+v, want setups on one line and the fix lore config fmt", list.ConfigStyle)
	}
	out, err := Call[opsconfig.FmtIn, opsconfig.FmtOut](ctx, c, "config.fmt", opsconfig.FmtIn{})
	if err != nil {
		t.Fatalf("the page's Run it (config.fmt): %v", err)
	}
	if !out.Written || !slices.Contains(out.Keys, "setups") {
		t.Fatalf("config.fmt = %+v, want the file rewritten", out)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "[{") || !strings.Contains(string(raw), "# kept by lore config fmt") {
		t.Fatalf("config.yaml after Run it:\n%s", raw)
	}
	list, err = Call[struct{}, opssetup.ListOut](ctx, c, "setup.list", struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !list.ConfigStyle.IsZero() || len(list.Setups) != 1 || list.Setups[0].Name != "personal" {
		t.Fatalf("after Run it: config_style %+v, setups %+v", list.ConfigStyle, list.Setups)
	}
}
