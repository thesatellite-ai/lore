package app

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerUID is the uid of the process on the other end of conn (SO_PEERCRED);
// -1 when it cannot be read, which no user has.
func peerUID(conn *net.UnixConn) int {
	raw, err := conn.SyscallConn()
	if err != nil {
		return -1
	}
	uid := -1
	_ = raw.Control(func(fd uintptr) {
		if cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil {
			uid = int(cred.Uid)
		}
	})
	return uid
}
