//go:build userscen

package userscen

// campaign_render_helpers_test.go — shared fixtures of the render-area
// campaign (TestCampaignRender_*): a person's fresh clone before
// `lore init`, a hash of a clone's work tree (a read-only command writes
// nothing), the exit codes of the error classes the harness does not name,
// and a JSON decoder for answers that are not enveloped. The render
// fixtures of render_helpers_test.go (renderSolo, renderStartAPI, the
// pointer blocks) are reused, never copied.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Exit codes of the error classes (model/errs): bad input is 20, a guard
// (E_NOT_A_STORE, E_LEAK_BLOCKED) 19, a refusal 23, not found 27, an
// unconfirmed question without a terminal 26.
const (
	campExitBadInput = 20
	campExitGuard    = th.ExitGuard
	campExitRefused  = th.ExitRefused
	campExitNotFound = th.ExitNotFound
	campExitConfirm  = th.ExitNeedsConfirm
)

// campFresh is one person's fresh clone of their own repo, NOT yet
// lore-initialised (the init matrix starts from here).
func campFresh(t *testing.T) *th.Clone {
	t.Helper()
	w := newWorld(t)
	p := w.Human("person", "person@example.com")
	p.WriteConfig(renderSoloConfig())
	return p.Clone(w.Remote(renderSlugOwn), "tool")
}

// campTree is a digest of every file under dir, by path (mode and bytes),
// .git included except what git and lore refresh without anything a person
// would call a write: git's index, logs and fetched refs (a fresh live
// fetch updates FETCH_HEAD and refs/remotes), and lore's cache, state and
// lock files.
func campTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	skip := []string{
		filepath.Join(".git", "index"), filepath.Join(".git", "logs"), filepath.Join(".git", "FETCH_HEAD"),
		filepath.Join(".git", "refs", "remotes"), filepath.Join(".git", "refs", "lore"), filepath.Join(".git", "packed-refs"),
		filepath.Join(".git", "objects"), filepath.Join(".git", "lore"),
		filepath.Join(".lore", "lore-cache.db"), filepath.Join(".lore", "state"), filepath.Join(".lore", "lock"),
	}
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		for _, s := range skip {
			if rel == s || strings.HasPrefix(rel, s+string(filepath.Separator)) || strings.HasPrefix(rel, s+"-") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		sum := sha256.Sum256(b)
		out[rel] = info.Mode().String() + " " + hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// campSameTree fails, naming each file, when after differs from before.
func campSameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	var diff []string
	for f, b := range before {
		if a, ok := after[f]; !ok {
			diff = append(diff, "removed "+f)
		} else if a != b {
			diff = append(diff, "changed "+f)
		}
	}
	for f := range after {
		if _, ok := before[f]; !ok {
			diff = append(diff, "added "+f)
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		t.Errorf("%s writes nothing, but: %s", what, strings.Join(diff, ", "))
	}
}

// campJSON decodes a successful command's stdout (not enveloped) into T.
func campJSON[T any](t *testing.T, r th.Result) T {
	t.Helper()
	r.OK(t)
	var v T
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("stdout is JSON: %v\n%s", err, r)
	}
	return v
}

// campErr is the --json error envelope every refusal prints on stdout.
type campErr struct {
	Error struct {
		Code  string `json:"code"`
		Hint  string `json:"hint"`
		Exit  int    `json:"exit"`
		Class string `json:"class"`
	} `json:"error"`
}

// campRefusal checks one refusal both ways a caller meets it: the text form
// (exit code, the E_ code in brackets, a "hint:" line naming the next
// step) and the --json envelope (the same code and exit, a non-empty hint).
// args must not already carry --json.
func campRefusal(t *testing.T, c *th.Clone, exit int, code, hintHas string, args ...string) {
	t.Helper()
	r := c.Lore(args...)
	r.Fails(t, exit, "["+code+"]")
	if hintHas != "" && !strings.Contains(r.Out(), "hint: ") {
		t.Errorf("lore %s: the refusal names its next step (a hint line): %s", strings.Join(args, " "), r)
	}
	if hintHas != "" && !strings.Contains(r.Out(), hintHas) {
		t.Errorf("lore %s: the hint says %q: %s", strings.Join(args, " "), hintHas, r)
	}
	jr := c.Lore(append(args, "--json")...)
	var e campErr
	if err := json.Unmarshal([]byte(jr.Stdout), &e); err != nil {
		t.Fatalf("lore %s --json: the refusal is a JSON envelope on stdout: %v\n%s", strings.Join(args, " "), err, jr)
	}
	if jr.Code != exit || e.Error.Code != code || e.Error.Exit != exit {
		t.Errorf("lore %s --json: exit %d code %s, got exit %d %+v", strings.Join(args, " "), exit, code, jr.Code, e.Error)
	}
	if hintHas != "" && e.Error.Hint == "" {
		t.Errorf("lore %s --json: the envelope carries the hint: %s", strings.Join(args, " "), jr)
	}
}
