package app

// formatpin_sweep_test.go — S-5 through the real command line (decisions
// D12–D15: a project's format is the rule, never the binary's). A repo
// lore 0.1.17 left (every row file `"_v": 1`, a _meta.json without a
// format key) is pushed to a bare remote on main and on a second branch.
// Then EVERY leaf command of the lore command tree runs in its clone once
// (the generated ones with inputs built from their operation's field list,
// the host's own with no arguments), through the whole host: live rows and
// their landing, render, the hook commands, sync. After each one no file
// under .lore/ in the work tree, in any ref of the clone or in any ref of
// the remote carries a newer `_v`, and the project still reads as format
// 1. `lore migrate run` is the one command allowed to raise it: it runs
// last and must raise it, so the check can tell the two formats apart.
// wire's TestEveryCommandKeepsOldFormat is the same sweep over the App
// alone; this one adds everything the host wraps around it.

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/surface/cli"
	"github.com/thesatellite-ai/lore/wire"
)

// fpcSkip are the leaf commands the sweep does not run, each with why:
// only commands that would leave the sandbox, wait for a person or serve
// forever. Never one because it misbehaves.
var fpcSkip = map[string]string{
	"migrate run":        "the one command allowed to raise the format: it runs last, on purpose",
	"upgrade":            "downloads a lore release from the network",
	"upgrade run":        "downloads a lore release from the network",
	"agent start":        "starts an agent process",
	"api":                "serves the local API until stopped",
	"tui":                "a full-screen terminal UI waiting for keys",
	"setup web":          "serves the setup page until stopped",
	"onboard":            "serves the setup page until stopped",
	"help":               "prints help",
	"completion":         "prints a shell completion script",
	"version":            "prints the version",
	"support-bundle run": "writes a bundle file into the working folder",
}

// fpcSample is the text the sweep puts in every text input it fills.
const fpcSample = "fpc sample"

// fpcTextFields are optional text inputs the sweep fills too, so a write
// reaches the store instead of stopping at a missing field.
var fpcTextFields = map[string]bool{
	"title": true, "body": true, "name": true, "summary": true, "text": true,
	"reason": true, "note": true, "description": true, "content": true,
}

// fpcV2 is a format-2 envelope: the row files the fixture rewrites.
var fpcV2 = regexp.MustCompile(`"_v":\s*2\b`)

// fpcNewV is any envelope newer than format 1.
var fpcNewV = regexp.MustCompile(`"_v":\s*([2-9]|[1-9][0-9])\b`)

// fpcNoSeed are the add commands whose rows configure lore itself: a
// sample row there would break later commands for reasons unrelated to
// the format.
var fpcNoSeed = map[string]bool{
	"policy": true, "vars": true, "meta": true, "secrets": true, "pii_pattern": true,
	"external_source": true, "contact_type": true, "role": true, "team": true, "member": true,
	"actor": true, "repo": true, "store": true, "resource": true,
}

// fpcShards is how many worlds the sweep splits the commands over: each
// builds its own 0.1.17 fixture and runs every fpcShards-th command, in
// parallel (one world ran ~530 commands in about 200 s).
const fpcShards = 8

