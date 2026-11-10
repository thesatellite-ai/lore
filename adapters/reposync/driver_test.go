package reposync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestDriverValue: which config values are lore drivers, which of those
// can run, over the program forms lore writes (bare, quoted absolute) and
// the ones it must not mistake for its own (another command, a quoted path
// with no merge-driver, an unclosed quote, empty).
func TestDriverValue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	present := filepath.Join(dir, "lore")
	if err := os.WriteFile(present, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(present, 0o700); err != nil {
		t.Fatal(err)
	}
	notExec := filepath.Join(dir, "plain")
	if err := os.WriteFile(notExec, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "gone", "lore")
	q := func(p string) string { return "'" + filepath.ToSlash(p) + "'" }
	for _, tc := range []struct {
		name, value       string
		isLore, gone, run bool
		prog              string
	}{
		{"bare", "lore merge-driver %O %A %B %P", true, false, true, "lore"},
		{"bare keep-ours", "lore merge-driver --keep-ours %O %A %B %P", true, false, true, "lore"},
		{"bare other name", "lore-dev merge-driver %O %A %B %P", true, false, true, "lore-dev"},
		{"abs present", q(present) + " merge-driver %O %A %B %P", true, false, true, filepath.ToSlash(present)},
		{"abs missing", q(missing) + " merge-driver %O %A %B %P", true, true, false, filepath.ToSlash(missing)},
		{"abs not executable", q(notExec) + " merge-driver %O %A %B %P", true, true, false, filepath.ToSlash(notExec)},
		{"padded", "  lore merge-driver %O %A %B %P\n", true, false, true, "lore"},
		{"foreign", "cat %A", false, false, false, ""},
		{"quoted foreign", q(present) + " %O %A %B", false, false, false, ""},
		{"unclosed quote", "'/x/lore merge-driver %O", false, false, false, ""},
		{"empty", "", false, false, false, ""},
		{"program only", "lore", false, false, false, ""},
	} {
		prog, ok := DriverProgram(tc.value)
		if ok != tc.isLore || prog != tc.prog {
			t.Errorf("%s: DriverProgram = %q, %v; want %q, %v", tc.name, prog, ok, tc.prog, tc.isLore)
		}
		if got := DriverMissing(tc.value); got != tc.gone {
			t.Errorf("%s: DriverMissing = %v, want %v", tc.name, got, tc.gone)
		}
		if got := DriverRuns(tc.value); got != tc.run {
			t.Errorf("%s: DriverRuns = %v, want %v", tc.name, got, tc.run)
		}
	}
}

// TestDriverBinary: bare `lore` only when the lore on PATH is the running
// file itself (also through a symlink), else the quoted absolute path; a
// path holding a quote or no executable falls back to the bare name.
func TestDriverBinary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exe := filepath.Join(dir, "lore")
	other := filepath.Join(dir, "other-lore")
	for _, p := range []string{exe, other} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "bin-lore")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	realExe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	quoted := "'" + filepath.ToSlash(realExe) + "'"
	found := func(p string) func(string) (string, error) {
		return func(string) (string, error) { return p, nil }
	}
	notFound := func(string) (string, error) { return "", errors.New("not found") }
	for _, tc := range []struct {
		name, exe string
		look      func(string) (string, error)
		want      string
	}{
		{"same file on PATH", exe, found(exe), BinaryName},
		{"PATH entry is a symlink to it", exe, found(link), BinaryName},
		{"running through a symlink", link, found(exe), BinaryName},
		{"another lore on PATH", exe, found(other), quoted},
		{"no lore on PATH", exe, notFound, quoted},
		{"PATH entry is gone", exe, found(filepath.Join(dir, "gone")), quoted},
		{"no executable", "", notFound, BinaryName},
		{"quote in path", filepath.Join(dir, "it's", "lore"), notFound, BinaryName},
	} {
		if got := DriverBinary(tc.exe, tc.look); got != tc.want {
			t.Errorf("%s: DriverBinary = %q, want %q", tc.name, got, tc.want)
		}
	}
}
