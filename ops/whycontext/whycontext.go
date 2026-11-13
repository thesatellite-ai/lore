// Package whycontext defines `lore why-context` (layer L5, CLI_REFERENCE
// 2.14, CONTEXT_BUDGET 6.11): what the last `lore render` wrote to the
// context file, and with --cut what it moved out of the core of
// .lore/LORE.md and why.
//
//	why_context.show ─▶ Env.LastRender (the newest render record of this
//	                    machine; E_NOT_FOUND before the first render)
//	                 ─▶ default: id, time, target, bytes, sha256, scope (+ the text with --rendered)
//	                 ─▶ --cut: the record's cut summary (FA-CTX-1), E_NOT_FOUND on a
//	                    record written before 0.1.18
//
// The render records are machine-local (P3-LOCAL-3, render_histories); the
// binary that keeps them supplies Env.LastRender, so this package only
// reads one typed record and prints it (PASS3 ONE-PATH, P3-MISC-3).
package whycontext

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore why-context`, a bare-noun shortcut of show).
const (
	Noun   = "why_context"
	OpShow = "why_context.show"
)

// restNoun keeps the REST collection "why-context" un-pluralised.
const restNoun = "why_context"

// SchemaVersion is the v0.1.17 --json schema_version.
const SchemaVersion = 1

// timeLayout is the created_at form of the summary (v0.1.17).
const timeLayout = "2006-01-02 15:04:05Z"

// Record is one render as the render history keeps it.
type Record struct {
	ID         string
	CreatedAt  time.Time
	TargetPath string
	TotalBytes int
	SHA256     string
	// ScopeSummary is the record's scope summary JSON text; its
	// cut_summary key is the cut (absent before 0.1.18).
	ScopeSummary string
	// Text is the rendered text.
	Text string
}

// Env is what why-context needs; *wire.App implements it.
type Env interface {
	// LastRender is this machine's newest render record of the repo
	// (E_NOT_FOUND when there is none; E_UNSUPPORTED in a binary that
	// keeps no render history).
	LastRender(ctx context.Context) (Record, error)
}

// ShowIn is the input of why_context.show.
type ShowIn struct {
	Cut      bool `json:"cut,omitempty" flag:"cut" desc:"show what the last render moved out of the core of LORE.md, and why"`
	Rendered bool `json:"rendered,omitempty" flag:"rendered" desc:"also print the full rendered text"`
}

// Entry is one row the render moved out of the core, with its reason.
type Entry struct {
	ID     string            `json:"id"`
	Reason contextfit.Reason `json:"reason"`
}

// Cut is what one render moved out of the core of LORE.md (the
// cut_summary key of the record's scope summary, FA-CTX-1).
type Cut struct {
	File    string `json:"file"`
	Profile string `json:"profile"`
	Budget  int    `json:"budget"`
	Before  int    `json:"before" desc:"bytes with every pinned entry in full"`
	After   int    `json:"after" desc:"bytes written"`
	Over    bool   `json:"budget_exceeded"`
	Index   bool   `json:"index_collapsed" desc:"the topic index was collapsed to one line"`
	// Entries: superseded and manual rows (left out), then must rules in
	// line form (budget or entry_max), each with its reason.
	Entries []Entry `json:"entries"`
}

// Counts are the rows a render included in full.
type Counts struct {
	Rules    int `json:"rules"`
	Hotfixes int `json:"hotfixes"`
	Memories int `json:"memories"`
}

// Scope is the record's scope summary: what the render included, and its
// cut (nil on a record written before 0.1.18).
type Scope struct {
	Included Counts `json:"included"`
	Cut      Cut    `json:"cut_summary,omitzero"`
}

// scopeJSON decodes a record's scope summary, telling an absent cut
// (a record written before 0.1.18) from an empty one.
type scopeJSON struct {
	Included Counts `json:"included"`
	Cut      *Cut   `json:"cut_summary"`
}

// ShowOut is the output of why_context.show: the v0.1.17 --json shape
// (the project id is left out on purpose, a privacy default), or with
// --cut {data: <cut>}.
type ShowOut struct {
	SchemaVersion int       `json:"schema_version,omitempty"`
	RenderID      string    `json:"render_id,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitzero"`
	TargetPath    string    `json:"target_path,omitempty"`
	TotalBytes    int       `json:"total_bytes,omitempty"`
	SHA256        string    `json:"rendered_sha256,omitempty"`
	ScopeSummary  Scope     `json:"scope_summary,omitzero"`
	RenderedText  string    `json:"rendered_text,omitempty"`
	Data          Cut       `json:"data,omitzero"`
	// cut: Data is the answer (--cut), not the summary.
	cut bool
}

// Text prints the v0.1.17 summary, or the cut list.
func (o ShowOut) Text() string {
	if o.cut {
		return o.Data.Text()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "render_id:    %s\n", o.RenderID)
	fmt.Fprintf(&b, "created_at:   %s\n", o.CreatedAt.UTC().Format(timeLayout))
	fmt.Fprintf(&b, "target:       %s\n", o.TargetPath)
	fmt.Fprintf(&b, "total_bytes:  %d\n", o.TotalBytes)
	fmt.Fprintf(&b, "sha256:       %s\n", o.SHA256)
	fmt.Fprintf(&b, "scope:        %s\n", o.ScopeSummary.Text())
	if o.RenderedText != "" {
		b.WriteString("\n--- rendered text ---\n")
		b.WriteString(o.RenderedText + "\n")
	}
	return b.String()
}

// Text is the scope summary as a person reads it: what went in in full,
// and the cut (bytes against the budget, how many entries moved out of
// the core). Why: the summary line was the record's JSON (W-8); --json
// keeps scope_summary as it was.
func (s Scope) Text() string {
	in := s.Included
	t := fmt.Sprintf("%d rules, %d hotfixes, %d memories in full", in.Rules, in.Hotfixes, in.Memories)
	c := s.Cut
	if c.File == "" && c.Budget == 0 {
		return t // a record written before 0.1.18 has no cut
	}
	t += fmt.Sprintf("; %d of %d bytes (profile %s)", c.After, c.Budget, c.Profile)
	if c.Over {
		t += ", over budget"
	}
	if len(c.Entries) == 0 {
		return t + ", nothing moved out of the core"
	}
	return t + fmt.Sprintf(", %d moved out of the core (lore why-context --cut lists them)", len(c.Entries))
}

// Text is the human form of the cut list (CONTEXT_BUDGET 6.11).
func (c Cut) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "file:     %s (profile %s)\n", c.File, c.Profile)
	fmt.Fprintf(&b, "bytes:    %d of %d budget (every pinned entry in full: %d)\n", c.After, c.Budget, c.Before)
	if c.Over {
		b.WriteString("status:   budget_exceeded (nothing was dropped; lore render --report)\n")
	}
	if c.Index {
		b.WriteString("index:    collapsed to one line\n")
	}
	if len(c.Entries) == 0 {
		b.WriteString("moved out of the core: nothing (every pinned entry is in full)\n")
		return b.String()
	}
	fmt.Fprintf(&b, "moved out of the core (%d):\n", len(c.Entries))
	for _, e := range c.Entries {
		fmt.Fprintf(&b, "  %s  %s\n", e.ID, e.Reason)
	}
	b.WriteString("fetch any of them with `lore show <id>`\n")
	return b.String()
}

// Ops returns the why-context operation bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show the last rendered context (what the agent saw); --cut: what the render moved out of the core of LORE.md, and why",
			Permission: op.PermRead, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			Errors:   []errs.Code{errs.NotFound, errs.Unsupported, errs.Internal},
			Examples: []op.Example{{Title: "what the last render cut", CLI: "lore why-context --cut", Want: ""}},
		}, func(ctx context.Context, in ShowIn) (ShowOut, error) {
			r, err := env.LastRender(ctx)
			if err != nil {
				return ShowOut{}, err
			}
			return Show(r, in)
		}),
	}
}

// Show turns one record into the output in's flags ask for.
func Show(r Record, in ShowIn) (ShowOut, error) {
	var ss scopeJSON
	if err := json.Unmarshal([]byte(r.ScopeSummary), &ss); err != nil {
		return ShowOut{}, errs.Wrap(errs.Internal, "the render record's scope summary does not decode", err)
	}
	if in.Cut {
		if ss.Cut == nil {
			return ShowOut{}, errs.New(errs.NotFound, "the last render recorded no cut summary").
				WithHint("run `lore render` to record one (renders before this version kept none)")
		}
		if ss.Cut.Entries == nil {
			ss.Cut.Entries = []Entry{}
		}
		return ShowOut{Data: *ss.Cut, cut: true}, nil
	}
	out := ShowOut{SchemaVersion: SchemaVersion, RenderID: r.ID, CreatedAt: r.CreatedAt, TargetPath: r.TargetPath,
		TotalBytes: r.TotalBytes, SHA256: r.SHA256}
	out.ScopeSummary = Scope{Included: ss.Included}
	if ss.Cut != nil {
		out.ScopeSummary.Cut = *ss.Cut
	}
	if in.Rendered {
		out.RenderedText = r.Text
	}
	return out, nil
}
