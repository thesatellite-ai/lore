package wire

// compiledstaged.go — what the commit check says about a compiled program
// in the commit (mac:firstcommit-staged-binary: a first commit in a new
// repo staged the Go binary `go build` had just written, and lore said
// nothing). Committing it is not refused, since some repos ship binaries
// on purpose and the file was still checked for secrets like any other;
// the check names each compiled program the commit adds, once, with the
// way to keep build output out of git.
//
//	staged files (stagedFiles) ─▶ new in this commit (not at HEAD)
//	  ─▶ starts with an executable's magic bytes (ELF, Mach-O, PE, wasm)
//	  ─▶ one note: "this commit adds compiled programs: app (ELF, 1.8 MB) …"

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// Executable formats compiledKind names.
const (
	compiledELF   = "ELF"
	compiledMachO = "Mach-O"
	compiledPE    = "Windows PE"
	compiledWasm  = "WebAssembly"
)

// compiledKind is the executable format b starts with ("" when none). A
// "MZ" file counts as PE only with its "PE\0\0" header where the DOS
// header points, so a text file that starts with "MZ" is not one.
func compiledKind(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x7fELF")):
		return compiledELF
	case bytes.HasPrefix(b, []byte("\x00asm")):
		return compiledWasm
	case len(b) >= 4:
		switch binary.BigEndian.Uint32(b[:4]) {
		case 0xfeedface, 0xfeedfacf, 0xcefaedfe, 0xcffaedfe:
			return compiledMachO
		}
	}
	if len(b) >= 0x40 && bytes.HasPrefix(b, []byte("MZ")) {
		off := int(binary.LittleEndian.Uint32(b[0x3c:0x40]))
		if off > 0 && off+4 <= len(b) && bytes.Equal(b[off:off+4], []byte("PE\x00\x00")) {
			return compiledPE
		}
	}
	return ""
}

// compiledProgramsLine is the note for the compiled programs among docs
// (staged path → content) that HEAD of the repo at top does not hold yet
// ("" when none): a binary already committed is the team's choice and is
// not named again on every commit.
func compiledProgramsLine(ctx context.Context, top string, docs map[string][]byte) string {
	var found []string
	for p, b := range docs {
		if p == checkWhereCommitMsg {
			continue
		}
		kind := compiledKind(b)
		if kind == "" || atHEAD(ctx, top, p) {
			continue
		}
		found = append(found, fmt.Sprintf("%s (%s, %s)", p, kind, sizeText(len(b))))
	}
	if len(found) == 0 {
		return ""
	}
	sort.Strings(found)
	paths := make([]string, 0, len(found))
	for _, f := range found {
		paths = append(paths, f[:strings.LastIndex(f, " (")])
	}
	return "this commit adds compiled programs: " + strings.Join(found, ", ") +
		"; they were checked for secrets and are committed; if they are build output, keep them out of git: add them to .gitignore, then `git rm --cached -- " + strings.Join(paths, " ") + "` and `git commit --amend`"
}

// atHEAD reports whether HEAD of the repo at top holds path (false before
// the first commit).
func atHEAD(ctx context.Context, top, path string) bool {
	_, err := gitRun{dir: top, args: []string{"cat-file", "-e", "HEAD:" + path}}.output(ctx)
	return err == nil
}

// sizeText is n bytes for a person: "1.8 MB", "640 KB", "12 bytes".
func sizeText(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}
