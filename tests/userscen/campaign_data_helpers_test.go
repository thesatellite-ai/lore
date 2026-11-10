//go:build userscen

package userscen

// campaign_data_helpers_test.go — shared helpers of the data-area campaign
// (secrets, vars, resource, meta, link, comment, tag, pin, external-source):
// every refusal is checked against docs/reference/errors.md (code → exit),
// a dry run is checked by hashing what a commit would carry, and ids are
// read from --json. The repo fixtures are the resources area's resRepo and
// resHostService (resources_helpers_test.go), reused, not copied.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// cdErrorsDoc is the error table every refusal is checked against.
const cdErrorsDoc = "../../docs/reference/errors.md"

// cdErrRow matches one row of the errors table: | `E_X` | 20 | … |.
var cdErrRow = regexp.MustCompile("^\\| `(E_[A-Z0-9_]+)` \\| ([0-9]+) \\|")

var (
	cdExitsOnce sync.Once
	cdExits     map[string]int
	cdExitsErr  error
)

// cdDocExit is the exit code docs/reference/errors.md gives code.
func cdDocExit(t *testing.T, code string) int {
	t.Helper()
	cdExitsOnce.Do(func() {
		f, err := os.Open(cdErrorsDoc)
		if err != nil {
			cdExitsErr = err
			return
		}
		defer func() { _ = f.Close() }()
		cdExits = map[string]int{}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if m := cdErrRow.FindStringSubmatch(sc.Text()); m != nil {
				n, _ := strconv.Atoi(m[2])
				cdExits[m[1]] = n
			}
		}
		cdExitsErr = sc.Err()
	})
	if cdExitsErr != nil {
		t.Fatalf("read %s: %v", cdErrorsDoc, cdExitsErr)
	}
	n, ok := cdExits[code]
	if !ok {
		t.Fatalf("%s is not in %s", code, cdErrorsDoc)
	}
	return n
}

// cdRefused runs a --json refusal check: the envelope carries code, the
// process exits with the documented exit for that code, the envelope's
// exit agrees, and the error names a next step (a hint, or a message that
// already names a command). It returns the error object.
func cdRefused(t *testing.T, r th.Result, code string) *restErr {
	t.Helper()
	if r.Code == 0 {
		t.Fatalf("expected %s, got success: %s", code, r)
	}
	e := restCLIErr(r)
	if e == nil {
		t.Fatalf("expected a JSON error envelope with %s: %s", code, r)
	}
	if e.Code != code {
		t.Fatalf("expected %s, got %s (%s): %s", code, e.Code, e.Message, r)
	}
	want := cdDocExit(t, code)
	if r.Code != want || e.Exit != want {
		t.Fatalf("%s: process exit %d, envelope exit %d, errors.md says %d: %s", code, r.Code, e.Exit, want, r)
	}
	if strings.TrimSpace(e.Hint) == "" && !strings.Contains(e.Message, "lore ") {
		t.Errorf("%s names no next step (no hint, no command in the message): %q", code, e.Message)
	}
	return e
}

// cdJSON runs a --json command and decodes its stdout into a generic map.
func cdJSON(t *testing.T, c *th.Clone, args ...string) map[string]json.RawMessage {
	t.Helper()
	r := c.Lore(append(args, "--json")...).OK(t)
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.Stdout), &m); err != nil {
		t.Fatalf("decode %v: %v\n%s", args, err, r)
	}
	return m
}

// cdTreeHash hashes every file a commit would carry from the clone (the
// working tree outside .git, .lore/cache and the repo's own tmp files) and
// the git status, so a dry run that writes anything changes the hash.
func cdTreeHash(t *testing.T, c *th.Clone) string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(c.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(c.Dir, p)
		if d.IsDir() && (rel == ".git" || strings.HasPrefix(rel, filepath.Join(".lore", "cache")) || rel == filepath.Join(".lore", "tmp")) {
			return filepath.SkipDir
		}
		if !d.IsDir() && !strings.HasSuffix(rel, ".lock") && !strings.Contains(rel, "cache") {
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		b, err := os.ReadFile(filepath.Join(c.Dir, p))
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(p + "\x00"))
		h.Write(b)
	}
	h.Write([]byte(c.Status()))
	return hex.EncodeToString(h.Sum(nil))
}

// cdHelpCmds are the 90 commands of the data area (inventory.md).
var cdHelpCmds = []string{
	"comment add", "comment delete", "comment list", "comment search",
	"external-source add", "external-source disable", "external-source enable", "external-source list",
	"link add", "link list", "link remove",
	"meta add", "meta adopt", "meta delete", "meta describe", "meta edit", "meta export", "meta forget",
	"meta get", "meta history", "meta import", "meta keys", "meta lint", "meta move", "meta rebuild",
	"meta rehome", "meta rename", "meta set", "meta show", "meta unset", "meta value add", "meta value rename",
	"pin set", "pin unset",
	"resource add", "resource adopt", "resource archive", "resource check", "resource clone", "resource delete",
	"resource edit", "resource export", "resource field add", "resource field edit", "resource field remove",
	"resource field rename", "resource field retype", "resource graph", "resource history", "resource impact",
	"resource import", "resource kind add", "resource kind edit", "resource kind rename", "resource kind show",
	"resource kinds", "resource lint", "resource list", "resource merge", "resource rehome", "resource rename",
	"resource repoint", "resource report", "resource show", "resource stale",
	"secrets allow list", "secrets allow rm", "secrets check", "secrets link", "secrets list", "secrets move",
	"secrets relabel", "secrets rename-entry", "secrets rule add", "secrets rule edit", "secrets rule off",
	"secrets rule on", "secrets rules", "secrets show",
	"tag add", "tag attach", "tag detach", "tag edit", "tag list",
	"vars list", "vars rename", "vars rm", "vars set", "vars show", "vars where",
}

// cdIDOf reads the id a --json write printed ({"id"} or {"data":{"id"}}).
func cdIDOf(t *testing.T, r th.Result) string { t.Helper(); return secID(t, r) }

// cdLong is a 10k-character text of one repeated word (no secret shape).
func cdLong() string { return strings.Repeat("lorem ", 10000/6+1)[:10000] }

// cdMetaShowJSON decodes `lore meta show <id> --json`.
func cdMetaShowJSON(t *testing.T, c *th.Clone, id string) cdMetaShow {
	t.Helper()
	return th.DecodeJSON[cdMetaShow](t, c.Lore("meta", "show", id, "--json"))
}
