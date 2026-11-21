//go:build linux || darwin

package app

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/thesatellite-ai/lore/adapters/procwalk"
)

// TestSwapProcessRunsAsTheClient (devco:D-833): while the read server runs
// a command, the process has the client's working directory, environment,
// arguments, parent (agent detection) and stdin/stdout/stderr; afterwards
// every one is the server's again. Not parallel: it swaps process state.
func TestSwapProcessRunsAsTheClient(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir("/"); err != nil { // the server's own folder
		t.Fatal(err)
	}
	var reads [3]*os.File
	var fds []int
	for i := range 3 {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 { // the client's stdin: the server reads what the client's writer sent
			_, _ = w.WriteString("in")
			_ = w.Close()
			fds = append(fds, dupFD(t, r))
			_ = r.Close()
			continue
		}
		reads[i] = r
		fds = append(fds, dupFD(t, w))
		_ = w.Close()
	}
	env, args, parent := os.Environ(), os.Args, procwalk.Parent()
	req := readRequest{Dir: dir, Env: []string{"HOME=" + dir, "ZZ_CLIENT=1"}, Args: []string{"ask", "--query", "x"}, Parent: 4242}
	restore, err := swapProcess(req, fds)
	if err != nil {
		restore()
		t.Fatal(err)
	}
	got, _ := os.Getwd()
	in, _ := io.ReadAll(os.Stdin)
	_, _ = os.Stdout.WriteString("out")
	_, _ = os.Stderr.WriteString("err")
	seen := []string{got, os.Getenv("ZZ_CLIENT"), os.Getenv("HOME"), strings.Join(os.Args[1:], " "), string(in)}
	seenParent := procwalk.Parent()
	restore()
	for _, fd := range fds {
		_ = os.NewFile(uintptr(fd), "").Close()
	}
	if real, _ := filepath.EvalSymlinks(dir); seen[0] != dir && seen[0] != real {
		t.Errorf("working directory during the command = %q, want %q", seen[0], dir)
	}
	for i, want := range []string{"", "1", dir, "ask --query x", "in"} {
		if i > 0 && seen[i] != want {
			t.Errorf("during the command: got %q, want %q", seen[i], want)
		}
	}
	if seenParent != 4242 {
		t.Errorf("agent detection walked from %d, want the client's parent 4242", seenParent)
	}
	for i, want := range []string{"", "out", "err"} {
		if reads[i] == nil {
			continue
		}
		b, _ := io.ReadAll(reads[i])
		if string(b) != want {
			t.Errorf("fd %d: the client got %q, want %q", i, b, want)
		}
	}
	if now, _ := os.Getwd(); now != "/" || os.Getenv("ZZ_CLIENT") != "" || len(os.Environ()) != len(env) || strings.Join(os.Args, " ") != strings.Join(args, " ") || procwalk.Parent() != parent {
		t.Errorf("not restored: wd %q, ZZ_CLIENT %q, %d env vars (had %d), args %q, parent %d (had %d)", now, os.Getenv("ZZ_CLIENT"), len(os.Environ()), len(env), os.Args, procwalk.Parent(), parent)
	}
}

// dupFD is a new descriptor of f, owned by the caller.
func dupFD(t *testing.T, f *os.File) int {
	t.Helper()
	fd, err := unix.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	return fd
}

// TestReadServerLongHomePath (devco:D-833): a lore home deep enough that
// its socket path does not fit a socket address is still served: the
// server listens and the CLI dials by the bare name inside the run folder.
func TestReadServerLongHomePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("deep-folder-", 10), "run")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, readServerSock)
	if len(sock) <= sockPathMax {
		t.Fatalf("test path %d bytes is not long enough", len(sock))
	}
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	ln, err := listenReadServer(dir, sock)
	if err != nil {
		t.Fatalf("listen on a long path: %v", err)
	}
	ln.SetUnlinkOnClose(false)
	defer func() { _ = ln.Close() }()
	_ = os.Chdir(wd)
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close()
		}
		done <- err
	}()
	c, err := dialReadServer(dir, sock)
	if err != nil {
		t.Fatalf("dial a long path: %v", err)
	}
	_ = c.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if now, _ := os.Getwd(); now != wd {
		t.Errorf("the dial left the working directory at %q, want %q", now, wd)
	}
}