// TestEveryCLICommandKeepsOldFormat is the S-5 sweep through the binary.
func TestEveryCLICommandKeepsOldFormat(t *testing.T) {
	t.Parallel()
	reg := fpcRegistry(t)
	byPath := map[string]op.Entry{}
	for _, e := range reg.Entries() {
		byPath[strings.Join(op.CLIPath(e.Spec().Name), " ")] = e
	}
	var leaves []*cobra.Command
	fpcLeaves(newDriftRoot(t), &leaves)
	var paths []string
	for _, c := range leaves {
		path := strings.TrimSpace(strings.TrimPrefix(c.CommandPath(), cli.RootName))
		if why, skip := fpcSkip[path]; skip {
			t.Logf("skip %s: %s", path, why)
			continue
		}
		if c.Flags().Lookup("yes") != nil {
			path += " --yes"
		}
		paths = append(paths, path)
	}
	if len(paths) < 400 {
		t.Fatalf("the sweep has %d commands; the tree has more", len(paths))
	}
	var refused atomic.Int32
	t.Run("shards", func(t *testing.T) { // the group waits for its parallel shards
		for shard := range fpcShards {
			t.Run("shard"+strconv.Itoa(shard), func(t *testing.T) {
				t.Parallel()
				w, repo, remote, seeded := fpcWorld(t, reg)
				seen := map[string]string{}
				fpcCheck(t, w, seen, "the 0.1.17 repo", repo, remote)
				for n, path := range paths {
					if n%fpcShards != shard {
						continue
					}
					args := strings.Fields(path)
					if e, ok := byPath[strings.TrimSuffix(path, " --yes")]; ok {
						args = append(args, fpcArgs(e, seeded)...)
					}
					code, out, errOut := w.loreRun(t, repo, nil, "", time.Minute, args...)
					if code == fpcFormatOldExit {
						refused.Add(1)
					}
					fpcCheck(t, w, seen, "lore "+strings.Join(args, " ")+" (exit "+strconv.Itoa(code)+": "+firstLine(errOut+out)+")", repo, remote)
					if t.Failed() {
						t.Fatalf("stopped at the first command that raised the format: lore %s", path)
					}
				}
				if shard != 0 {
					return
				}
				// the control: migrate run does raise it, so the check sees format 2
				w.mustLore(t, repo, "migrate", "run")
				if pf, err := wire.ProjectFormatAt(repo); err != nil || pf.Format != rowfs.ProjectFormatCurrent {
					t.Fatalf("after lore migrate run the project is %+v (%v), want format 2", pf, err)
				}
				if len(fpcNewInTree(repo)) == 0 {
					t.Fatal("after lore migrate run no file is on format 2: the sweep's check cannot see a raise")
				}
			})
		}
	})
	t.Logf("ran %d commands: %d refused with E_FORMAT_OLD", len(paths), refused.Load())
	if !t.Failed() && refused.Load() < 40 {
		t.Fatalf("only %d commands were refused with E_FORMAT_OLD: the sweep's inputs no longer reach the store", refused.Load())
	}
}

// fpcWorld is one shard's world: a pushed lore repo with a row of most
// nouns, turned into what lore 0.1.17 left (fpcToV017). It returns the
// world, the clone, its bare remote and the seeded ids by table.
func fpcWorld(t *testing.T, reg *op.Registry) (*cliWorld, string, string, map[string]string) {
	t.Helper()
	w := newCLIWorld(t).person().author()
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY"} {
		w.set(k, "http://127.0.0.1:9") // nothing leaves the machine
	}
	w.setAll(map[string]string{"NO_PROXY": "", "no_proxy": "", "EDITOR": "true", "VISUAL": "true", "BROWSER": "true", "GIT_EDITOR": "true"})
	w.set("XDG_CONFIG_HOME", filepath.Join(w.env["HOME"], ".config"))
	repo := w.pushedLore(t, "a")
	seeded := fpcSeed(t, w, repo, reg)
	fpcToV017(t, w, repo)
	return w, repo, w.path("remote.git"), seeded
}

// fpcFormatOldExit is the exit status of E_FORMAT_OLD.
const fpcFormatOldExit = 24

