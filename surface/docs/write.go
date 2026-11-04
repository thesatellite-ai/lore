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
	if err != nil {
		return nil, 0, errs.Wrap(errs.NotFound, "docs: read "+p, err)
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, 0, errs.Wrap(errs.NotFound, "docs: stat "+p, err)
	}
	return b, st.Mode().Perm(), nil
}

// CheckStale fails t (Errorf, the test goes on) for every path whose
// committed bytes differ from what Render produces from reg, naming the
// first differing line and the command that refreshes it. Phase 2 calls it
// from a test with the real registry and the real CLI_REFERENCE.md and
// openapi.json, so a changed Summary, flag or error code fails CI until the
// files are regenerated (spec 9.6).
func CheckStale(t testing.TB, reg *op.Registry, paths ...string) {
	t.Helper()
	for _, p := range paths {
		current, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			t.Errorf("docs: %s: %v", p, err)
			continue
		}
		want, err := Render(reg, p, current)
		if err != nil {
			t.Errorf("docs: %s: %v", p, err)
			continue
		}
		if !bytes.Equal(current, want) {
			line, got, exp := firstDiff(current, want)
			t.Errorf("docs: %s is stale at line %d:\n  committed: %s\n  generated: %s\nrun: %s", p, line, got, exp, RegenerateCommand)
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
