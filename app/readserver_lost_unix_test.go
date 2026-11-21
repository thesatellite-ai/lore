//go:build linux || darwin

package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// TestAskReadServerNeverRerunsAfterRunning (devco:D-838): a read server
// lost before it says Running printed nothing, so the CLI runs the
// command itself (errNotServed); one lost after Running may have printed
// the start of the answer, so the CLI must not run it again
// (errServerLost: exit 1 with readServerLostMsg), or that start shows
// twice.
func TestAskReadServerNeverRerunsAfterRunning(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replies []readReply
		want    error
	}{
		{"lost before running", []readReply{{Accepted: true}}, errNotServed},
		{"lost while running", []readReply{{Accepted: true}, {Running: true}}, errServerLost},
		{"declined", []readReply{{Why: "not a read"}}, errNotServed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := unixPair(t)
			done := make(chan struct{})
			go func() { // a server that sends its replies, reads the go, and dies
				defer close(done)
				defer func() { _ = server.Close() }()
				req, fds, err := readRequestFrom(server)
				closeFDs(fds)
				if err != nil || req.V != readServerProto {
					return
				}
				r := bufio.NewReader(server)
				for i, rep := range tc.replies {
					b, _ := json.Marshal(rep)
					if _, err := server.Write(append(b, '\n')); err != nil {
						return
					}
					if i == 0 && rep.Accepted {
						if _, err := r.ReadByte(); err != nil { // the CLI's go
							return
						}
					}
				}
			}()
			_, err := askReadServer(client, []string{"ask", "--query", "x"})
			<-done
			if !errors.Is(err, tc.want) {
				t.Fatalf("askReadServer = %v, want %v", err, tc.want)
			}
		})
	}
}

// unixPair is two connected unix stream sockets.
func unixPair(t *testing.T) (client, server *net.UnixConn) {
	t.Helper()
	fds, err := unixSocketpair()
	if err != nil {
		t.Fatal(err)
	}
	conns := make([]*net.UnixConn, 2)
	for i, fd := range fds {
		f := os.NewFile(uintptr(fd), "pair")
		c, err := net.FileConn(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		conns[i] = c.(*net.UnixConn)
		t.Cleanup(func() { _ = c.Close() })
	}
	return conns[0], conns[1]
}

// unixSocketpair is socketpair(2) for a unix stream.
func unixSocketpair() ([2]int, error) {
	return unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
}
