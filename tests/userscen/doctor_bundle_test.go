//go:build userscen

package userscen

// `lore support-bundle` (docs/operations/doctor.md "Send a support
// bundle"), as a person attaching it to a bug report uses it:
//
//   - stdout: one JSON bundle, schema_version 1, every v0.1.17 key,
//     sanitized true, the doctor report inside, row counts per table
//   - --out <file> (folders made): "✓ wrote support bundle to <file> (N
//     bytes)", N is the file size, mode 0600
//   - --json: the bundle under "bundle", plus "wrote"/"bytes" with --out
//   - --include-content: sanitized false, include_content true, and still
//     no note text (the documented pitfall)
//   - outside a project: still a bundle, counts {}
//   - never: note bodies (rule, memory, decision), a token in a git
//     remote URL, a token in the environment, a session id, or a secret a
//     doctor line quotes (a broken row file whose field name is a token):
//     every canary is built at run time and searched for in the raw bytes
//   - --out to a path that cannot be written: an error, no "wrote" line

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// bundle is the part of a support bundle the scenarios read.
type bundle struct {
	SchemaVersion int    `json:"schema_version"`
	GeneratedAt   string `json:"generated_at"`
	BinaryVersion string `json:"binary_version"`
	GoVersion     string `json:"go_version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Identity      struct {
		Resolved string `json:"resolved"`
	} `json:"identity"`
	Doctor *struct {
		Status string   `json:"status"`
		Errors []string `json:"errors"`
	} `json:"doctor"`
	Counts         map[string]int    `json:"counts"`
	Sessions       []json.RawMessage `json:"sessions"`
	Note           string            `json:"note"`
	Sanitized      bool              `json:"sanitized"`
	IncludeContent bool              `json:"include_content"`
}

// bundleKeys are the bundle's top-level keys (ops/supportbundle.Bundle).
var bundleKeys = []string{"schema_version", "generated_at", "binary_version", "go_version", "os", "arch", "identity", "doctor", "counts", "sessions", "note", "sanitized", "include_content"}

func decodeBundle(t *testing.T, raw string) bundle {
	t.Helper()
	var b bundle
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatalf("a bundle is one JSON object: %v\n%s", err, raw)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range bundleKeys {
		if _, ok := keys[k]; !ok {
			t.Fatalf("bundle lacks key %q: %s", k, raw)
		}
	}
	if len(keys) != len(bundleKeys) {
		t.Fatalf("bundle has keys beyond the documented ones: %s", raw)
	}
	return b
}

func TestDoctorBundle_StdoutFileAndJSON(t *testing.T) {
	p := newDoctorPerson(t)
	p.c.MustLore("memory", "add", "--body", "a remembered thing")

	res := p.c.MustLore("support-bundle")
	b := decodeBundle(t, res.Stdout)
	if b.SchemaVersion != 1 || !b.Sanitized || b.IncludeContent || b.BinaryVersion == "" || b.GoVersion == "" || b.OS == "" || b.Arch == "" ||
		!strings.HasPrefix(b.Note, "Sanitized: no memory/decision/rule body content included.") || b.Identity.Resolved != "human:person@example.com" {
		t.Fatalf("stdout bundle: %s", res.Stdout)
	}
	if b.Doctor == nil || b.Doctor.Status != "healthy" || b.Counts["rules"] != 1 || b.Counts["memories"] != 1 || b.Sessions == nil {
		t.Fatalf("doctor report, counts and sessions inside: %s", res.Stdout)
	}

	out := filepath.Join(p.w.Root, "reports", "deep", "bundle.json")
	w := p.c.MustLore("support-bundle", "--out", out)
	data, err := os.ReadFile(filepath.Clean(out))
	if err != nil {
		t.Fatal(err)
	}
	if w.Stdout != "✓ wrote support bundle to "+out+" ("+strconv.Itoa(len(data))+" bytes)\n" {
		t.Fatalf("--out line names the file and its exact size: %q", w.Stdout)
	}
	if st, serr := os.Stat(out); serr != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("bundle file mode 0600: %v %v", st.Mode(), serr)
	}
	decodeBundle(t, string(data))

	// Twice to the same file: overwritten, still one valid bundle.
	p.c.MustLore("support-bundle", "--out", out)
	data2, err := os.ReadFile(filepath.Clean(out))
	if err != nil {
		t.Fatal(err)
	}
	decodeBundle(t, string(data2))

	var env struct {
		Bundle json.RawMessage `json:"bundle"`
		Wrote  string          `json:"wrote"`
		Bytes  int             `json:"bytes"`
	}
	j := p.c.MustLore("support-bundle", "--json")
	if err := json.Unmarshal([]byte(j.Stdout), &env); err != nil || env.Wrote != "" || len(env.Bundle) == 0 {
		t.Fatalf("--json: the bundle under \"bundle\": %v %s", err, j)
	}
	decodeBundle(t, string(env.Bundle))
	out2 := filepath.Join(p.w.Root, "b2.json")
	j = p.c.MustLore("support-bundle", "--json", "--out", out2)
	if err := json.Unmarshal([]byte(j.Stdout), &env); err != nil || env.Wrote != out2 || env.Bytes == 0 {
		t.Fatalf("--json --out: wrote and bytes: %v %s", err, j)
	}
}

func TestDoctorBundle_IncludeContentAddsNoText(t *testing.T) {
	p := newDoctorPerson(t)
	canary := "canary" + strconv.Itoa(7*191) + "body"
	p.c.MustLore("memory", "add", "--body", "the deploy key lives in "+canary)
	res := p.c.MustLore("support-bundle", "--include-content")
	b := decodeBundle(t, res.Stdout)
	if b.Sanitized || !b.IncludeContent {
		t.Fatalf("--include-content marks the bundle unsanitized: %s", res.Stdout)
	}
	if strings.Contains(res.Stdout, canary) {
		t.Fatalf("--include-content adds no note text today (docs pitfall): %s", res.Stdout)
	}
}

func TestDoctorBundle_OutsideAProject(t *testing.T) {
	p := newDoctorPerson(t)
	res := p.a.Lore(filepath.Join(p.w.Root, "nowhere"), "support-bundle").OK(t)
	b := decodeBundle(t, res.Stdout)
	if len(b.Counts) != 0 || b.Doctor == nil {
		t.Fatalf("outside a project: counts {} and a doctor report: %s", res.Stdout)
	}
}

func TestDoctorBundle_UnwritableOut(t *testing.T) {
	p := newDoctorPerson(t)
	blocker := filepath.Join(p.w.Root, "a-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := p.c.Lore("support-bundle", "--out", filepath.Join(blocker, "bundle.json"))
	if res.Code == 0 || strings.Contains(res.Stdout, "wrote support bundle") || !strings.Contains(res.Out(), "E_") {
		t.Fatalf("an --out under a file fails with a lore error code: %s", res)
	}
}

// githubPAT is a GitHub token-shaped value built at run time (never a
// literal): ghp_ and 36 alphanumerics, which the gitleaks default rules
// recognise.
func githubPAT(seed string) string {
	s := strings.Repeat(seed+"Zx9Q", 12)
	return "gh" + "p_" + s[:36]
}

func TestDoctorBundle_NeverCarriesSecretsOrBodies(t *testing.T) {
	p := newDoctorPerson(t)
	tag := "cnry" + strconv.Itoa(4242)
	rel := p.ruleFile(t) // the first rule, broken below
	bodies := map[string][]string{
		"rule":     {"rule", "add", "--body", "rule text " + tag + "R"},
		"memory":   {"memory", "add", "--body", "memory text " + tag + "M"},
		"decision": {"decision", "add", "--title", "decision title " + tag + "DT", "--body", "decision text " + tag + "D"},
	}
	for kind, args := range bodies {
		if res := p.c.Lore(args...); res.Code != 0 {
			t.Fatalf("add %s: %s", kind, res)
		}
	}
	remoteToken := githubPAT("rem")
	envToken := githubPAT("env")
	fieldToken := githubPAT("fld")
	sessionID := "sess-" + tag + "S"

	// A token in the remote URL (doctor warns about it, naming the remote).
	userinfo := strings.Join([]string{"person", remoteToken}, ":")
	p.c.Git("remote", "set-url", "origin", "https"+"://"+userinfo+"@"+doctorSlug+".git")
	// A broken row file whose field name is a token: doctor quotes the
	// field name in its error line.
	body, _ := p.c.ReadFile(rel)
	p.c.WriteFile(rel, strings.Replace(body, `"severity"`, `"`+fieldToken+`": 1,`+"\n  "+`"severity"`, 1))

	res := p.c.With("GITHUB_TOKEN="+envToken, "LORE_SESSION="+sessionID).Lore("support-bundle")
	if res.Code != 0 {
		t.Fatalf("support-bundle: %s", res)
	}
	b := decodeBundle(t, res.Stdout)
	if b.Doctor == nil || b.Doctor.Status != "broken" || len(b.Doctor.Errors) == 0 {
		t.Fatalf("the bundle's doctor report shows the broken row file: %s", res.Stdout)
	}
	for name, secret := range map[string]string{
		"rule body": tag + "R", "memory body": tag + "M", "decision title": tag + "DT", "decision body": tag + "D",
		"remote token": remoteToken, "env token": envToken, "token quoted by doctor": fieldToken, "session id": sessionID,
	} {
		if strings.Contains(res.Stdout, secret) { // stdout is the bundle (stderr is the terminal)
			t.Fatalf("the bundle carries the %s: %s", name, res.Stdout)
		}
	}
	// The masked doctor line still says what is wrong.
	if !hasLine(b.Doctor.Errors, rel, "is not a usable row file") {
		t.Fatalf("the masked doctor error still names the file: %s", res.Stdout)
	}
}