// fpcRegistry is wire's operation registry (an App over scratch folders).
func fpcRegistry(t *testing.T) *op.Registry {
	t.Helper()
	a, err := wire.New(wire.Options{RepoDir: t.TempDir(), Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a.Registry()
}

// fpcLeaves collects every runnable leaf under c.
func fpcLeaves(c *cobra.Command, out *[]*cobra.Command) {
	subs := c.Commands()
	if len(subs) == 0 {
		if c.Runnable() {
			*out = append(*out, c)
		}
		return
	}
	for _, s := range subs {
		fpcLeaves(s, out)
	}
}

// fpcTable maps a noun ("memory", "task_list") to its built-in table.
func fpcTable(noun string) string {
	flat := strings.ReplaceAll(strings.ReplaceAll(noun, "-", "_"), "_", "")
	for _, d := range schema.Builtin() {
		name := strings.ReplaceAll(string(d.Name), "_", "")
		for _, cand := range []string{flat + "s", flat + "es", strings.TrimSuffix(flat, "y") + "ies"} {
			if name == cand {
				return string(d.Name)
			}
		}
	}
	return ""
}

// fpcSeed runs every `<noun> add` of the registry once (except the
// configuring nouns) so later commands have rows to edit, move, delete and
// show; it returns the new ids by table, the project's included.
func fpcSeed(t *testing.T, w *cliWorld, repo string, reg *op.Registry) map[string]string {
	t.Helper()
	seeded := map[string]string{}
	var meta struct {
		ProjectID string `json:"project_id"`
	}
	if b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rowfs.RepoMetaPath))); err == nil && json.Unmarshal(b, &meta) == nil && meta.ProjectID != "" {
		seeded[string(schema.TableProjects)] = meta.ProjectID
	}
	for _, e := range reg.Entries() {
		name := e.Spec().Name
		noun, ok := strings.CutSuffix(name, ".add")
		if !ok || strings.Contains(noun, ".") || fpcNoSeed[noun] {
			continue
		}
		table := fpcTable(noun)
		if table == "" {
			continue
		}
		args := append(op.CLIPath(name), fpcArgs(e, seeded)...)
		code, out, _ := w.lore(t, repo, append(args, "--json")...)
		if code != 0 {
			continue
		}
		var res struct {
			ID   string `json:"id"`
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(out), &res) == nil {
			if res.ID == "" {
				res.ID = res.Data.ID
			}
			if res.ID != "" {
				seeded[table] = res.ID
			}
		}
	}
	if len(seeded) < 15 {
		t.Fatalf("seeded %d tables (%v); the sweep needs rows to work on", len(seeded), seeded)
	}
	return seeded
}

// fpcArgs builds the command line of e from its own field list: every
// required field, plus the text fields that make a write reach the store.
// An id is a seeded row of the operation's noun (or of the table a
// `<x>_id` field names), else the project.
func fpcArgs(e op.Entry, seeded map[string]string) []string {
	name := e.Spec().Name
	noun := name
	if i := strings.Index(name, "."); i > 0 {
		noun = name[:i]
	}
	idOf := func(n string) string {
		if id, ok := seeded[fpcTable(n)]; ok {
			return id
		}
		return seeded[string(schema.TableProjects)]
	}
	var pos []op.FieldInfo
	var flags []string
	for _, f := range e.InputFields() {
		if !f.Required && !fpcTextFields[f.Name] {
			continue
		}
		var v []string
		switch {
		case f.Name == "id" || f.Name == "target" || f.Name == "row" || f.Name == "row_id" || f.Name == "ids":
			v = []string{idOf(noun)}
		case strings.HasSuffix(f.Name, "_id"):
			v = []string{idOf(strings.TrimSuffix(f.Name, "_id"))}
		case f.Type == op.TypeText:
			v = []string{idOf(noun)}
		case f.Type == op.TypeString || f.Type == op.TypeStrings:
			v = []string{fpcSample}
		case f.Type == op.TypeInt || f.Type == op.TypeUint || f.Type == op.TypeFloat:
			v = []string{"1"}
		case f.Type == op.TypeDuration:
			v = []string{"1h"}
		case f.Type == op.TypeBool:
			continue
		default:
			continue
		}
		if f.Flag == "" && f.Arg != op.NoArg {
			if f.Required { // an optional positional often aliases a flag filled below
				pos = append(pos, f)
			}
			continue
		}
		if f.Flag != "" {
			for _, x := range v {
				flags = append(flags, "--"+f.Flag+"="+x)
			}
		}
	}
	sort.Slice(pos, func(i, j int) bool { return pos[i].Arg < pos[j].Arg })
	var args []string
	for _, f := range pos {
		switch {
		case f.Name == "id" || f.Name == "target" || f.Name == "row" || f.Name == "row_id" || f.Name == "ids":
			args = append(args, idOf(noun))
		case strings.HasSuffix(f.Name, "_id"):
			args = append(args, idOf(strings.TrimSuffix(f.Name, "_id")))
		case f.Type == op.TypeText:
			args = append(args, idOf(noun))
		default:
			args = append(args, fpcSample)
		}
	}
	return append(args, flags...)
}

