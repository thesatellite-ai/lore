package supportbundle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// fakeHost answers fixed parts; empty leaves every part empty (outside a
// project).
type fakeHost struct{ empty bool }

func (fakeHost) Version() string { return "v9.9.9" }
func (fakeHost) Identity(context.Context) Identity {
	return Identity{Resolved: "git:mira@acme.example", Source: "git-email", Stable: true}
}

func (h fakeHost) Doctor(context.Context) RawJSON {
	if h.empty {
		return nil
	}
	return RawJSON(`{"checks":[]}`)
}

func (h fakeHost) Counts(context.Context) Counts {
	if h.empty {
		return nil
	}
	return Counts{"memories": 2, "rules": 1}
}

func (h fakeHost) Sessions(context.Context) []Session {
	if h.empty {
		return nil
	}
	return []Session{{Tool: "claude_code", HasToolSessionID: true}}
}

var now = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

func TestBundleStdoutAndFile(t *testing.T) {
	out, err := Run(context.Background(), fakeHost{}, now, RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	var b Bundle
	if jerr := json.Unmarshal([]byte(out.Text()), &b); jerr != nil {
		t.Fatalf("human form is not the bundle JSON: %v\n%s", jerr, out.Text())
	}
	if b.SchemaVersion != SchemaVersion || b.Counts["memories"] != 2 || !b.Sanitized || b.IncludeContent ||
		b.BinaryVersion != "v9.9.9" || b.GeneratedAt != "2026-11-05T10:00:00Z" || len(b.Sessions) != 1 || !strings.Contains(string(b.Doctor), `"checks"`) {
		t.Fatalf("bundle: %+v", b)
	}
	file := filepath.Join(t.TempDir(), "sub", "bundle.json")
	out, err = Run(context.Background(), fakeHost{}, now, RunIn{Out: file, IncludeContent: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Text(), "✓ wrote support bundle to "+file+" (") {
		t.Fatalf("--out line: %q", out.Text())
	}
	data, err := os.ReadFile(filepath.Clean(file))
	if err != nil || len(data) != out.Bytes {
		t.Fatalf("file: %v %d/%d", err, len(data), out.Bytes)
	}
	fi, err := os.Stat(file)
	// Windows has no POSIX mode bits (os.Chmod only sets read-only)
	if err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != fileMode) {
		t.Fatalf("file mode: %v %v", err, fi.Mode())
	}
	if err := json.Unmarshal(data, &b); err != nil || b.Sanitized || !b.IncludeContent {
		t.Fatalf("--include-content: %v %+v", err, b)
	}
}

// Outside a project every part is empty, never null (a reader indexes
// counts and sessions directly).
func TestBundleEmptyParts(t *testing.T) {
	b := Build(context.Background(), fakeHost{empty: true}, now, false)
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{`"counts":{}`, `"sessions":[]`, `"doctor":null`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
}

type env struct{ host Host }

func (e env) SupportHost(context.Context) (Host, error) {
	if e.host == nil {
		return nil, errs.New(errs.Unsupported, "support-bundle: not available in this build")
	}
	return e.host, nil
}
func (env) Now() time.Time { return now }

// The operation registers (Local, read) and a binary without the host
// answers E_UNSUPPORTED, never an empty bundle.
func TestOpsSpec(t *testing.T) {
	reg, err := op.NewRegistry(Ops(env{})...)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := reg.Lookup(OpRun)
	if !ok || !e.Spec().Local || e.Spec().Permission != op.PermRead {
		t.Fatalf("spec: %v %+v", ok, e.Spec())
	}
	in, err := e.Decode(op.JSONDecoder{Body: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Invoke(context.Background(), in); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("no host: %v", err)
	}
}

// Counts round-trip through their text form (the field model's view).
func TestCountsText(t *testing.T) {
	c := Counts{"rules": 1, "memories": 2}
	b, err := c.MarshalText()
	if err != nil || string(b) != "memories=2,rules=1" {
		t.Fatalf("text: %q %v", b, err)
	}
	var back Counts
	if err := back.UnmarshalText(b); err != nil || back["memories"] != 2 || back["rules"] != 1 {
		t.Fatalf("back: %v %v", back, err)
	}
	if err := back.UnmarshalText([]byte("rules")); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("bad pair: %v", err)
	}
}
