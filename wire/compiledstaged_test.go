package wire

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompiledKind: the executable formats are told by their magic bytes;
// a text file starting with "MZ" or "ELF" is not one.
func TestCompiledKind(t *testing.T) {
	t.Parallel()
	pe := make([]byte, 0x80)
	copy(pe, "MZ")
	pe[0x3c] = 0x40
	copy(pe[0x40:], "PE\x00\x00")
	for _, c := range []struct {
		name string
		b    []byte
		want string
	}{
		{"elf", []byte("\x7fELF\x02\x01\x01"), compiledELF},
		{"mach-o 64", []byte{0xcf, 0xfa, 0xed, 0xfe, 7, 0, 0, 1}, compiledMachO},
		{"mach-o 32 big", []byte{0xfe, 0xed, 0xfa, 0xce, 0}, compiledMachO},
		{"pe", pe, compiledPE},
		{"wasm", []byte("\x00asm\x01\x00\x00\x00"), compiledWasm},
		{"text MZ", []byte("MZ is a text file that is long enough to have a PE offset field, but no PE header"), ""},
		{"text ELF", []byte("ELF is not \\x7fELF"), ""},
		{"empty", nil, ""},
	} {
		if got := compiledKind(c.b); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestCompiledProgramsLine (mac:firstcommit-staged-binary): a compiled
// program a commit adds is named with its format and size and the way to
// keep build output out of git; one HEAD already holds is not named
// again; source files and the commit message never are.
func TestCompiledProgramsLine(t *testing.T) {
	t.Parallel()
	top := gitRepo(t)
	elf := []byte("\x7fELF" + strings.Repeat("x", 2<<20))
	docs := map[string][]byte{"bin/app": elf, "main.go": []byte("package main\n"), checkWhereCommitMsg: []byte("\x7fELF in a message")}
	got := compiledProgramsLine(background(), top, docs)
	if !strings.HasPrefix(got, "this commit adds compiled programs: bin/app (ELF, 2.0 MB);") || !strings.Contains(got, "`git rm --cached -- bin/app` and `git commit --amend`") || strings.Contains(got, "main.go") {
		t.Fatalf("first commit: %q", got)
	}
	if err := os.MkdirAll(filepath.Join(top, "bin"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(top, "bin", "app"), elf, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "bin/app"}, {"-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", "app"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = top
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if got := compiledProgramsLine(background(), top, docs); got != "" {
		t.Fatalf("a binary HEAD holds is named again: %q", got)
	}
}
