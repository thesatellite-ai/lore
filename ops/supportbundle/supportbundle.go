// Package supportbundle defines `lore support-bundle` (layer L5,
// CLI_REFERENCE 2.11, AGENT_IDENTITY 9): a sanitized JSON bundle of
// lore's state for a bug report.
//
//	support_bundle.run ─▶ Host: version, identity, doctor report, row counts, newest sessions
//	                   ─▶ Bundle (schema_version 1, sessions already stripped by the host)
//	                   ─▶ no --out: the bundle, indented, on stdout (v0.1.17)
//	                   └▶ --out <file>: folders made, file written 0600,
//	                      "✓ wrote support bundle to <file> (N bytes)"
//
// The bundle never carries row text: v0.1.17 printed none even with
// --include-content (the flag only marks the bundle unsanitized), and this
// port keeps that. The doctor report, the counts and the sessions come
// from the binary that owns lore.db (PASS3 ONE-PATH, P3-MISC-3); the
// bundle is a file on this machine, so the operation is Local.
package supportbundle

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore support-bundle`, a bare-noun shortcut of run).
const (
	Noun  = "support_bundle"
	OpRun = "support_bundle.run"
	// RunVerb is the generated verb the bare `lore support-bundle` runs.
	RunVerb = "run"
)

// restNoun keeps the REST collection "support-bundle" un-pluralised.
const restNoun = "support_bundle"

// SchemaVersion is the v0.1.17 bundle schema_version.
const SchemaVersion = 1

// Modes of the --out file and the folders made for it (the CLI's own
// modes: the bundle describes this machine, so only its owner reads it).
const (
	fileMode fs.FileMode = 0o600
	dirMode  fs.FileMode = 0o750
)

// noteSanitized is the bundle's note (v0.1.17 wording).
const noteSanitized = "Sanitized: no memory/decision/rule body content included. " +
	"Use --include-content to include if needed for bug repro."

// Identity is how this machine resolves the person (v0.1.17 keys).
type Identity struct {
	Resolved string `json:"resolved"`
	Source   string `json:"source"`
	Stable   bool   `json:"stable"`
}

// Session is one agent session in a bundle. AGENT_IDENTITY 9: the tool
// session id, model, cwd and ci are stripped, keeping only whether they
// were present; the tool, its version and the deciding signal stay (they
// are not secrets and explain detection bugs).
type Session struct {
	Tool             string   `json:"tool,omitempty"`
	ToolVersion      string   `json:"tool_version,omitempty"`
	Signal           string   `json:"signal,omitempty"`
	Flags            []string `json:"flags,omitempty"`
	DeclaredHuman    bool     `json:"declared_human,omitempty"`
	HasToolSessionID bool     `json:"has_tool_session_id"`
	HasModel         bool     `json:"has_model"`
	HasCwd           bool     `json:"has_cwd"`
	HasCI            bool     `json:"has_ci"`
}

// Host is the machinery behind the bundle, supplied by the binary that
// owns lore.db. Invariant: nothing it returns carries row text or a tool
// session id; each part degrades to empty (never an error) so a bundle is
// produced even outside a project, where it is most needed.
type Host interface {
	// Version is the binary's version.
	Version() string
	// Identity is how this machine resolves the person.
	Identity(ctx context.Context) Identity
	// Doctor is `lore doctor --json`'s report (null when it cannot run).
	Doctor(ctx context.Context) RawJSON
	// Counts are row counts per table (empty outside a project).
	Counts(ctx context.Context) Counts
	// Sessions are the newest agent sessions, already stripped.
	Sessions(ctx context.Context) []Session
}

// Env is what support-bundle needs; *wire.App implements it.
type Env interface {
	// SupportHost is the binary's Host (E_UNSUPPORTED when it has none).
	SupportHost(ctx context.Context) (Host, error)
	// Now is the wall clock.
	Now() time.Time
}

// RunIn is the input of support_bundle.run.
type RunIn struct {
	Out            string `json:"out,omitempty" flag:"out" desc:"output file (default: stdout)"`
	IncludeContent bool   `json:"include_content,omitempty" flag:"include-content" desc:"mark the bundle as not sanitized, for a bug that needs content to reproduce (NOT default)"`
}

// RawJSON is a JSON value kept as its bytes (the doctor report, whose
// type is the binary's): it encodes as itself in JSON, and as its text for
// the operation's field model (TypeText), so no untyped value is needed.
type RawJSON []byte

// MarshalJSON implements json.Marshaler: the bytes as they are ("null"
// when empty).
func (r RawJSON) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return r, nil
}

// UnmarshalJSON implements json.Unmarshaler: a copy of the bytes.
func (r *RawJSON) UnmarshalJSON(b []byte) error {
	*r = append((*r)[:0], b...)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (r RawJSON) MarshalText() ([]byte, error) { return r, nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (r *RawJSON) UnmarshalText(b []byte) error { return r.UnmarshalJSON(b) }

// Counts are row counts per table. A JSON object in the bundle; for the
// operation's field model (string maps only) it is text, "table=n" pairs
// joined by commas in table order.
type Counts map[string]int

// MarshalJSON implements json.Marshaler: the plain object.
func (c Counts) MarshalJSON() ([]byte, error) { return json.Marshal(map[string]int(c)) }

// UnmarshalJSON implements json.Unmarshaler.
func (c *Counts) UnmarshalJSON(b []byte) error {
	m := map[string]int{}
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*c = m
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (c Counts) MarshalText() ([]byte, error) {
	keys := slices.Sorted(maps.Keys(c))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+strconv.Itoa(c[k]))
	}
	return []byte(strings.Join(parts, ",")), nil
}

// UnmarshalText implements encoding.TextUnmarshaler (MarshalText's form).
func (c *Counts) UnmarshalText(b []byte) error {
	m := map[string]int{}
	for part := range strings.SplitSeq(string(b), ",") {
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		n, err := strconv.Atoi(v)
		if !ok || err != nil {
			return errs.New(errs.InvalidInput, "counts: want table=n, got "+part)
		}
		m[k] = n
	}
	*c = m
	return nil
}

// Bundle is the support bundle (the v0.1.17 JSON keys, in order).
type Bundle struct {
	SchemaVersion int      `json:"schema_version"`
	GeneratedAt   string   `json:"generated_at"`
	BinaryVersion string   `json:"binary_version"`
	GoVersion     string   `json:"go_version"`
	OS            string   `json:"os"`
	Arch          string   `json:"arch"`
	Identity      Identity `json:"identity"`
	Doctor        RawJSON  `json:"doctor"`
	Counts        Counts   `json:"counts"`
	// Sessions are the newest agent sessions, stripped (AGENT_IDENTITY 9).
	Sessions       []Session `json:"sessions"`
	Note           string    `json:"note"`
	Sanitized      bool      `json:"sanitized"`
	IncludeContent bool      `json:"include_content"`
}

// RunOut is the output of support_bundle.run: the bundle, and with --out
// the file it went to. Under --json the bundle sits under "bundle"
// (v0.1.17 had no --json; its stdout was the bare bundle, which the human
// form keeps, P3-MISC-5).
type RunOut struct {
	// Bundle is the bundle itself (the whole human form without --out).
	Bundle Bundle `json:"bundle"`
	// Wrote is the --out file (empty: the bundle went to stdout).
	Wrote string `json:"wrote,omitempty"`
	// Bytes is the size of the written file.
	Bytes int `json:"bytes,omitempty"`
	// pretty is the indented bundle the human form prints.
	pretty string
}

// Text implements op.Texter: the indented bundle, or the "wrote" line.
func (o RunOut) Text() string {
	if o.Wrote != "" {
		return fmt.Sprintf("✓ wrote support bundle to %s (%d bytes)\n", o.Wrote, o.Bytes)
	}
	return o.pretty + "\n"
}

// Ops returns the support-bundle operation bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpRun, Summary: "Produce a sanitized incident-report bundle (no row text)",
			Permission: op.PermRead, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			Errors:   []errs.Code{errs.Unsupported, errs.Internal},
			Examples: []op.Example{{Title: "write a bundle for a bug report", CLI: "lore support-bundle --out /tmp/lore-bundle.json", Want: ""}},
		}, func(ctx context.Context, in RunIn) (RunOut, error) {
			h, err := env.SupportHost(ctx)
			if err != nil {
				return RunOut{}, err
			}
			return Run(ctx, h, env.Now(), in)
		}),
	}
}

// Run builds the bundle from h at now and, with in.Out, writes it there.
func Run(ctx context.Context, h Host, now time.Time, in RunIn) (RunOut, error) {
	b := Build(ctx, h, now, in.IncludeContent)
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return RunOut{}, errs.Wrap(errs.Internal, "marshal bundle", err)
	}
	out := RunOut{Bundle: b, pretty: string(data)}
	if in.Out == "" {
		return out, nil
	}
	if err := os.MkdirAll(filepath.Dir(in.Out), dirMode); err != nil {
		return RunOut{}, errs.Wrap(errs.Internal, "mkdir output", err)
	}
	if err := os.WriteFile(in.Out, data, fileMode); err != nil {
		return RunOut{}, errs.Wrap(errs.Internal, "write bundle", err)
	}
	out.Wrote, out.Bytes = in.Out, len(data)
	return out, nil
}

// Build assembles the bundle; empty parts are empty, never null, so a
// reader can index counts and sessions without a nil check.
func Build(ctx context.Context, h Host, now time.Time, includeContent bool) Bundle {
	doctor := h.Doctor(ctx)
	if len(doctor) == 0 {
		doctor = RawJSON("null")
	}
	counts := h.Counts(ctx)
	if counts == nil {
		counts = Counts{}
	}
	sessions := h.Sessions(ctx)
	if sessions == nil {
		sessions = []Session{}
	}
	return Bundle{
		SchemaVersion: SchemaVersion, GeneratedAt: now.UTC().Format(time.RFC3339Nano),
		BinaryVersion: h.Version(), GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		Identity: h.Identity(ctx), Doctor: doctor, Counts: counts, Sessions: sessions,
		Note: noteSanitized, Sanitized: !includeContent, IncludeContent: includeContent,
	}
}
