// Package opstest is the test harness of the ops packages: a real wire.App
// (real backends, cache, engine, validation, policy) over a fresh git repo
// and lore home in temp folders, driven by operation name with a JSON
// input exactly like the REST surface and `--json` do.
//
// Why a real App and not a fake Env: the ops packages' behaviour that
// matters to a user (natural ids, required fields, closed sets, store
// routing, tombstones, Text() output) is decided by the engine and the
// schema as much as by the op code, so a test through the real stack pins
// what a user sees. Packages with a narrow Env seam still use their own
// fakes for failure injection.
//
// Only _test.go files may import this package (internal/layers flags any
// production import of internal/...); it imports wire, the composition
// root, which is why it cannot live under ops/.
package opstest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// Actor is the fixed actor every harness write is stamped with (the 32
// hex digits are arbitrary).
const Actor = "act_0e45506e64357c94a87e70ef1c6bbba9"

// ProjectName is the name of the project row New seeds (what `lore init`
// writes for the repo).
const ProjectName = "demo"

// H is one harness: an App over Repo (a git work tree) and Home.
type H struct {
	T    testing.TB
	App  *wire.App
	Repo string
	Home string
	// Project is the seeded project row's id.
	Project string
}

// New builds a harness with a seeded project row. Each mut may adjust the
// wire options before the App is built (hooks such as RepoIsClient,
// DefaultTarget, OrgStores, a fixed Wall clock).
func New(t testing.TB, mut ...func(*wire.Options)) *H {
	t.Helper()
	h := Bare(t, mut...)
	h.SeedProject()
	return h
}

// WithStores builds a harness whose repo has the named stores attached
// (each `lore store init <name>` over a fresh folder store; "me" is the
// personal store at its default place under Home), then seeds the
// project. Stores must be initialised before the App opens its engine,
// which attaches the stores of .lore/stores.yaml once.
func WithStores(t testing.TB, names []string, mut ...func(*wire.Options)) *H {
	t.Helper()
	h := Bare(t, mut...)
	for _, n := range names {
		in := `{"name":` + Quote(n) + `}`
		if n != personalStore {
			in = `{"name":` + Quote(n) + `,"url":` + Quote("file://"+filepath.ToSlash(t.TempDir())) + `}`
		}
		h.MustYes("store.init", in)
	}
	h.SeedProject()
	return h
}

// personalStore is the personal store's reserved name (ops/stores
// PersonalName): its url defaults under the lore home.
const personalStore = "me"

// Bare builds a harness without a project row (for the "no lore project"
// paths).
func Bare(t testing.TB, mut ...func(*wire.Options)) *H {
	t.Helper()
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	actor, err := ids.Parse[ids.Actor](Actor)
	if err != nil {
		t.Fatal(err)
	}
	opts := wire.Options{RepoDir: repo, Home: t.TempDir(), Actor: actor}
	for _, m := range mut {
		m(&opts)
	}
	a, err := wire.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return &H{T: t, App: a, Repo: repo, Home: opts.Home}
}

// Fixed returns an option setting the wall clock to a fixed instant, so
// stamped dates in Text() output are stable.
func Fixed(at time.Time) func(*wire.Options) {
	return func(o *wire.Options) { o.Wall = func() time.Time { return at } }
}