// fpcToV017 turns the clone into what lore 0.1.17 left: every row file
// under .lore/data at `"_v": 1`, a _meta.json without a format key; then
// commits it, pushes main and a second branch `feature`.
func fpcToV017(t *testing.T, w *cliWorld, repo string) {
	t.Helper()
	data := filepath.Join(repo, rowfs.LoreDir, rowfs.DataDir)
	n := 0
	err := filepath.WalkDir(data, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() || filepath.Ext(p) != ".json" {
			return werr
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if filepath.Base(p) == rowfs.MetaFileName || !fpcV2.Match(b) {
			return nil
		}
		// what lore 0.1.17 wrote: no HLC, no origin block, `"_v": 1`
		var o map[string]json.RawMessage
		if jerr := json.Unmarshal(b, &o); jerr != nil {
			return jerr
		}
		delete(o, "_hlc")
		delete(o, "_o")
		o["_v"] = json.RawMessage(`1`)
		nb, merr := json.MarshalIndent(o, "", "  ")
		if merr != nil {
			return merr
		}
		n++
		return os.WriteFile(p, append(nb, '\n'), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 10 {
		t.Fatalf("rewrote %d row files to _v 1; the seed wrote too few", n)
	}
	meta := filepath.Join(data, rowfs.MetaFileName)
	b, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	var o map[string]json.RawMessage
	if err := json.Unmarshal(b, &o); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{rowfs.MetaKeyFormat, rowfs.MetaKeyMigratedBy, rowfs.MetaKeyMigratedAt, "min_lore_version", "features"} {
		delete(o, k)
	}
	o["_v"] = json.RawMessage(`1`)
	if b, err = json.MarshalIndent(o, "", "  "); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meta, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if pf, err := wire.ProjectFormatAt(repo); err != nil || pf.Format != rowfs.ProjectFormatV017 {
		t.Fatalf("the fixture reads as %+v (%v), want the 0.1.17 format", pf, err)
	}
	w.git(t, repo, "add", "-A")
	w.git(t, repo, "commit", "-q", "-m", "lore 0.1.17 data")
	w.git(t, repo, "push", "-q", "origin", "HEAD:main")
	w.git(t, repo, "branch", "feature")
	w.git(t, repo, "push", "-q", "origin", "feature")
}

// fpcCheck fails when the clone or the remote is no longer on format 1.
// Refs are scanned when they point somewhere new since the last check
// (seen holds "<dir> <ref>" → object): a ref that did not move holds what
// was already checked.
func fpcCheck(t *testing.T, w *cliWorld, seen map[string]string, when, repo, remote string) {
	t.Helper()
	if pf, err := wire.ProjectFormatAt(repo); err != nil || pf.Format != rowfs.ProjectFormatV017 {
		t.Errorf("%s: the project reads as %+v (%v), want the 0.1.17 format", when, pf, err)
	}
	if hits := fpcNewInTree(repo); len(hits) > 0 {
		t.Errorf("%s: the work tree holds format-2 files: %v", when, hits)
	}
	for _, dir := range []string{repo, remote} {
		for _, line := range strings.Split(strings.TrimSpace(w.git(t, dir, "for-each-ref", "--format=%(objectname) %(refname)")), "\n") {
			obj, ref, ok := strings.Cut(line, " ")
			if !ok || seen[dir+" "+ref] == obj {
				continue
			}
			seen[dir+" "+ref] = obj
			cmd := exec.Command(w.gitBin, "-C", dir, "grep", "-l", "-E", `"_v":[[:space:]]*([2-9]|[1-9][0-9])([^0-9]|$)`, ref, "--", ".lore")
			cmd.Env = w.environ(nil)
			out, _ := cmd.Output() // exit 1: nothing matched, the good case
			if s := strings.TrimSpace(string(out)); s != "" {
				t.Errorf("%s: ref %s of %s holds format-2 files: %s", when, ref, filepath.Base(dir), s)
			}
		}
	}
}

// fpcNewInTree lists the files under repo/.lore whose content carries a
// newer `_v`.
func fpcNewInTree(repo string) []string {
	var hits []string
	_ = filepath.WalkDir(filepath.Join(repo, rowfs.LoreDir), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".json" {
			return nil
		}
		if b, rerr := os.ReadFile(p); rerr == nil && fpcNewV.Match(b) {
			rel, _ := filepath.Rel(repo, p)
			hits = append(hits, rel)
		}
		return nil
	})
	return hits
}

// firstLine is s's first non-empty line.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
