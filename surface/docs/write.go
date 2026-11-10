package docs

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Generated blocks inside a committed markdown file. A block is
//
//	<!-- lore:spec:begin commands -->
//	…generated table…
//	<!-- lore:spec:end commands -->
//
// Everything outside the markers is hand-written and left untouched, so the
// generated tables can live inside CLI_REFERENCE.md.
const (
	BlockCommands = "commands"
	BlockErrors   = "errors"

	markerBegin = "<!-- lore:spec:begin "
	markerEnd   = "<!-- lore:spec:end "
	markerClose = " -->"
)

// File kinds Render knows, by extension.
const (
	extJSON     = ".json"
	extMarkdown = ".md"
)

// newFileMode is the mode of a generated file Update creates.
const newFileMode fs.FileMode = 0o644

// newDirMode is the mode of the --out folder Update creates for a new
// generated file (openapi.json into a folder that does not exist yet).
const newDirMode fs.FileMode = 0o750

// RegenerateCommand is what a person runs to refresh stale files; named in
// the CheckStale failure.
const RegenerateCommand = "lore spec docs && lore spec openapi"

// blockContent returns the generated text of a block, ok false for an
// unknown block name.
func blockContent(reg *op.Registry, name string) (string, bool) {
	switch name {
	case BlockCommands:
		return CommandsMarkdown(reg), true
	case BlockErrors:
		return ErrorsMarkdown(), true
	}
	return "", false
}

// Render returns what the file at path should hold given its current bytes:
// a .json file is the whole OpenAPI document of the fixed operations; a .md
// file is current with every lore:spec block replaced by its generated
// table. It fails (E_CONFIG_INVALID) on an unknown extension, a markdown
// file with no block, an unknown block name or a missing end marker.
func Render(reg *op.Registry, path string, current []byte) ([]byte, error) {
	switch filepath.Ext(path) {
	case extJSON:
		return OpenAPI(reg, nil)
	case extMarkdown:
		return renderBlocks(reg, path, string(current))
	}
	return nil, errs.New(errs.ConfigInvalid, "docs: "+path+": only .json and .md files are generated")
}

// renderBlocks replaces every block in src.
func renderBlocks(reg *op.Registry, path, src string) ([]byte, error) {
	var out strings.Builder
	found := 0
	for {
		i := strings.Index(src, markerBegin)
		if i < 0 {
			break
		}
		lineEnd := strings.Index(src[i:], markerClose)
		if lineEnd < 0 {
			return nil, errs.New(errs.ConfigInvalid, "docs: "+path+": unclosed begin marker")
		}
		name := src[i+len(markerBegin) : i+lineEnd]
		content, ok := blockContent(reg, name)
		if !ok {
			return nil, errs.New(errs.ConfigInvalid, "docs: "+path+": unknown block "+strconv.Quote(name))
		}
		endMarker := markerEnd + name + markerClose
		j := strings.Index(src[i:], endMarker)
		if j < 0 {
			return nil, errs.New(errs.ConfigInvalid, "docs: "+path+": block "+name+" has no end marker")
		}
		beginLine := src[i : i+lineEnd+len(markerClose)]
		out.WriteString(src[:i] + beginLine + "\n\n" + content + "\n" + endMarker)
		src = src[i+j+len(endMarker):]
		found++
	}
	if found == 0 {
		return nil, errs.New(errs.ConfigInvalid, "docs: "+path+": no lore:spec block")
	}
	out.WriteString(src)
	return []byte(out.String()), nil
}

// Update rewrites every path with its rendered content (`lore spec docs`,
// `lore spec openapi`). A missing .json file is created; a missing .md file
// is an error, since its hand-written text cannot be generated. A file that
// is already current is not written.
func Update(reg *op.Registry, paths ...string) error {
	for _, p := range paths {
		current, mode, err := readFile(p)
		if err != nil {
			return err
		}
		want, err := Render(reg, p, current)
		if err != nil {
			return err
		}
		if bytes.Equal(current, want) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), newDirMode); err != nil {
			return errs.Wrap(errs.Internal, "docs: create the folder of "+p, err)
		}
		if err := os.WriteFile(p, want, mode); err != nil {
			return errs.Wrap(errs.Internal, "docs: write "+p, err)
		}
	}
	return nil
}