// SeedProject writes the repo's project row (name ProjectName) through
// the engine, as `lore init` does, and records its id.
func (h *H) SeedProject() {
	h.T.Helper()
	ctx := context.Background()
	name, err := value.TextOf(ProjectName)
	if err != nil {
		h.T.Fatal(err)
	}
	w, err := h.App.Create(ctx, store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": name})
	if err != nil {
		h.T.Fatal(err)
	}
	h.Project = w.Row.ID.String()
}

// Run runs the named operation with a JSON input (an empty string is {}),
// as a non-interactive caller that did not confirm.
func (h *H) Run(name, input string) (op.Result, error) {
	h.T.Helper()
	return h.run(name, input, false)
}

// RunYes is Run with the caller's confirmation (--yes).
func (h *H) RunYes(name, input string) (op.Result, error) {
	h.T.Helper()
	return h.run(name, input, true)
}

func (h *H) run(name, input string, yes bool) (op.Result, error) {
	h.T.Helper()
	e, ok := h.App.Registry().Lookup(name)
	if !ok {
		h.T.Fatalf("no op %s", name)
	}
	actor, err := ids.Parse[ids.Actor](Actor)
	if err != nil {
		h.T.Fatal(err)
	}
	return h.App.Run(context.Background(), e, op.JSONDecoder{Body: []byte(input)}, op.Caller{Actor: actor, Yes: yes})
}

// Must runs the operation and fails the test on an error.
func (h *H) Must(name, input string) op.Result {
	h.T.Helper()
	res, err := h.Run(name, input)
	if err != nil {
		h.T.Fatalf("%s %s: %v", name, input, err)
	}
	return res
}

// MustYes is Must with --yes.
func (h *H) MustYes(name, input string) op.Result {
	h.T.Helper()
	res, err := h.RunYes(name, input)
	if err != nil {
		h.T.Fatalf("%s %s: %v", name, input, err)
	}
	return res
}

// Fails runs the operation, fails the test unless it errs with code, and
// returns the error message.
func (h *H) Fails(name, input string, code errs.Code) string {
	h.T.Helper()
	_, err := h.Run(name, input)
	if err == nil {
		h.T.Fatalf("%s %s: no error, want %s", name, input, code)
		return ""
	}
	if !errs.Has(err, code) {
		h.T.Fatalf("%s %s: err %v, want %s", name, input, err, code)
	}
	return err.Error()
}

// JSON is the result's JSON (REST body / --json).
func (h *H) JSON(res op.Result) string {
	h.T.Helper()
	b, err := res.JSON()
	if err != nil {
		h.T.Fatal(err)
	}
	return string(b)
}

// Text is the result's human form: Text() when the output is an
// op.Texter, else "" (the CLI then prints a generic dump).
func Text(res op.Result) string {
	v := res.Value()
	if !v.IsValid() {
		return ""
	}
	if t, ok := v.Interface().(op.Texter); ok {
		return t.Text()
	}
	return ""
}

// Field reads a top-level string field of the result's JSON ("" when
// absent or not a string).
func (h *H) Field(res op.Result, key string) string {
	h.T.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(h.JSON(res)), &m); err != nil {
		h.T.Fatal(err)
	}
	var s string
	if raw, ok := m[key]; ok {
		if err := json.Unmarshal(raw, &s); err != nil {
			return ""
		}
	}
	return s
}

// ID is the id of a write output: its top-level "id", else data.id (the
// show envelope some adds print). An output with neither fails the test,
// so a test never goes on with an empty id that silently matches nothing.
func (h *H) ID(res op.Result) string {
	h.T.Helper()
	if id := h.Field(res, "id"); id != "" {
		return id
	}
	var env struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(res)), &env); err != nil || env.Data.ID == "" {
		h.T.Fatalf("no id in %s", h.JSON(res))
	}
	return env.Data.ID
}

// Quote JSON-encodes s, for building inputs by hand.
func Quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return `""`
	}
	return strings.TrimSpace(b.String())
}

