package wire_test

// formatpin_sweep_test.go — S-5, the format-pin sweep (decisions D12–D15:
// a project's format is the rule, never the binary's). A checkout lore
// 0.1.17 wrote (every row file `"_v": 1`, a _meta.json with no format
// key) is committed on main and on a second branch; then EVERY operation
// of wire's real registry runs in it, once, with inputs built from its own
// field list (no hand list of commands). After each one the project must
// still be format 1: no file under .lore/ in the work tree or in any git
// ref (branches, live refs, anything) carries a newer `_v`, and the
// project format lore reads is still format 1. The one operation allowed
// to raise it is `lore migrate run`; it runs last and must raise it, so
// the check can tell format 1 from format 2. A new command joins the sweep
// the day it is registered.

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// fpsSkip are the operations the sweep does not run, each with why. The
// list stays short: an operation is skipped only when running it in a
// test would leave the sandbox (network, another process), never because
// it misbehaves.
var fpsSkip = map[string]string{
	opsmigrate.OpRun:     "the one operation allowed to raise the format: it runs last, on purpose",
	"upgrade.run":        "downloads a lore release from the network",
	"agent.start":        "starts an agent process",
	"support_bundle.run": "writes a bundle file into the process's working folder (the package folder under go test)",
}

// fpsNoSeed are core tables whose rows configure lore itself (policies,
// vars, metadata keys, leak rules): a sample row there would break every
// later command for reasons that have nothing to do with the format.
var fpsNoSeed = map[row.Table]bool{
	schema.TablePolicies: true, schema.TableVars: true, schema.TableMetaKeys: true,
	schema.TableMetaValues: true, schema.TableSecretRules: true, schema.TableSecretAllows: true,
}

// fpsSample is the text the sweep puts in every text input it fills.
const fpsSample = "fps sample"

// fpsTextFields are optional text inputs the sweep fills too, so the
// writes reach the store instead of stopping at a missing field.
var fpsTextFields = map[string]bool{
	"title": true, "body": true, "name": true, "summary": true, "text": true,
	"reason": true, "note": true, "description": true, "content": true,
}

// fpsSampleKind is a valid JSON literal of each field kind, for the rows
// the sweep seeds.
var fpsSampleKind = map[value.Kind]string{
	value.KindText: `"` + fpsSample + `"`, value.KindNumber: `1`, value.KindBool: `true`,
	value.KindDate: `"2026-01-02"`, value.KindDateTime: `"2026-01-02T03:04:05Z"`,
	value.KindDuration: `"1h"`, value.KindIP: `"10.0.0.1"`, value.KindCIDR: `"10.0.0.0/8"`,
	value.KindDNS: `"example.com"`, value.KindURL: `"https://example.com"`,
	value.KindEmail: `"a@example.com"`, value.KindJSON: `{}`,
	value.KindSecretRef: `"gopass://acme/sample"`,
}

// fpsNewV matches a row or metadata file of a format newer than 1.
var fpsNewV = regexp.MustCompile(`"_v":\s*([2-9]|[1-9][0-9])\b`)

// fpsV2 is a format-2 envelope: the row files the fixture rewrites.
var fpsV2 = regexp.MustCompile(`"_v":\s*2\b`)

// TestEveryCommandKeepsOldFormat is the S-5 sweep (see the file comment).
func TestEveryCommandKeepsOldFormat(t *testing.T) {
	// no t.Parallel: the sweep points HOME and git's global config at temp
	// folders, so a command that reads the person's home never sees the
	// real one
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// nothing leaves the machine: every proxy is a closed local port
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY"} {
		t.Setenv(k, "http://127.0.0.1:9")
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	h := opstest.New(t)
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	seeded := fpsSeed(t, h, reg)
	fpsToV017(t, h)
	h = h.ReopenCold() // a new process over the 0.1.17 files: nothing kept in memory
	fpsCheck(t, "the 0.1.17 checkout", h.Repo)

	ran, refused, failed := 0, 0, 0
	var unknown []string
	for _, e := range h.App.Registry().Entries() {
		name := e.Spec().Name
		if why, skip := fpsSkip[name]; skip {
			t.Logf("skip %s: %s", name, why)
			continue
		}
		in, ok := fpsInput(e, reg, h, seeded)
		if !ok {
			unknown = append(unknown, name)
		}
		_, rerr := h.RunYes(name, in)
		ran++
		switch {
		case rerr == nil:
		case errs.Has(rerr, errs.FormatOld):
			refused++
		default:
			failed++
		}
		fpsCheck(t, name+" "+in, h.Repo)
		if t.Failed() {
			t.Fatalf("stopped at the first command that raised the format: %s", name)
		}
	}
	t.Logf("ran %d operations: %d refused with E_FORMAT_OLD, %d failed otherwise; inputs guessed for %d", ran, refused, failed, len(unknown))
	if ran < 400 {
		t.Fatalf("the sweep ran %d operations; the registry has more", ran)
	}
	if refused < 50 {
		t.Fatalf("only %d writes were refused with E_FORMAT_OLD: the sweep's inputs no longer reach the store", refused)
	}

	// the control: migrate run does raise it, so the check sees format 2
	if _, err := h.RunYes(opsmigrate.OpRun, `{}`); err != nil {
		t.Fatalf("lore migrate run: %v", err)
	}
	if pf, err := wire.ProjectFormatAt(h.Repo); err != nil || pf.Format != rowfs.ProjectFormatCurrent {
		t.Fatalf("after lore migrate run the project is %+v (%v), want format 2", pf, err)
	}
	if len(fpsNewInTree(t, h.Repo)) == 0 {
		t.Fatal("after lore migrate run no file is on format 2: the sweep's check cannot see a raise")
	}
}

