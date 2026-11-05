// Package contextop defines `lore context`, the agent's fetch command
// (layer L5, CLI_REFERENCE 2.14, CONTEXT_BUDGET 6.8, 6.9, 6.13): the rules,
// hotfixes and notes for a file, a task, a topic or a prompt, the topic
// index, a coding tool's hook answer, and the internal rows (--internal).
//
//	context.fetch ─▶ exactly one mode (E_INVALID_INPUT otherwise, v0.1.17 wording)
//	              ─▶ --hook: the hook input from hook_input (REST) or stdin (--hook-stdin)
//	              ─▶ Host.Fetch ─▶ Answer: the text (never splits an entry; ends with
//	                 "more: <command>" at the cap) and the --json data
//
// The fetch reads the committed-render sources, the hook's per-session
// seen set and the usage counts of this machine; the binary that owns
// that machinery supplies Host (PASS3 ONE-PATH, P3-MISC-3). The directory
// is ops/context; the package is contextop so it never shadows the
// standard library's context.
package contextop

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore context`, a bare-noun shortcut of fetch).
const (
	Noun    = "context"
	OpFetch = "context.fetch"
	// FetchVerb is the generated verb the bare `lore context` runs.
	FetchVerb = "fetch"
)

// restNoun keeps the REST collection "context" un-pluralised.
const restNoun = "context"

// errNeedsMode is the v0.1.17 message when no mode is given.
const errNeedsMode = "pass --for-file <path>, --for-task <id>, --topic <name>, --topics, --for-prompt <text>, --hook <tool> --hook-stdin, or --internal"

// FetchIn is the input of context.fetch (the v0.1.17 flags).
type FetchIn struct {
	ForFile      string `json:"for_file,omitempty" flag:"for-file" desc:"rules, hotfixes and notes for this path"`
	ForTask      string `json:"for_task,omitempty" flag:"for-task" desc:"the task, its plan and links, then what applies to the files it names"`
	Topic        string `json:"topic,omitempty" flag:"topic" desc:"every row tagged with this topic"`
	Topics       bool   `json:"topics,omitempty" flag:"topics" desc:"list the topics with their counts"`
	ForPrompt    string `json:"for_prompt,omitempty" flag:"for-prompt" desc:"rows matching this free text"`
	Hook         string `json:"hook,omitempty" flag:"hook" desc:"answer a tool hook (claude_code, codex, gemini_cli)"`
	HookStdin    bool   `json:"hook_stdin,omitempty" flag:"hook-stdin" desc:"with --hook: read the hook input JSON from stdin"`
	SessionStart bool   `json:"session_start,omitempty" flag:"session-start" desc:"with --hook: answer SessionStart (in-flight tasks, rows changed since your last session)"`
	MaxBytes     int    `json:"max_bytes,omitempty" flag:"max-bytes" desc:"cap the output in bytes (default 16000; hooks: context.hook_cap)"`
	Meta         bool   `json:"meta,omitempty" flag:"meta" desc:"also print each returned row's metadata (sensitive values masked)"`
	Internal     bool   `json:"internal,omitempty" flag:"internal" desc:"internal, global and personal rows (outside this checkout)"`
	Table        string `json:"table,omitempty" flag:"table" desc:"with --internal: only this table (rules, memories, decisions, tasks, …)"`
	Search       string `json:"search,omitempty" flag:"search" desc:"with --internal: only rows whose title or body contains this text"`
	// HookInput is the hook's input JSON given in the request (REST, where
	// there is no stdin to read); no CLI flag: the CLI reads stdin.
	HookInput string `json:"hook_input,omitempty"`
}

// Answer is what Host.Fetch returns.
type Answer struct {
	// Text is the human answer (a hook's answer in the tool's format;
	// empty when a hook had nothing to say or failed: a hook never fails
	// the tool, 6.9).
	Text string
	// Data is the --json "data" value, encoded (nil for a hook).
	Data []byte
}

// Host is the machinery behind lore context, supplied by the binary.
// Invariant: a --hook fetch returns no error (it logs and answers empty).
type Host interface {
	Fetch(ctx context.Context, in FetchIn, hookInput io.Reader) (Answer, error)
}

// Env is what lore context needs; *wire.App implements it.
type Env interface {
	// ContextHost is the binary's Host (E_UNSUPPORTED when it has none).
	ContextHost(ctx context.Context) (Host, error)
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

// FetchOut is the output of context.fetch: under --json {"data": …} as in
// v0.1.17 (picks with id, kind, why, bytes and, with --meta, meta; the
// topic list; or the internal rows), otherwise the text.
type FetchOut struct {
	Data RawJSON `json:"data,omitempty"`
	text string
}

// Text implements op.Texter.
func (o FetchOut) Text() string { return o.text }

// Ops returns the context operation bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpFetch, Summary: "Print the rules, hotfixes and notes for a file, task, topic or prompt (the agent's fetch command)",
			Permission: op.PermRead, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			Errors: []errs.Code{errs.InvalidInput, errs.NotFound, errs.ConfigInvalid, errs.Unsupported, errs.Internal},
			Examples: []op.Example{
				{Title: "what applies to a file", CLI: "lore context --for-file services/billing/invoice.go", Want: ""},
				{Title: "the topic index", CLI: "lore context --topics", Want: ""},
			},
		}, func(ctx context.Context, in FetchIn) (FetchOut, error) {
			h, err := env.ContextHost(ctx)
			if err != nil {
				return FetchOut{}, err
			}
			return Fetch(ctx, h, in, os.Stdin)
		}),
	}
}

// Fetch is context.fetch over h; stdin is read only for --hook-stdin
// without hook_input.
func Fetch(ctx context.Context, h Host, in FetchIn, stdin io.Reader) (FetchOut, error) {
	if !hasMode(in) {
		return FetchOut{}, errs.New(errs.InvalidInput, errNeedsMode)
	}
	var hookIn io.Reader = strings.NewReader("")
	switch {
	case in.HookInput != "":
		hookIn, in.HookStdin = strings.NewReader(in.HookInput), true
	case in.Hook != "" && in.HookStdin:
		hookIn = stdin
	}
	a, err := h.Fetch(ctx, in, hookIn)
	if err != nil {
		return FetchOut{}, err
	}
	return FetchOut{Data: a.Data, text: a.Text}, nil
}

// hasMode reports whether in names one of the fetch modes.
func hasMode(in FetchIn) bool {
	return in.Internal || in.Hook != "" || in.Topics || in.ForFile != "" || in.ForTask != "" || in.Topic != "" || in.ForPrompt != ""
}
