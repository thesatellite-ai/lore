package wire

import (
	"encoding/json"
	"strings"
	"testing"

	opssupportbundle "github.com/thesatellite-ai/lore/ops/supportbundle"
)

// bundleSecrets are the planted values a support bundle must never carry,
// built at run time (CLAUDE.md conv. 8): a GitHub token, an AWS key id and
// the password of a credential URL.
func bundleSecrets() (gh, aws, pass string) {
	return "ghp_" + strings.Repeat("aB3", 12), "AKIA" + "Q7Z2X9KL0PM4N3B8", "s3cr" + strings.Repeat("Et9", 4)
}

// TestMaskBundleJSONPlanted plants the three secrets in every place a doctor
// report carries text (a warning, a note, an error, a nested sync file
// error, an array of path conflicts, an object key, a value that is only
// the secret, a secret glued to other text, two secrets in one string) and
// checks none survives anywhere in the masked JSON, the document stays valid
// JSON with the same keys and the non-text values (numbers, booleans, null)
// are kept. Break-verified: with textMasker.text returning its input
// unchanged the test fails on every planted value.
func TestMaskBundleJSONPlanted(t *testing.T) {
	t.Parallel()
	gh, aws, pass := bundleSecrets()
	credURL := "https://bob:" + pass + "@git.example.com/acme/repo.git"
	q := func(s string) string {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	raw := []byte(`{` +
		`"warnings":[` + q("live: cannot check the fetch refspec: fatal: unable to access '"+credURL+"/': 403") + `,` + q("token in env: "+gh) + `],` +
		`"notes":[` + q("aws "+aws+" and "+gh+" in one line") + `,` + q(gh) + `],` +
		`"errors":[` + q("glued:"+aws+":end") + `],` +
		`"sync":{"errors":[{"path":"x.json","detail":` + q("remote "+credURL) + `}],"tracked_files":3,"baselined":true},` +
		`"path_conflicts":["/usr/bin/lore",` + q("/home/"+gh+"/bin/lore") + `],` +
		`"by_key":{` + q(gh) + `:1},` +
		`"wal_size_bytes":0,"ok":false,"repo_addresses":null}`)
	if !json.Valid(raw) {
		t.Fatalf("fixture is not JSON: %s", raw)
	}
	keys := []string{"warnings", "notes", "errors", "sync", "path_conflicts", "by_key", "wal_size_bytes", "ok", "repo_addresses"}
	out := maskBundleJSON(raw)
	if out == nil {
		t.Fatal("masked report is null; want the masked document")
	}
	for _, s := range []string{gh, aws, pass} {
		if strings.Contains(string(out), s) {
			t.Errorf("planted secret %.6s… survived masking: %s", s, out)
		}
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("masked report is not JSON: %v\n%s", err, out)
	}
	for _, k := range keys {
		if _, ok := back[k]; !ok {
			t.Errorf("key %q lost", k)
		}
	}
	for k, want := range map[string]string{"wal_size_bytes": "0", "ok": "false", "repo_addresses": "null"} {
		if got := string(back[k]); got != want {
			t.Errorf("%s = %s, want %s", k, got, want)
		}
	}
	// the url keeps its host and path (the bug report stays useful)
	if !strings.Contains(string(out), "git.example.com/acme/repo.git") {
		t.Errorf("credential url lost its host and path: %s", out)
	}
}

// TestMaskSecretTextMatrix: text without a secret is unchanged; each kind of
// secret is replaced wherever it sits; overlapping or adjacent matches leave
// no part of either; empty text stays empty.
func TestMaskSecretTextMatrix(t *testing.T) {
	t.Parallel()
	gh, aws, pass := bundleSecrets()
	m := newTextMasker()
	if m.sc == nil {
		t.Fatal("the default-rule scanner did not build")
	}
	clean := []string{"", "plain warning: rows past trash.retention_days", "https://git.example.com/acme/repo.git", "ünïcode — line\nsecond line"}
	for _, s := range clean {
		if got := m.text(s); got != s {
			t.Errorf("clean text changed: %q -> %q", s, got)
		}
	}
	cases := map[string]struct{ text, secret string }{
		"github token alone":    {gh, gh},
		"github token in text":  {"token " + gh + " here", gh},
		"aws key at the start":  {aws + " is the key", aws},
		"aws key at the end":    {"key=" + aws, aws},
		"two secrets adjacent":  {gh + aws, gh},
		"url password":          {"fetch https://bob:" + pass + "@host.example/x failed", pass},
		"url password ssh form": {"ssh://bob:" + pass + "@host.example/x", pass},
	}
	for name, c := range cases {
		got := m.text(c.text)
		if strings.Contains(got, c.secret) {
			t.Errorf("%s: secret survived: %q", name, got)
		}
		if name == "two secrets adjacent" && strings.Contains(got, aws) {
			t.Errorf("%s: second secret survived: %q", name, got)
		}
	}
	// fail closed: a masker without a scanner drops every string whole
	if got := (textMasker{}).text("any text " + gh); got != maskedSecret {
		t.Errorf("scanner-less masker = %q, want %q", got, maskedSecret)
	}
}

// TestMaskBundleJSONInvalid: a doctor report that is not JSON is dropped
// (null in the bundle), never copied in unmasked.
func TestMaskBundleJSONInvalid(t *testing.T) {
	t.Parallel()
	gh, _, _ := bundleSecrets()
	for _, in := range []string{"", "not json " + gh, `{"a":`, `["x",`, `"unterminated`} {
		if out := maskBundleJSON([]byte(in)); out != nil {
			t.Errorf("maskBundleJSON(%q) = %s, want nil", in, out)
		}
	}
}

// TestCoreSupportBundle: the core host on an App without a supplied one
// (cmd/lore): SupportHost never answers E_UNSUPPORTED; in a project the
// bundle has the identity a write uses, a doctor report that is JSON, the
// v0.1.17 count keys with the live memory counted, and no row text; outside
// a project the counts are empty and the bundle is still produced.
func TestCoreSupportBundle(t *testing.T) {
	t.Parallel()
	ctx := background()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	body := "support bundle body text " + strings.Repeat("z", 8)
	if _, err := run(t, a, "memory.add", `{"body":"`+body+`"}`, true); err != nil {
		t.Fatal(err)
	}
	h, err := a.SupportHost(ctx)
	if err != nil {
		t.Fatalf("SupportHost: %v", err)
	}
	b := opssupportbundle.Build(ctx, h, a.Now(), false)
	if b.Identity.Resolved == "" || b.Identity.Source == "" {
		t.Errorf("identity = %+v, want the write identity", b.Identity)
	}
	if !json.Valid(b.Doctor) || string(b.Doctor) == "null" {
		t.Errorf("doctor = %s, want the report", b.Doctor)
	}
	for _, k := range supportCoreTables {
		if _, ok := b.Counts[k]; !ok {
			t.Errorf("counts lack %s: %v", k, b.Counts)
		}
	}
	if b.Counts["memories"] != 1 {
		t.Errorf("memories = %d, want 1", b.Counts["memories"])
	}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), body) {
		t.Errorf("bundle carries row text: %s", data)
	}
	if !b.Sanitized || b.IncludeContent {
		t.Errorf("sanitized=%v include_content=%v, want true/false", b.Sanitized, b.IncludeContent)
	}

	out := newTestApp(t, t.TempDir()) // not a repo, not a project
	h2, err := out.SupportHost(ctx)
	if err != nil {
		t.Fatalf("SupportHost outside a project: %v", err)
	}
	b2 := opssupportbundle.Build(ctx, h2, out.Now(), false)
	if len(b2.Counts) != 0 {
		t.Errorf("counts outside a project = %v, want {}", b2.Counts)
	}
	if b2.Sessions == nil {
		t.Error("sessions = nil, want []")
	}
}
