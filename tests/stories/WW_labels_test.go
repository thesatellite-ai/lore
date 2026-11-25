//go:build stories && !windows

package stories

// WW_labels_test.go — the matrix cells of owner use pass 4 weak 1, 6 and 7
// (devco D-685, D-689, D-690) on WW's world, so each item is checked in
// every placement a person stands, not only on story WUL's linked own repo:
//
//	D-685: `lore sql` names a folder's rows `<org>/<folder>` (never the bare
//	       org) from the store folder, the linked own repo, the linked
//	       client repo and the home folder with --setup
//	D-689: one policy key set in two folders gives two ids; a folder keeps
//	       one id however it is attached (store folder and linked repo
//	       write the same row); an old id repeated in two folders is
//	       refused by `show --id` naming both places, and each printed read
//	       works where it was printed
//	D-690: `--to me` with no personal store: every `--to` the refusal
//	       offers works where it was printed; the placeholder form's
//	       `lore setup list` names the folders
//
// One world, sequential cells (they share the seeded rows; D-690 runs last
// because it turns the personal store off).

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// wwlValueRe finds the policy row's value 400 (the JSON field, quoted or
// not), never the same digits inside an id or a stamp.
var wwlValueRe = regexp.MustCompile(`("value"\s*:\s*"?)400\b`)

// Names of the label cells (made up).
const (
	wwlAPI = "api"
	wwlKey = "trash.retention_days"
)

// wwlLabel is a sql source naming a store folder: <org>/<folder>.
var wwlLabel = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[A-Za-z0-9_.-]+$`)

// wwlTo is one --to value a refusal hint offers: repo, an org or an
// <org>/<folder> (placeholders and prose such as "--to takes" excluded).
var wwlTo = regexp.MustCompile("--to (repo|" + wOrg + "|" + wOrg + "/[A-Za-z0-9_.-]+)(?:[\\s,`]|$)")

// wwlShowRead is one printed `lore show --id … --store …` read.
var wwlShowRead = regexp.MustCompile("`lore show --id (\\S+) --store ([^\\s`]+)`")

