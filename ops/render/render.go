// Package render defines `lore render` (layer L5, CLI_REFERENCE 2.14,
// CONTEXT_BUDGET 6.1 to 6.11, TOOL NEUTRAL): it writes .lore/LORE.md
// within the budget of the repo's tool profiles and wires each tool's
// pointer file to it.
//
//	render.run ─▶ flag checks (--scopes needs --report; one read mode at a time)
//	           ─▶ --report [--scopes] | --trend: read only; the text, and the
//	              --json data (the v0.1.17 report / trend object as it is)
//	           ─▶ --check: read only; E_REFUSED over the warn level or the budget
//	           ─▶ --dry-run: the generated body, nothing written
//	           └▶ default: LORE.md, LORE.full.md, the pointer blocks, path rule
//	              files, folder files, the private render; one line per file
//
// The rendering lives in the binary that owns the render history and the
// committed-render sources (PASS3 ONE-PATH, P3-MISC-7); it writes the
// checkout's own files, so the operation is Local.
package render

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore render`, a bare-noun shortcut of run).
const (
	Noun  = "render"
	OpRun = "render.run"
	// RunVerb is the generated verb the bare `lore render` runs.
	RunVerb = "run"
	// DefaultOut is the generated file when --out is not given.
	DefaultOut = ".lore/LORE.md"
)

// restNoun keeps the REST collection "render" un-pluralised.
const restNoun = "render"

// RunIn is the input of render.run (CLI_REFERENCE 2.14).
type RunIn struct {
	// Target is repeatable and each value may be comma-separated.
	Target    []string `json:"target,omitempty" flag:"target" desc:"tool profile name or pointer file to wire (repeatable, comma-separated; default: the repo's tool files, else AGENTS.md)"`
	Out       string   `json:"out,omitempty" flag:"out" desc:"generated knowledge file path (default .lore/LORE.md)"`
	NoPointer bool     `json:"no_pointer,omitempty" flag:"no-pointer" desc:"do not stitch the pointer into the agent file"`
	DryRun    bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"print the generated body instead of writing it"`
	Check     bool     `json:"check,omitempty" flag:"check" desc:"CI check: write nothing, fail when LORE.md would be over the warn level or the budget"`
	Report    bool     `json:"report,omitempty" flag:"report" desc:"print the budget report (what is full, what is a line, what moved out) and write nothing"`
	Scopes    bool     `json:"scopes,omitempty" flag:"scopes" desc:"with --report: one row per generated file per tool, plus the Codex and Claude Code chain sums"`
	Trend     bool     `json:"trend,omitempty" flag:"trend" desc:"bytes per render over the last 14 days, growth per day and when LORE.md reaches its budget; writes nothing"`
}

// Targets are the --target values split on commas, blanks dropped.
func (in RunIn) Targets() []string {
	var out []string
	for _, v := range in.Target {
		for p := range strings.SplitSeq(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// OutPath is --out, or DefaultOut.
func (in RunIn) OutPath() string {
	if in.Out == "" {
		return DefaultOut
	}
	return in.Out
}

// ReadOnly reports whether the run writes nothing.
func (in RunIn) ReadOnly() bool { return in.Check || in.Report || in.Trend || in.DryRun }

// Answer is what Host.Render returns.
type Answer struct {
	// Text is the command's lines (or the --dry-run body).
	Text string
	// Data is the --report / --trend object for --json (nil otherwise).
	Data []byte
}

// Host renders. Invariant: a ReadOnly input writes nothing (no file, no
// render record).
type Host interface {
	Render(ctx context.Context, in RunIn) (Answer, error)
}

// Env is what lore render needs; *wire.App implements it.
type Env interface {
	// RenderHost is the binary's Host (E_UNSUPPORTED when it has none).
	RenderHost(ctx context.Context) (Host, error)
}

// RawJSON is a JSON value kept as its bytes: it encodes as itself, and is
// text for the operation's field model (TypeText).
type RawJSON []byte

// MarshalJSON implements json.Marshaler ("null" when empty).
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

// RunOut is the output of render.run.
type RunOut struct {
	// Report is the --report or --trend object (v0.1.17's --json form).
	Report RawJSON `json:"report,omitempty"`
	// Output is the command's lines.
	Output string `json:"output,omitempty"`
}

// Text implements op.Texter.
func (o RunOut) Text() string { return o.Output }

// MarshalJSON keeps v0.1.17's `--report --json` and `--trend --json`: the
// report object itself; any other run is {"output": …}.
func (o RunOut) MarshalJSON() ([]byte, error) {
	if len(o.Report) > 0 {
		return o.Report, nil
	}
	type plain RunOut // no methods: the default encoding
	return json.Marshal(plain(o))
}

// Ops returns the render operation bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpRun, Summary: "Render .lore/LORE.md from scoped knowledge and point your AI tools at it",
			Permission: op.PermWrite, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			Errors: []errs.Code{errs.InvalidInput, errs.Refused, errs.ConfigInvalid, errs.NotFound, errs.Unsupported, errs.Internal},
			Examples: []op.Example{
				{Title: "render and wire the repo's tools", CLI: "lore render", Want: ""},
				{Title: "CI: fail when LORE.md is over its budget", CLI: "lore render --check", Want: ""},
			},
		}, func(ctx context.Context, in RunIn) (RunOut, error) {
			h, err := env.RenderHost(ctx)
			if err != nil {
				return RunOut{}, err
			}
			return Run(ctx, h, in)
		}),
	}
}

// Run is render.run over h.
func Run(ctx context.Context, h Host, in RunIn) (RunOut, error) {
	if in.Scopes && !in.Report {
		return RunOut{}, errs.New(errs.InvalidInput, "--scopes needs --report").WithHint("run `lore render --report --scopes`")
	}
	a, err := h.Render(ctx, in)
	if err != nil {
		return RunOut{}, err
	}
	return RunOut{Report: a.Data, Output: a.Text}, nil
}
