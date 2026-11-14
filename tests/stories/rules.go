//go:build stories && !windows

package stories

// rules.go — the owner's standing rules as checks every story can make:
//
//	nothing pushes without push/sync   RefsOf before, NoPushSince after
//	a repo without lore data stays     TreeOf before, Untouched after
//	  byte-identical
//	old-format projects stay old       NewFormatFiles (empty = still 0.1.17)
//	  until `lore migrate run`
//	no secret value printed            FakeSecret + NotPrinted
//	named flags in every hint          PositionalHints

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// RefsOf is every ref of a remote with the commit it points at, one
// "<sha> <ref>" line each, sorted: compare two of them to see a push.
func (s *Story) RefsOf(r *teamharness.Remote) string {
	s.T.Helper()
	return s.Git(r.Dir, "for-each-ref", "--sort=refname", "--format=%(objectname) %(refname)")
}

// NoPushSince fails when any ref of r moved, appeared or vanished since
// before (a RefsOf): only lore sync, sync land, store push or the person's own
// git push may change a remote. what names the commands run in between.
func (s *Story) NoPushSince(t testing.TB, r *teamharness.Remote, before, what string) {
	t.Helper()
	if now := s.RefsOf(r); now != before {
		t.Errorf("[%s] %s moved refs of %s without a push or sync:\nbefore:\n%s\nafter:\n%s", s.ID, what, r.Slug, before, now)
	}
}

// TreeOf hashes what "byte-identical" means for a repo with no lore data:
// every work-tree file (path, mode, bytes) plus .git/config, .git/HEAD,
// .git/hooks and .git/info. The index and objects are left out (git status
// refreshes the index's stat cache; that is git's, not lore's).
func TreeOf(t testing.TB, repo string) string {
	t.Helper()
	h := sha256.New()
	var lines []string
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repo, p)
		if d.IsDir() {
			if rel == ".git" {
				return nil
			}
			if strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
				top := strings.Split(rel, string(filepath.Separator))[1]
				if top != "hooks" && top != "info" {
					return filepath.SkipDir
				}
			}
			lines = append(lines, "d "+rel)
			return nil
		}
		if strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) == 2 && parts[1] != "config" && parts[1] != "HEAD" {
				return nil
			}
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		var body []byte
		if info.Mode()&fs.ModeSymlink != 0 {
			target, lerr := os.Readlink(p)
			if lerr != nil {
				return lerr
			}
			body = []byte("-> " + target)
		} else if body, err = os.ReadFile(p); err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		lines = append(lines, info.Mode().String()+" "+rel+" "+hex.EncodeToString(sum[:8]))
		return nil
	})
	if err != nil {
		t.Fatalf("hash %s: %v", repo, err)
	}
	sort.Strings(lines)
	for _, l := range lines {
		h.Write([]byte(l + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil)) + "\n" + strings.Join(lines, "\n")
}

// Untouched fails when repo differs from before (a TreeOf), naming the
// entries that changed.
func (s *Story) Untouched(t testing.TB, repo, before, what string) {
	t.Helper()
	if diff := TreeDiff(before, TreeOf(t, repo)); len(diff) > 0 {
		t.Errorf("[%s] %s changed %s, a repo with no lore data (it must stay byte-identical):\n%s", s.ID, what, repo, strings.Join(diff, "\n"))
	}
}

// TreeDiff lists the entries of two TreeOf snapshots that differ ("+ " in
// now only, "- " in before only); empty when the trees are the same.
func TreeDiff(before, now string) []string {
	if before == now {
		return nil
	}
	set := func(snap string) map[string]bool {
		m := map[string]bool{}
		lines := strings.Split(snap, "\n")
		for _, l := range lines[1:] {
			m[l] = true
		}
		return m
	}
	was, is := set(before), set(now)
	var diff []string
	for l := range is {
		if !was[l] {
			diff = append(diff, "+ "+l)
		}
	}
	for l := range was {
		if !is[l] {
			diff = append(diff, "- "+l)
		}
	}
	sort.Strings(diff)
	if len(diff) == 0 {
		diff = []string{"(same entries, different hash)"}
	}
	return diff
}

// NewFormatFiles lists the files under repo/.lore/data that are not in lore
// 0.1.17's format (`"_v": 1` in every row file and in _meta.json): empty
// means the project is still on the old format, as it must stay until a
// person runs `lore migrate run`.
func NewFormatFiles(t testing.TB, repo string) []string {
	t.Helper()
	data := filepath.Join(repo, ".lore", "data")
	var out []string
	err := filepath.WalkDir(data, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".json" {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var head struct {
			V json.Number `json:"_v"`
		}
		rel, _ := filepath.Rel(repo, p)
		if json.Unmarshal(b, &head) != nil || head.V != "1" {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", data, err)
	}
	return out
}

// FakeSecret builds a secret-shaped value at run time (a GitHub-token shape
// the leak rules know), so no key-looking literal is ever committed.
func FakeSecret(seed string) string {
	sum := sha256.Sum256([]byte("stories-fake-secret/" + seed))
	return "gh" + "p_" + strings.ToUpper(hex.EncodeToString(sum[:]))[:36]
}

// NotPrinted fails when lore printed secret (or its last 12 characters,
// enough to identify it) in any of rs.
func (s *Story) NotPrinted(t testing.TB, secret string, rs ...teamharness.Result) {
	t.Helper()
	for _, args := range Printed(secret, rs...) {
		t.Errorf("[%s] lore printed a secret value (or its tail) in %q", s.ID, args)
	}
}

// Printed returns the argument lists of the runs in rs whose output holds
// secret or its last 12 characters.
func Printed(secret string, rs ...teamharness.Result) [][]string {
	tail := secret
	if len(tail) > 12 {
		tail = tail[len(tail)-12:]
	}
	var out [][]string
	for _, r := range rs {
		if o := r.Stdout + r.Stderr; strings.Contains(o, secret) || strings.Contains(o, tail) {
			out = append(out, r.Args)
		}
	}
	return out
}

// positional matches a "run this" line that hands a value to a lore command
// without a flag: `lore task add "x"`, `lore task show tsk_…`. The owner's
// rule (U-4) is named flags in every hint lore prints.
var positional = regexp.MustCompile(`\blore (?:task|memory|rule|decision|tasklist|mission|plan|reminder|hotfix|pattern|note) (?:add|edit|show|done|delete|restore|start|claim|move) (?:"|'|[a-z]{3}_[0-9a-f]{6,})`)

// PositionalHints returns the lines of out that show a lore command with a
// positional value instead of its named flag.
func PositionalHints(out string) []string {
	var bad []string
	for _, l := range strings.Split(out, "\n") {
		if positional.MatchString(l) {
			bad = append(bad, strings.TrimSpace(l))
		}
	}
	return bad
}

// NamedFlags fails when any of rs printed a hint with a positional value.
func (s *Story) NamedFlags(t testing.TB, rs ...teamharness.Result) {
	t.Helper()
	for _, r := range rs {
		if bad := PositionalHints(r.Stdout + "\n" + r.Stderr); len(bad) > 0 {
			t.Errorf("[%s] %q printed a hint without named flags: %q", s.ID, r.Args, bad)
		}
	}
}
