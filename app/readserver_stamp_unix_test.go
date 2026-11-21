//go:build linux || darwin

package app

// readserver_stamp_unix_test.go — devco:D-864 (bug hunt H-130, public
// issue #94): the read server answered no read of a released lore. The
// client built its binary stamp before Main applied WithVersion (it sent
// "dev") while the server applied it first, so every call was declined as
// another lore binary and ran one-shot.

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestMainStampsTheReleaseVersion: a lore whose main passes WithVersion
// (every release) asks its read server with a stamp that ends in that
// version, the stamp its server (which applies the same options) holds.
// A fake server on the socket reads the request and declines it, so the
// command then runs one-shot; only the stamp it sent is checked.
func TestMainStampsTheReleaseVersion(t *testing.T) {
	w := newCLIWorld(t)
	loreHome, err := os.MkdirTemp("", "lh-") // short: a socket path holds ~100 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(loreHome) })
	dir, sock := readServerPaths(loreHome)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln := listenFake(t, dir, sock)
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan string, 1)
	go func() { got <- fakeServerStamp(ln) }()

	const release = "1.2.3"
	w.setAll(map[string]string{"LORE_HOME": loreHome, envReadServer: "1", envCLIChildVersion: release})
	w.lore(t, w.root, "search", "--query", "deploy") // one-shot after the decline; its answer does not matter

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := fileStamp(exe, release)
	select {
	case stamp := <-got:
		if stamp != want {
			t.Errorf("a lore built with version %s asked its read server with stamp %q, want %q (its server holds that one and declines every other)", release, stamp, want)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the lore child never asked the read server")
	}
}

// TestFileStampNamesTheFileNotThePath: a lore reached through a symlink
// (a package manager's bin link) or a hard link is the same binary as its
// real path; a copy, or the same file reporting another version, is not.
func TestFileStampNamesTheFileNotThePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	orig := filepath.Join(dir, "cellar", "lore")
	if err := os.MkdirAll(filepath.Dir(orig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orig, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "bin", "lore")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(orig, link); err != nil {
		t.Fatal(err)
	}
	hard := filepath.Join(dir, "lore-hard")
	if err := os.Link(orig, hard); err != nil {
		t.Fatal(err)
	}
	cp := filepath.Join(dir, "lore-copy")
	if err := os.WriteFile(cp, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-time.Hour)
	for _, p := range []string{orig, cp} { // the copy matches size and mtime: only the file differs
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	base := fileStamp(orig, "0.1.0")
	if base == "" {
		t.Fatal("no stamp for a readable file")
	}
	for _, p := range []string{link, hard} {
		if s := fileStamp(p, "0.1.0"); s != base {
			t.Errorf("the same binary through %s stamps %q, through the file's own path %q", p, s, base)
		}
	}
	if s := fileStamp(cp, "0.1.0"); s == base {
		t.Errorf("a copy stamps like the original (%q): a different binary would be answered", s)
	}
	if s := fileStamp(orig, "0.1.1"); s == base {
		t.Errorf("the same file reporting another version stamps alike (%q)", s)
	}
	if s := fileStamp(filepath.Join(dir, "gone"), "0.1.0"); s != "" {
		t.Errorf("a missing binary stamps %q, want \"\"", s)
	}
}

// listenFake listens on the read server socket, by its bare name from
// inside dir when the path is too long for a socket address (the test's
// working directory is put back at once).
func listenFake(t *testing.T, dir, sock string) *net.UnixListener {
	t.Helper()
	name := sock
	if len(sock) > sockPathMax {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(wd) }()
		name = readServerSock
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: name, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// fakeServerStamp accepts one call, closes the stdio it was handed,
// declines it and returns the stamp the request carried ("" on a broken
// request).
func fakeServerStamp(ln *net.UnixListener) string {
	conn, err := ln.AcceptUnix()
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	buf := make([]byte, 1<<20)
	oob := make([]byte, unix.CmsgSpace(3*4))
	n, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return ""
	}
	if msgs, err := unix.ParseSocketControlMessage(oob[:oobn]); err == nil {
		for _, m := range msgs {
			fds, _ := unix.ParseUnixRights(&m)
			for _, fd := range fds {
				_ = unix.Close(fd)
			}
		}
	}
	body := buf[:n]
	if len(body) < 4 {
		return ""
	}
	size := int(binary.BigEndian.Uint32(body))
	body = body[4:]
	for len(body) < size {
		more := make([]byte, size-len(body))
		m, err := io.ReadFull(conn, more)
		body = append(body, more[:m]...)
		if err != nil {
			break
		}
	}
	var req readRequest
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	line, _ := json.Marshal(readReply{Why: "a test server"})
	_, _ = conn.Write(append(line, '\n'))
	return strings.TrimSpace(req.Stamp)
}