// readFile returns a file's bytes and mode; a missing .json file reads as
// empty with newFileMode.
func readFile(p string) ([]byte, fs.FileMode, error) {
	b, err := os.ReadFile(filepath.Clean(p))
	if errors.Is(err, fs.ErrNotExist) && filepath.Ext(p) == extJSON {
		return nil, newFileMode, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		// a markdown file is only ever updated between its markers: the
		// hand-written page has to exist first
		return nil, 0, errs.Wrap(errs.NotFound, "docs: "+p+" does not exist", err).
			WithHint("run it in the lore repo with the default --out, or pass --out <folder> holding a page with the " + markerBegin + BlockCommands + markerClose + " and " + markerBegin + BlockErrors + markerClose + " blocks")
	}
	if err != nil {
		return nil, 0, errs.Wrap(errs.NotFound, "docs: read "+p, err)
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, 0, errs.Wrap(errs.NotFound, "docs: stat "+p, err)
	}
	return b, st.Mode().Perm(), nil
}

// Drift is one committed file that differs from what the registry writes:
// the first differing line (1-based) and both versions of it.
type Drift struct {
	Path      string
	Line      int
	Committed string
	Generated string
}

// String is the one-line report of d, naming the command that refreshes it.
func (d Drift) String() string {
	return d.Path + " is stale at line " + strconv.Itoa(d.Line) + ":\n  committed: " + d.Committed + "\n  generated: " + d.Generated
}

// Stale returns every path whose committed bytes differ from what Render
// produces from reg (`lore spec docs --check`, the drift test). A file
// that cannot be read or rendered is an error, never "current".
func Stale(reg *op.Registry, paths ...string) ([]Drift, error) {
	var out []Drift
	for _, p := range paths {
		current, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			return out, errs.Wrap(errs.NotFound, "docs: read "+p, err)
		}
		want, err := Render(reg, p, current)
		if err != nil {
			return out, err
		}
		if !bytes.Equal(current, want) {
			line, got, exp := firstDiff(current, want)
			out = append(out, Drift{Path: p, Line: line, Committed: got, Generated: exp})
		}
	}
	return out, nil
}

// CheckError is the refusal of `lore spec docs --check` for drift: one
// E_CONFIG_INVALID (exit non-zero) listing every stale file, with the
// regenerate command as its hint; nil when nothing drifted.
func CheckError(drift []Drift) error {
	if len(drift) == 0 {
		return nil
	}
	lines := make([]string, 0, len(drift))
	for _, d := range drift {
		lines = append(lines, d.String())
	}
	return errs.New(errs.ConfigInvalid, "docs: the committed reference is stale: "+strings.Join(lines, "; ")).
		WithHint("run: " + RegenerateCommand + ", then commit the files")
}

// CheckStale fails t (Errorf, the test goes on) for every path whose
// committed bytes differ from what Render produces from reg, naming the
// first differing line and the command that refreshes it. The drift test
// calls it with the real registry and the committed files, so a changed
// Summary, flag or error code fails CI until they are regenerated (9.6).
func CheckStale(t testing.TB, reg *op.Registry, paths ...string) {
	t.Helper()
	for _, p := range paths {
		drift, err := Stale(reg, p)
		if err != nil {
			t.Errorf("docs: %s: %v", p, err)
			continue
		}
		for _, d := range drift {
			t.Errorf("docs: %s\nrun: %s", d, RegenerateCommand)
		}
	}
}

// firstDiff returns the 1-based number and both versions of the first line
// that differs.
func firstDiff(a, b []byte) (line int, got, want string) {
	al := strings.Split(string(a), "\n")
	bl := strings.Split(string(b), "\n")
	for i := range max(len(al), len(bl)) {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return i + 1, x, y
		}
	}
	return 0, "", ""
}
