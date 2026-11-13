package directive

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names.
const (
	OpInstall = "directive.install"
	OpShow    = "directive.show"
)

// File statuses of FileResult, one per v0.1.17 output line.
const (
	StatusWritten   = "written"   // ✓ <path>
	StatusUnchanged = "unchanged" // = <path> (no change)
)

// blockRegex matches an existing block (across newlines, non-greedy).
var blockRegex = toolprofile.MarkerRegexp(StartMarker, EndMarker)

// Target is one tool file the directive goes into.
type Target struct {
	// File is the file as given (absolute, or a slash path relative to the
	// repo root).
	File    string
	Profile toolprofile.Profile
}

// Pick is what Manager.DirectiveTargets resolved: the files, and the lines
// that say how they were chosen (v0.1.17 printed them before the files).
type Pick struct {
	Targets []Target
	Notes   []string
}

// Edit maps a file's current text (exists false: the file is missing) to
// its new text; write false leaves the file untouched.
type Edit func(existing string, exists bool) (out string, write bool)

// Manager does the file work; wire implements it.
type Manager interface {
	// DirectiveTargets turns --target values (profile names, a known
	// profile's pointer file, any other file) into tool files; with none,
	// the repo's own targets: its context.targets settings row, else the
	// pointer-file detection (toolprofile.Resolve), as `lore render`.
	DirectiveTargets(ctx context.Context, values []string) (Pick, error)
	// EditRepoFile reads file (absolute or repo-relative), applies edit and
	// writes the result when edit asks to (creating parent folders; an
	// existing file keeps its mode). It returns the absolute path.
	EditRepoFile(ctx context.Context, file string, edit Edit) (string, error)
}

// InstallIn is the input of directive.install.
type InstallIn struct {
	Targets []string `json:"targets,omitempty" flag:"target" desc:"tool profile(s) or file(s) — repeatable or comma-separated (default: the repo's tool files, else AGENTS.md)"`
	// CONTEXT_BUDGET 6.5: the compact form by default.
	Full bool `json:"full,omitempty" flag:"full" desc:"write the full directive instead of the compact one"`
}

// ShowIn is the input of directive.show.
type ShowIn struct {
	Full bool `json:"full,omitempty" flag:"full" desc:"print the full directive"`
}

// FileResult is what happened to one file.
type FileResult struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// FilesOut is the output of directive.install.
type FilesOut struct {
	// Notes say how the targets were chosen.
	Notes []string     `json:"notes,omitempty"`
	Files []FileResult `json:"files"`
}

// Text implements op.Texter with the v0.1.17 lines.
func (o FilesOut) Text() string {
	var b strings.Builder
	for _, n := range o.Notes {
		b.WriteString(n + "\n")
	}
	for _, f := range o.Files {
		switch f.Status {
		case StatusWritten:
			b.WriteString("✓ " + f.Path + "\n")
		case StatusUnchanged:
			b.WriteString("= " + f.Path + " (no change)\n")
		}
	}
	return b.String()
}

// ShowOut is the output of directive.show.
type ShowOut struct {
	Block string `json:"block"`
}

// Text implements op.Texter: the block as is.
func (o ShowOut) Text() string { return o.Block }

// Block returns the fenced block (markers, body, trailing newline): the
// full directive when full, else the compact one.
func Block(full bool) string {
	body := compactBody
	if full {
		body = fullBody
	}
	return StartMarker + "\n" + body + "\n" + EndMarker + "\n"
}

// Install places block in existing: an existing block is replaced in place
// (literally: the block's shell examples contain "$RID", which a regexp
// replace would expand), else it goes at the top with a blank line, after
// a Cursor rule file's frontmatter, which must stay first.
func Install(p toolprofile.Profile, existing, block string) string {
	return p.Place(existing, block, blockRegex)
}

// splitValues splits repeatable, comma-separated --target values.
func splitValues(values []string) []string {
	var out []string
	for _, v := range values {
		for s := range strings.SplitSeq(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// errorCodes are the codes the directive operations return.
var errorCodes = []errs.Code{errs.InvalidInput, errs.ConfigInvalid, errs.BadPath, errs.NotAStore, errs.Internal}

// Ops returns the directive operations bound to m.
func Ops(m Manager) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpInstall, Summary: "Install (or refresh) the directive block at the top of one or more files",
			Permission: op.PermWrite, Access: op.AccessRead, Errors: errorCodes, Local: true,
			Examples: []op.Example{
				{Title: "into the repo's tool files", CLI: "lore directive install", Want: "✓"},
				{Title: "the full form into one file", CLI: "lore directive install --target AGENTS.md --full", Want: "AGENTS.md"},
			},
		}, func(ctx context.Context, in InstallIn) (FilesOut, error) {
			pick, err := m.DirectiveTargets(ctx, splitValues(in.Targets))
			if err != nil {
				return FilesOut{}, err
			}
			out := FilesOut{Notes: pick.Notes, Files: []FileResult{}}
			block := Block(in.Full)
			for _, t := range pick.Targets {
				if !t.Profile.HasPointerFile() {
					continue // generic: no file to write into
				}
				status := StatusWritten
				abs, err := m.EditRepoFile(ctx, t.File, func(existing string, _ bool) (string, bool) {
					next := Install(t.Profile, existing, block)
					if note := toolprofile.StrayNote(t.File, next, StartMarker, EndMarker); note != "" {
						out.Notes = append(out.Notes, "⚠ "+note)
					}
					if next == existing {
						status = StatusUnchanged
						return existing, false
					}
					return next, true
				})
				if err != nil {
					return out, err
				}
				out.Files = append(out.Files, FileResult{Path: abs, Status: status})
			}
			return out, nil
		}),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Print the directive block (compact; --full for the long form) without writing any file",
			Permission: op.PermRead, Access: op.AccessRead, Errors: errorCodes, Local: true,
			Examples: []op.Example{
				{Title: "the compact block", CLI: "lore directive show", Want: StartMarker},
			},
		}, func(_ context.Context, in ShowIn) (ShowOut, error) {
			return ShowOut{Block: Block(in.Full)}, nil
		}),
	}
}
