//go:build stories && !windows

package stories

// WW_dryrun_test.go — the dry-run cells of story WW (devco:D-708): a dry
// run writes nothing, ever. `lore migrate run --dry-run` printed "Nothing
// written" while the pin refresh after it rewrote the tracked
// lore-sync-merge workflow; every hook that writes after an operation now
// asks one place (wire/host Run.WritesNothing).
//
// The op list is every operation of the real registry whose input has the
// --dry-run flag, so a dry run added tomorrow joins the matrix the day it
// lands. Each runs with --dry-run (inputs as WW's write cells generate
// them) from the places a person stands: an org-linked repo, a repo lore
// is set up in and a 0.1.17 repo (the last two with an auto-pinned
// workflow an older lore installed, the release moved by
// LORE_TEST_PIN_VERSION), a client repo, a store folder and home naming
// the org. Whatever it answers (a refusal included), the
// whole HOME (every repo and store clone: work tree, .git/config, hooks,
// info, branch refs; the lore home's state; drySnap) must be exactly as it
// was before.

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/wire"
)

// wwDryPlaces are the places of the dry-run cells.
var wwDryPlaces = []string{"own-linked", "claude-link-out", "v0117", "client", "store-folder", "home-to-acme"}

// wwDryFlag is the flag every dry run takes.
const wwDryFlag = "dry-run"

// wwDryOps lists every registry op whose input has --dry-run.
func wwDryOps(t *testing.T, w *wwWorld) []wwOp {
	t.Helper()
	reg, err := wire.Registry(&wire.App{})
	if err != nil {
		t.Fatal(err)
	}
	var out []wwOp
	for _, e := range reg.Entries() {
		sp := e.Spec()
		if sp.Hidden {
			continue
		}
		if _, skip := wwSkip[sp.Name]; skip {
			continue
		}
		in := e.InputFields()
		dry := false
		for _, f := range in {
			dry = dry || f.Flag == wwDryFlag
		}
		if !dry {
			continue
		}
		o := wwOp{wRead: wRead{Name: sp.Name, Path: op.CLIPath(sp.Name), In: in}}
		help := w.m.Lore(w.m.Home, append(append([]string{}, o.Path...), "--help")...).Out()
		o.Yes = strings.Contains(help, "--yes ")
		o.To = strings.Contains(help, "--to string")
		o.Body = strings.Contains(help, "--body string")
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) < 40 {
		t.Fatalf("only %d dry-run ops in the registry; the walk is broken", len(out))
	}
	return out
}

// TestStoryWWDryRunWritesNothing runs every --dry-run op in every dry-run
// place and fails each one that changed a file (D-708).
func TestStoryWWDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	w := wwBuild(t)
	m := w.m
	// an auto-pinned workflow from an older lore in a repo lore is set up
	// in and the 0.1.17 repo, and a release for the test build to move it to
	for _, name := range []string{"claude-link-out", "v0117"} {
		dir := w.places[name].Dir
		m.Lore(dir, "sync", "install-action").OK(t)
		w.s.Git(dir, "add", ".github/workflows/lore-sync-merge.yml")
		w.s.Git(dir, "-c", "user.name="+wwHome.Name, "-c", "user.email="+wwHome.Email, "commit", "-q", "--no-verify", "-m", "lore-sync-merge workflow") // fixture commit, before the cells
	}
	m.SetEnv("LORE_TEST_PIN_VERSION", "0.1.19")
	body := filepath.Join(t.TempDir(), "body.md")
	writeFile(t, body, "matrix body\n")
	ops := wwDryOps(t, w)
	var bad []string
	ran := 0
	w.s.Step("D-708/every-dry-run-writes-nothing-in-every-place", func() {
		for _, pn := range wwDryPlaces {
			p := w.places[pn]
			c := wwCtx{w: w, p: p, org: p.Org, ids: w.ids[p.Org]}
			for _, o := range ops {
				args := append(wwInput(o, c, body), "--"+wwDryFlag)
				before := drySnap(t, m.Home)
				r := m.Lore(p.Dir, args...)
				ran++
				if d := dryDiff(before, drySnap(t, m.Home)); len(d) > 0 {
					if len(d) > 4 {
						d = append(d[:4], "…")
					}
					bad = append(bad, pn+": lore "+strings.Join(args, " ")+" (exit "+strconv.Itoa(r.Code)+") changed:\n    "+strings.Join(d, "\n    "))
				}
			}
		}
	})
	for _, b := range bad {
		t.Errorf("[WW dry-run] %s", b)
	}
	t.Logf("%d dry runs in %d places, %d changed files", ran, len(wwDryPlaces), len(bad))
}