// IDPattern replaces every lore id (<3 letters>_<32 hex>) in s by
// "<prefix>_ID", so Text() lines can be compared with v0.1.17's output
// whatever ids the two runs drew.
func IDPattern(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if i+36 <= len(s) && isLower(s[i]) && isLower(s[i+1]) && isLower(s[i+2]) && s[i+3] == '_' && allHex(s[i+4:i+36]) && (i == 0 || !isWord(s[i-1])) && (i+36 == len(s) || !isWord(s[i+36])) {
			out.WriteString(s[i:i+3] + "_ID")
			i += 36
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func isLower(c byte) bool { return c >= 'a' && c <= 'z' }

func isWord(c byte) bool {
	return isLower(c) || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

func allHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// sampleJSON is a valid JSON literal of each field kind, for Seed's
// required fields.
var sampleJSON = map[value.Kind]string{
	value.KindText: `"sample"`, value.KindNumber: `1`, value.KindBool: `true`,
	value.KindDate: `"2026-01-02"`, value.KindDateTime: `"2026-01-02T03:04:05Z"`,
	value.KindDuration: `"1h"`, value.KindIP: `"10.0.0.1"`, value.KindCIDR: `"10.0.0.0/8"`,
	value.KindDNS: `"example.com"`, value.KindURL: `"https://example.com"`,
	value.KindEmail: `"a@example.com"`, value.KindJSON: `{}`,
	value.KindSecretRef: `"gopass://acme/sample"`,
}

// Seed writes a row of table into the repo store through the engine and
// returns its id. Every required field the caller does not set (set maps
// a field to a JSON literal) gets a sample value: a closed set's first
// value, the seeded project for a ref to projects, the harness actor for a
// ref to actors, and a freshly seeded row for any other ref. It is the
// generic row factory for tests that must cover every table of a kind
// (for example every table that carries tags).
func (h *H) Seed(table row.Table, set map[string]string) string {
	h.T.Helper()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		h.T.Fatal(err)
	}
	def, ok := reg.Table(table)
	if !ok {
		h.T.Fatalf("seed: no table %s", table)
	}
	fields := map[string]value.Value{}
	for _, f := range def.Fields {
		raw, given := set[f.Name]
		if !given && !f.Required {
			continue
		}
		if !given {
			raw = h.sample(f)
		}
		var v value.Value
		if f.Kind == value.KindList {
			v, err = value.ParseList(f.Elem, []byte(raw))
		} else {
			v, err = value.Parse(f.Kind, []byte(raw))
		}
		if err != nil {
			h.T.Fatalf("seed %s.%s = %s: %v", table, f.Name, raw, err)
		}
		fields[f.Name] = v
	}
	w, err := h.App.Create(context.Background(), store.SourceRepo, table, fields)
	if err != nil {
		h.T.Fatalf("seed %s: %v", table, err)
	}
	return w.Row.ID.String()
}

// sample is the JSON literal Seed writes into a required field f.
func (h *H) sample(f schema.FieldDef) string {
	h.T.Helper()
	switch {
	case len(f.Values) > 0:
		return Quote(f.Values[0])
	case f.Kind == value.KindRef && f.RefTable == schema.TableProjects:
		return Quote(h.Project)
	case f.Kind == value.KindRef && f.RefTable == schema.TableActors:
		return Quote(Actor)
	case f.Kind == value.KindRef && f.RefTable != "":
		return Quote(h.Seed(f.RefTable, nil))
	case f.Kind == value.KindList:
		return `[]`
	}
	raw, ok := sampleJSON[f.Kind]
	if !ok {
		h.T.Fatalf("seed: no sample for field %s (kind %s)", f.Name, f.Kind)
	}
	return raw
}

// SeedProjectID writes the repo's project row under a given id (a
// v0.1.17 project id, so natural ids derived from it can be compared with
// files v0.1.17 wrote) and records it. Use it on a Bare harness.
func (h *H) SeedProjectID(id string) {
	h.T.Helper()
	pid, err := ids.ParseAny(id)
	if err != nil {
		h.T.Fatal(err)
	}
	name, err := value.TextOf(ProjectName)
	if err != nil {
		h.T.Fatal(err)
	}
	if _, err := h.App.CreateWithID(context.Background(), store.SourceRepo, schema.TableProjects, pid, map[string]value.Value{"name": name}); err != nil {
		h.T.Fatal(err)
	}
	h.Project = id
}

// Reopen builds a second App over the same repo and home, as a new lore
// process would: nothing is held in memory, every row is read back from
// its file (or the cache rebuilt from the files). The first App stays
// open; both are closed at cleanup.
func (h *H) Reopen(mut ...func(*wire.Options)) *H {
	h.T.Helper()
	actor, err := ids.Parse[ids.Actor](Actor)
	if err != nil {
		h.T.Fatal(err)
	}
	opts := wire.Options{RepoDir: h.Repo, Home: h.Home, Actor: actor}
	for _, m := range mut {
		m(&opts)
	}
	a, err := wire.New(opts)
	if err != nil {
		h.T.Fatal(err)
	}
	h.T.Cleanup(func() {
		if err := a.Close(); err != nil {
			h.T.Error(err)
		}
	})
	return &H{T: h.T, App: a, Repo: h.Repo, Home: h.Home, Project: h.Project}
}

// ReopenCold is Reopen with every cache gone: the repo cache files are
// removed and the new App gets a fresh home, so each row is decoded from
// its row file (what a teammate's fresh clone, or a deleted cache, sees).
func (h *H) ReopenCold(mut ...func(*wire.Options)) *H {
	h.T.Helper()
	files, err := filepath.Glob(filepath.Join(h.Repo, filepath.FromSlash(wire.RepoCacheFile)) + "*")
	if err != nil {
		h.T.Fatal(err)
	}
	if len(files) == 0 {
		h.T.Fatal("ReopenCold: no repo cache file to remove (did the App write anything?)")
	}
	for _, f := range files {
		if err := os.Remove(f); err != nil {
			h.T.Fatal(err)
		}
	}
	home := h.T.TempDir()
	c := h.Reopen(append([]func(*wire.Options){func(o *wire.Options) { o.Home = home }}, mut...)...)
	c.Home = home
	return c
}