// TestStoryWWLabelCells runs the D-685, D-689 and D-690 cells per placement.
func TestStoryWWLabelCells(t *testing.T) {
	t.Parallel()
	w := wwBuild(t)
	m := w.m
	setup := []string{"--setup", wSetup}
	inWeb, inAPI := wOrg+"/"+wFolder, wOrg+"/"+wwlAPI
	type place struct {
		name, dir string
		extra     []string // flags a read there needs (home: --store, --setup)
		label     string   // the folder label its rows carry
	}
	sf, own, cl := w.places["store-folder"].Dir, w.places["own-linked"].Dir, w.places["client"].Dir
	clientLabel := wOrg + "/" + wClientProj
	places := []place{
		{"store-folder", sf, nil, inWeb},
		// both repos are org-linked client repos: their linked store is
		// internal there, so sql reads it with --internal (CFG4)
		{"own-linked", own, []string{"--internal"}, inWeb},
		{"client", cl, []string{"--internal"}, clientLabel},
		{"home", m.Home, append([]string{"--store", inWeb}, setup...), inWeb},
	}

	s := w.s
	s.Step("D-685/sql-source-is-the-folder-in-every-placement", func() {
		m.Lore(own, "memory", "add", "--body", "the web app keeps sessions for a day", "--yes").OK(t)
		m.Lore(cl, "memory", "add", "--body", "the shop bills monthly", "--yes").OK(t)
		q := "SELECT DISTINCT source FROM memories_all WHERE source <> 'repo'"
		for _, p := range places {
			out := m.Lore(p.dir, append([]string{"sql", "run", "--query", q, "--format", "csv"}, p.extra...)...).OK(t).Out()
			var labels []string
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || line == "source" || line == "me" {
					continue
				}
				labels = append(labels, line)
				if !wwlLabel.MatchString(line) {
					t.Errorf("devco:D-685 @%s: sql source %q is not <org>/<folder>:\n%s", p.name, line, out)
				}
			}
			if !slices.Contains(labels, p.label) {
				t.Errorf("devco:D-685 @%s: sql source does not name %s (got %v):\n%s", p.name, p.label, labels, out)
			}
		}
	})

	var webID string
	s.Step("D-689/one-folder-one-id-two-folders-two-ids", func() {
		m.Lore(m.Home, append([]string{"store", "folder", "add", "--store", wOrg, "--folder", wwlAPI}, setup...)...).OK(t)
		// the same folder through two attachments (the linked repo attaches
		// it under the bare org, home names it): one row, one id
		r := m.Lore(own, "policy", "set", "--key", wwlKey, "--value", "365", "--to", inWeb, "--yes").OK(t)
		if !strings.Contains(r.Out(), "("+inWeb+", pol_") {
			t.Errorf("devco:D-689 @own-linked: the result line does not name %s:\n%s", inWeb, r)
		}
		r = m.Lore(m.Home, append([]string{"policy", "set", "--key", wwlKey, "--value", "400", "--to", inWeb, "--yes"}, setup...)...).OK(t)
		if !strings.Contains(r.Out(), "("+inWeb+", pol_") {
			t.Errorf("devco:D-689 @home: the result line does not name %s:\n%s", inWeb, r)
		}
		r = m.Lore(m.Home, append([]string{"policy", "set", "--key", wwlKey, "--value", "365", "--to", inAPI, "--yes"}, setup...)...).OK(t)
		if !strings.Contains(r.Out(), "("+inAPI+", pol_") {
			t.Errorf("devco:D-689 @home: the result line does not name %s:\n%s", inAPI, r)
		}
		q := "SELECT source, id FROM policies_all WHERE key = '" + wwlKey + "'"
		out := m.Lore(m.Home, append([]string{"sql", "run", "--store", wOrg, "--query", q, "--format", "csv"}, setup...)...).OK(t).Out()
		rows := map[string]string{}
		for _, line := range strings.Split(out, "\n") {
			src, id, ok := strings.Cut(strings.TrimSpace(line), ",")
			if ok && wulPolID.MatchString(id) {
				if prev, dup := rows[src]; dup && prev != id {
					t.Errorf("devco:D-689: %s holds two rows of one key (%s, %s): the store folder and the linked repo derived different ids:\n%s", src, prev, id, out)
				}
				rows[src] = id
			}
		}
		webID = rows[inWeb]
		if webID == "" || rows[inAPI] == "" || webID == rows[inAPI] {
			t.Fatalf("devco:D-689: one key in %s and %s gave ids %v:\n%s", inWeb, inAPI, rows, out)
		}
		// every placement of the web folder reads the same id
		for _, p := range []place{places[0], places[1]} {
			o := m.Lore(p.dir, append([]string{"sql", "run", "--query", "SELECT id FROM policies_all WHERE key = '" + wwlKey + "' AND source = '" + inWeb + "'", "--format", "csv"}, p.extra...)...).OK(t).Out()
			if got := wulPolID.FindAllString(o, -1); len(got) != 1 || got[0] != webID {
				t.Errorf("devco:D-689 @%s: the %s policy reads as %v, home reads %s:\n%s", p.name, inWeb, got, webID, o)
			}
		}
	})

	s.Step("D-689/show-refuses-an-old-duplicate-naming-both-places", func() {
		root := w.places["store-root"].Dir
		webDir, apiDir := wFindDir(t, root, wFolder), wFindDir(t, root, wwlAPI)
		src := wwlFileWith(t, webDir, webID)
		rel, err := filepath.Rel(webDir, src)
		if err != nil {
			t.Fatal(err)
		}
		// an older lore derived the id without the folder: the same id in
		// the api folder (a second live row, not a copy)
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(apiDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		// only the value field: a blanket "400" replace also hit a random
		// id holding those digits (pol_…69400ead), and the row stopped being one
		dup := wwlValueRe.ReplaceAllString(string(b), "${1}30")
		if dup == string(b) {
			t.Fatalf("the policy row has no value 400 to change:\n%s", b)
		}
		if err := os.WriteFile(dst, []byte(dup), 0o644); err != nil {
			t.Fatal(err)
		}
		s.Git(root, "add", "-A")
		s.Git(root, "commit", "-q", "-m", "old duplicate id")
		for _, p := range []place{{"home", m.Home, setup, ""}, places[0], places[1]} {
			var extra []string
			if p.name == "home" {
				extra = setup
			}
			r := m.Lore(p.dir, append([]string{"show", "--id", webID}, extra...)...)
			t.Logf("%s: show --id %s:\n%s", p.name, webID, r)
			if p.name != "home" {
				// the store folder and the linked repo see the web folder
				// only (api is not attached there): one row answers
				if r.Code != 0 || !strings.Contains(r.Out(), "value: 400") {
					t.Errorf("devco:D-689 @%s: show --id of the web folder's row:\n%s", p.name, r)
				}
				// D-685: the header names the folder, as sql and the lists do
				// (in a linked repo the store is attached under the bare org)
				if !strings.Contains(r.Out(), "policies  "+inWeb+"\n") {
					t.Errorf("devco:D-685 @%s: show labels the row by another name than %s:\n%s", p.name, inWeb, r)
				}
				continue
			}
			if r.Code == 0 {
				// home with --setup reads every folder of the store: two live
				// rows of one id are refused, never one answered silently
				t.Errorf("devco:D-689 @home: show --id of an id live in %s and %s answered one row:\n%s", inWeb, inAPI, r)
				continue
			}
			if !strings.Contains(r.Out(), "E_AMBIGUOUS_ID") {
				t.Errorf("devco:D-689 @%s: show --id of an id in two folders did not refuse as E_AMBIGUOUS_ID:\n%s", p.name, r)
				continue
			}
			if !strings.Contains(r.Out(), inWeb) || !strings.Contains(r.Out(), inAPI) {
				t.Errorf("devco:D-689 @%s: the refusal does not name both %s and %s:\n%s", p.name, inWeb, inAPI, r)
			}
			reads := wwlShowRead.FindAllStringSubmatch(r.Out(), -1)
			if len(reads) < 2 {
				t.Errorf("devco:D-689 @%s: the refusal prints no read per place:\n%s", p.name, r)
			}
			for _, rd := range reads {
				args := []string{"show", "--id", rd[1], "--store", rd[2]}
				if p.name == "home" {
					args = append(args, setup...)
				}
				if got := m.Lore(p.dir, args...); got.Code != 0 {
					t.Errorf("devco:D-689 @%s: the printed `lore %s` does not work where printed:\n%s", p.name, strings.Join(args, " "), got)
				}
			}
		}
	})

	s.Step("D-690/every-offered-target-works-where-printed", func() {
		m.Lore(m.Home, "setup", "edit", "--name", wSetup, "--personal-store", "none", "--yes").OK(t)
		for _, p := range places {
			args := []string{"memory", "add", "--body", "a draft for me", "--to", "me", "--yes"}
			if p.name == "home" {
				args = append(args, setup...)
			}
			r := m.Lore(p.dir, args...)
			t.Logf("%s: --to me:\n%s", p.name, r)
			if r.Code == 0 {
				t.Errorf("devco:D-690 @%s: --to me with no personal store wrote:\n%s", p.name, r)
				continue
			}
			// every placement, the store clone folder included (it sits under
			// no setup's folders: the setup is the one of the store's org),
			// gets the setup's refusal, never "no store named me"
			if !strings.Contains(r.Out(), "E_SETUP_NO_PERSONAL_STORE") {
				t.Errorf("devco:D-690 @%s: --to me with no personal store is not the setup's refusal:\n%s", p.name, r)
			}
			tos := wwlTo.FindAllStringSubmatch(r.Out(), -1)
			for _, to := range tos {
				if to[1] == "me" {
					continue
				}
				a := []string{"memory", "add", "--body", "a draft for " + to[1], "--to", to[1], "--yes"}
				if p.name == "home" {
					a = append(a, setup...)
				}
				if got := m.Lore(p.dir, a...); got.Code != 0 {
					t.Errorf("devco:D-690 @%s: the offered --to %s does not work where printed:\n%s\n%s", p.name, to[1], r, got)
				}
			}
			if strings.Contains(r.Out(), "--to <org>/<folder>") {
				// the placeholder form: setup list must name the folders
				out := m.Lore(p.dir, "setup", "list").OK(t).Out()
				if !strings.Contains(out, wOrg) {
					t.Errorf("devco:D-690 @%s: the hint sends to `lore setup list`, which names no folder of %s:\n%s", p.name, wOrg, out)
				}
			} else if len(tos) == 0 {
				t.Errorf("devco:D-690 @%s: the refusal offers no --to:\n%s", p.name, r)
			}
		}
	})
}

// wwlFileWith is the one file under dir whose content holds s.
func wwlFileWith(t *testing.T, dir, s string) string {
	t.Helper()
	var hit string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || hit != "" {
			return err
		}
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), s) {
			hit = p
		}
		return nil
	})
	if err != nil || hit == "" {
		t.Fatalf("no file under %s holds %s (%v)", dir, s, err)
	}
	return hit
}