// fpsSeed writes one row of every core table (the tables migrate rewrites)
// into the repo store, so the commands have rows to edit, delete, move and
// show; it returns the ids by table. Tables whose required refs cannot be
// filled are left out (logged).
func fpsSeed(t *testing.T, h *opstest.H, reg *schema.Registry) map[row.Table]string {
	t.Helper()
	ids := map[row.Table]string{schema.TableProjects: h.Project}
	var pending []row.Table
	for _, table := range wire.CoreTables() {
		if !fpsNoSeed[table] {
			pending = append(pending, table)
		}
	}
	for pass := 0; pass < 4 && len(pending) > 0; pass++ {
		var next []row.Table
		for _, table := range pending {
			def, ok := reg.Table(table)
			if !ok {
				continue
			}
			fields, ready := fpsRowFields(t, h, def, ids)
			if !ready {
				next = append(next, table)
				continue
			}
			w, err := h.App.Create(context.Background(), store.SourceRepo, table, fields)
			if err != nil {
				t.Logf("seed %s: %v", table, err)
				continue
			}
			ids[table] = w.Row.ID.String()
		}
		pending = next
	}
	if len(pending) > 0 {
		t.Logf("not seeded (unfilled refs): %v", pending)
	}
	if len(ids) < 20 {
		t.Fatalf("seeded %d tables; the sweep needs rows to work on", len(ids))
	}
	return ids
}

// fpsRowFields fills def's required fields; ready is false while a ref
// names a table with no seeded row yet.
func fpsRowFields(t *testing.T, h *opstest.H, def schema.TableDef, ids map[row.Table]string) (map[string]value.Value, bool) {
	t.Helper()
	fields := map[string]value.Value{}
	for _, f := range def.Fields {
		if !f.Required && !fpsTextFields[f.Name] && f.Name != "project_id" {
			continue
		}
		var raw string
		switch {
		case len(f.Values) > 0:
			raw = opstest.Quote(f.Values[0])
		case f.Kind == value.KindRef && f.RefTable == schema.TableActors:
			raw = opstest.Quote(opstest.Actor)
		case f.Kind == value.KindRef && f.RefTable != "":
			id, ok := ids[f.RefTable]
			if !ok {
				if f.Required {
					return nil, false
				}
				continue
			}
			raw = opstest.Quote(id)
		case f.Kind == value.KindRef:
			if f.Required {
				return nil, false // a polymorphic ref: no generic value
			}
			continue
		case f.Kind == value.KindList:
			raw = `[]`
		default:
			s, ok := fpsSampleKind[f.Kind]
			if !ok {
				if f.Required {
					return nil, false
				}
				continue
			}
			raw = s
		}
		var v value.Value
		var err error
		if f.Kind == value.KindList {
			v, err = value.ParseList(f.Elem, []byte(raw))
		} else {
			v, err = value.Parse(f.Kind, []byte(raw))
		}
		if err != nil {
			if f.Required {
				return nil, false
			}
			continue
		}
		fields[f.Name] = v
	}
	return fields, true
}

