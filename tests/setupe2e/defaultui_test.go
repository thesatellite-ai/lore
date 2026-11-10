//go:build setupe2e

package setupe2e

// defaultui_test.go — the binary built from this module (cmd/lore, which
// calls app.Main with no setup UI) serves setupweb.DefaultUI: the plain page
// naming the CLI commands, with its stylesheet and no script. It proves the
// composition seam from the core side: a build that passes no
// app.WithSetupUI gets the fallback page, not someone else's UI and not a
// 404. (A build that passes a UI checks the other side in its own suite.)

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// defaultUIDeadline bounds the whole check: one world, one start, two GETs.
const defaultUIDeadline = 2 * time.Minute

// defaultUIMarkers are what only the open-core page holds (surface/setupweb/opencore).
var defaultUIMarkers = []string{"The guided setup page comes with lore EE", "lore setup plan", "/setup/assets/opencore.css"}

func TestBinaryServesDefaultUI(t *testing.T) {
	s := Shape{GHAccounts: 1, GHActive: ActiveFirst, SSH: SSHSeveral, Keys: KeysWithPub, IncludeIf: 0, LoreHome: HomeEmpty, Start: StartPlain}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultUIDeadline)
	defer cancel()
	w, err := Build(ctx, t.TempDir(), s, loreBin)
	if err != nil {
		t.Fatalf("build world: %v", err)
	}
	if err := w.Start(ctx, 0, loreBin); err != nil {
		t.Fatalf("start lore setup web: %v", err)
	}
	defer w.Stop()
	page := getPage(ctx, t, w.Origin()+"/setup/")
	for _, want := range defaultUIMarkers {
		if !strings.Contains(page, want) {
			t.Errorf("the core binary's /setup/ lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "<script") {
		t.Errorf("the core binary's /setup/ carries a script; the default page has none:\n%s", page)
	}
	getPage(ctx, t, w.Origin()+"/setup/assets/opencore.css")
}

// getPage GETs url and returns its body, failing on any status but 200.
func getPage(ctx context.Context, t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d\n%s", url, res.StatusCode, b)
	}
	return string(b)
}
