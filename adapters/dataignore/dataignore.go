// Package dataignore answers "does git ignore lore's row files here?" for
// every caller that needs it: the git wiring's re-include step and `lore
// sync`'s refusal (wire/host/dataignore.go) and the [sync] section of `lore
// doctor` (wire/hostbase_doctor_sync.go). It sits below both (wire/host
// imports wire, so neither can hold it for the other).
//
// The answer comes from lore's own files only: one probe path per kind of
// file lore commits under .lore/data, asked of `git check-ignore`. It is
// never "git lists some ignored entry under .lore/data": operating systems
// drop clutter there (.DS_Store, ._name, Thumbs.db) and a global
// core.excludesFile commonly ignores it, which says nothing about lore data
// (the false "gitignored" doctor warning found on macOS).
package dataignore

import (
	"bytes"
	"context"
	"path"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// DataRel is the committed data folder, relative to the project root.
const DataRel = rowfs.LoreDir + "/" + rowfs.DataDir

// OSJunkPatterns are the operating-system clutter names (rowfs.OSJunkPatterns,
// the one list). The git wiring writes them, in this order, as ignore lines
// after its re-include block, so changing the list changes that block.
var OSJunkPatterns = rowfs.OSJunkPatterns

// IsOSJunk reports whether a file's base name is operating-system clutter (rowfs.IsOSJunk).
func IsOSJunk(name string) bool { return rowfs.IsOSJunk(name) }

// IsRowFile reports whether rel (a slash path) has the shape of a file lore
// commits under .lore/data: a .json file that is not OS clutter (an
// AppleDouble "._x.json" is clutter).
func IsRowFile(rel string) bool {
	return strings.HasSuffix(rel, rowfs.RowFileExt) && !IsOSJunk(rel)
}

// checkIgnoreNone is `git check-ignore`'s exit status when no path given
// is ignored (0 = some are, 128 = a fatal error).
const checkIgnoreNone = 1

// probeStem is the file name stem used inside table folders: not a valid
// row id, so a stray file by that name is skipped, never read.
const probeStem = "sample"

// Probes are the paths, relative to the project root, of one file of every
// kind lore commits under .lore/data.
func Probes() []string {
	out := []string{path.Join(DataRel, rowfs.MetaFileName), path.Join(DataRel, rowfs.PurgedFileName)}
	for _, t := range schema.Builtin() {
		if !t.Synced || t.MachineLocal {
			continue
		}
		out = append(out, path.Join(DataRel, t.Name.String(), probeStem+rowfs.RowFileExt))
	}
	return out
}

// Shown is how a report names an ignored probe path: a probe row file
// (made-up name) as its table's files ("<dir>/*.json"), any other path as
// it is.
func Shown(p string) string {
	if path.Base(p) == probeStem+rowfs.RowFileExt {
		return path.Dir(p) + "/*" + rowfs.RowFileExt
	}
	return p
}

// Ignored maps each path git ignores in root to the rule responsible
// ("<source>:<line>:<pattern>"). paths are relative to root; nil asks the
// Probes. A path matched only by a negation is not ignored.
func Ignored(ctx context.Context, root string, paths []string) (map[string]string, error) {
	if paths == nil {
		paths = Probes()
	}
	if len(paths) == 0 {
		return nil, nil
	}
	out, err := (gitref.Exec{}).Run(ctx, gitref.Invocation{Dir: root,
		Args:  []string{"check-ignore", "-v", "--no-index", "--stdin", "-z"},
		Stdin: []byte(strings.Join(paths, "\x00") + "\x00")})
	if code, ok := gitref.ExitCode(err); ok && code == checkIgnoreNone {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git check-ignore", err)
	}
	// -z -v: <source> NUL <linenum> NUL <pattern> NUL <pathname> NUL ...
	f := bytes.Split(out, []byte{0})
	res := map[string]string{}
	for i := 0; i+3 < len(f); i += 4 {
		pattern := string(f[i+2])
		if strings.HasPrefix(pattern, "!") {
			continue // matched by a negation: not ignored
		}
		res[string(f[i+3])] = string(f[i]) + ":" + string(f[i+1]) + ":" + pattern
	}
	return res, nil
}