// fpsToV017 turns the checkout into what lore 0.1.17 left: every row file
// under .lore/data at `"_v": 1`, a _meta.json without a format key; then
// commits it on main and branches `feature` from it (the "every branch"
// of the rule).
func fpsToV017(t *testing.T, h *opstest.H) {
	t.Helper()
	data := filepath.Join(h.Repo, rowfs.LoreDir, rowfs.DataDir)
	n := 0
	err := filepath.WalkDir(data, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() || filepath.Ext(p) != ".json" {
			return werr
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if filepath.Base(p) == rowfs.MetaFileName || !fpsV2.Match(b) {
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
	switch {
	case os.IsNotExist(err):
		b = []byte(`{"_v": 1, "project_id": ` + opstest.Quote(h.Project) + `}` + "\n")
	case err != nil:
		t.Fatal(err)
	default:
		var o map[string]json.RawMessage
		if jerr := json.Unmarshal(b, &o); jerr != nil {
			t.Fatal(jerr)
		}
		for _, k := range []string{rowfs.MetaKeyFormat, rowfs.MetaKeyMigratedBy, rowfs.MetaKeyMigratedAt, "min_lore_version", "features"} {
			delete(o, k)
		}
		o["_v"] = json.RawMessage(`1`)
		if b, err = json.MarshalIndent(o, "", "  "); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(meta, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if pf, err := wire.ProjectFormatAt(h.Repo); err != nil || pf.Format != rowfs.ProjectFormatV017 {
		t.Fatalf("the seeded checkout reads as %+v (%v), want the 0.1.17 format", pf, err)
	}
	fpsGit(t, h.Repo, "add", "-A")
	fpsGit(t, h.Repo, "-c", "user.name=Fps", "-c", "user.email=fps@example.test", "commit", "-q", "--no-verify", "-m", "lore 0.1.17 data")
	fpsGit(t, h.Repo, "branch", "feature")
}

// fpsInput builds e's JSON input from its own field list: every required
// field, plus the text fields that make a write reach the store. An id is
// a seeded row of the operation's noun (or of the table a `<x>_id` field
// names), else the project. ok is false when a required field got a
// guess the sweep has no better value for (logged in the summary only).
func fpsInput(e op.Entry, reg *schema.Registry, h *opstest.H, seeded map[row.Table]string) (string, bool) {
	name := e.Spec().Name
	noun := name
	if i := strings.Index(name, "."); i > 0 {
		noun = name[:i]
	}
	idOf := func(n string) string {
		if table, ok := tableOfNoun(reg, n); ok {
			if id, ok := seeded[table]; ok {
				return id
			}
		}
		return h.Project
	}
	in := map[string]json.RawMessage{}
	ok := true
	for _, f := range e.InputFields() {
		if !f.Required && !fpsTextFields[f.Name] {
			continue
		}
		var raw string
		switch {
		case f.Name == "id" || f.Name == "target" || f.Name == "row" || f.Name == "row_id":
			raw = opstest.Quote(idOf(noun))
		case f.Name == "ids":
			raw = `[` + opstest.Quote(idOf(noun)) + `]`
		case strings.HasSuffix(f.Name, "_id"):
			raw = opstest.Quote(idOf(strings.TrimSuffix(f.Name, "_id")))
		case f.Type == op.TypeText:
			raw = opstest.Quote(idOf(noun))
		case f.Type == op.TypeString:
			raw = opstest.Quote(fpsSample)
		case f.Type == op.TypeStrings:
			raw = `[` + opstest.Quote(fpsSample) + `]`
		case f.Type == op.TypeInt || f.Type == op.TypeUint || f.Type == op.TypeFloat:
			raw = `1`
		case f.Type == op.TypeBool:
			raw = `false`
		case f.Type == op.TypeDuration:
			raw = `"1h"`
		case f.Type == op.TypeStringMap:
			raw = `{}`
		default:
			ok = false
			continue
		}
		in[f.Name] = json.RawMessage(raw)
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(opstest.Quote(k) + ":" + string(in[k]))
	}
	b.WriteString("}")
	return b.String(), ok
}

// fpsCheck fails when the checkout at repo is no longer on format 1: its
// format reads newer, or a file under .lore/ in the work tree or in any
// ref carries a newer `_v`.
func fpsCheck(t *testing.T, when, repo string) {
	t.Helper()
	if pf, err := wire.ProjectFormatAt(repo); err != nil || pf.Format != rowfs.ProjectFormatV017 {
		t.Errorf("%s: the project reads as %+v (%v), want the 0.1.17 format", when, pf, err)
	}
	if hits := fpsNewInTree(t, repo); len(hits) > 0 {
		t.Errorf("%s: the work tree holds format-2 files: %v", when, hits)
	}
	for _, ref := range strings.Split(fpsGit(t, repo, "for-each-ref", "--format=%(refname)"), "\n") {
		if ref == "" {
			continue
		}
		cmd := exec.Command("git", "grep", "-l", "-E", `"_v":[[:space:]]*([2-9]|[1-9][0-9])([^0-9]|$)`, ref, "--", ".lore")
		cmd.Dir = repo
		cmd.Env = gitenv.Clean(os.Environ())
		out, _ := cmd.Output() // exit 1: nothing matched, the good case
		if s := strings.TrimSpace(string(out)); s != "" {
			t.Errorf("%s: ref %s holds format-2 files: %s", when, ref, s)
		}
	}
}

// fpsNewInTree lists the files under repo/.lore whose content carries a
// newer `_v` (committed or not).
func fpsNewInTree(t *testing.T, repo string) []string {
	t.Helper()
	var hits []string
	_ = filepath.WalkDir(filepath.Join(repo, rowfs.LoreDir), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".json" {
			return nil
		}
		if b, rerr := os.ReadFile(p); rerr == nil && fpsNewV.Match(b) {
			rel, _ := filepath.Rel(repo, p)
			hits = append(hits, rel)
		}
		return nil
	})
	return hits
}

// fpsGit runs git in dir and returns its stdout.
func fpsGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitenv.Clean(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}
