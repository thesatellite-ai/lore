//go:build linux || darwin

package app

// readserver_unix.go — the read server and the CLI's side of it
// (devco:D-833; the flow is in readserver.go).
//
// Security: the socket lives in <lore home>/run (0700, owned by this
// user) and is 0600; the server checks the peer's uid on every connection
// (peerUID) and answers only a client of the same lore binary (the stamp:
// path, size, mtime, inode and version). It runs only one operation that
// only reads (cli.ReadCall), never a write, a host command or help; those
// stay one-shot.

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/adapters/gitfast"
	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/adapters/procwalk"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/surface/cli"
	"github.com/thesatellite-ai/lore/wire"
)

const (
	// readDialWait is how long the CLI waits to connect, and readAcceptWait
	// for the server to take the call (it runs one at a time): past either,
	// the command runs one-shot.
	readDialWait   = 100 * time.Millisecond
	readAcceptWait = 300 * time.Millisecond
	// readGoWait is how long the server waits for the CLI's go after it
	// accepted (a CLI that gave up closes instead).
	readGoWait = 2 * time.Second
	// readWatchEvery is how often the server checks that its socket is
	// still its own and the binary unchanged.
	readWatchEvery = 2 * time.Second
	// readMaxRequest bounds one request (args + env).
	readMaxRequest = 4 << 20
	// readGoByte is the CLI's go after the server accepted.
	readGoByte = 'g'
	// readServerGCPercent is the server's GOGC. The CLI's gcPercent (400)
	// lets a short-lived process skip most collections; a process that
	// lives for an hour would hold five times its live heap that way
	// (devco:D-845: ~600 MB RSS), so the server collects at Go's default.
	readServerGCPercent = 100
	// readServerMemLimit is the server's soft memory limit: past it the
	// server collects more often than readServerGCPercent asks.
	readServerMemLimit = 192 << 20
	// readServerMaxLive is the live heap past which the server exits after
	// the call it just served (the next call starts a fresh one): what it
	// keeps across calls (lenses, objects, decoded rows) is bounded, so
	// this only ever fires on growth nobody bounded (devco:D-845).
	readServerMaxLive = 128 << 20
)

// errNotServed is why the CLI runs a command one-shot after asking.
var errNotServed = errors.New("read server: not served")

// errServerLost is a server gone after it said Running: the command may
// have printed part of its answer, so the CLI must not run it again
// (devco:D-838); it exits 1 with readServerLostMsg instead.
var errServerLost = errors.New("read server: lost while running the command")

// binaryStamp names this lore binary: the file it runs from (device and
// inode), its size, mtime and the version it reports. A server answers
// only a client with the same stamp, and exits once its own binary changed
// (an upgrade writes a new file: a new inode, size or mtime).
//
// devco:D-864 (issue #94): the stamp names the file, not the path it was
// started through. On darwin os.Executable is the path the process was
// started by, so a lore reached through a package manager's symlink and
// the same file reached through its real path made two stamps for one
// binary. A copy is another file and still never answers.
func binaryStamp() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return fileStamp(exe, version)
}

// fileStamp is binaryStamp of the binary at path reporting version: the
// same file through any path (a symlink, a hard link) gives the same
// stamp, another file never does. "" when path cannot be read.
func fileStamp(path, version string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return strconv.FormatUint(uint64(st.Dev), 10) + "|" + strconv.FormatUint(st.Ino, 10) + "|" + strconv.FormatInt(fi.Size(), 10) + "|" + strconv.FormatInt(fi.ModTime().UnixNano(), 10) + "|" + version // Dev is int32 on darwin, uint64 on linux
}

// callReadServer asks the read server to run args. ok is false when the
// command must run one-shot: the server is off, absent (then it is
// started for the next call), busy, declined, or went away before it
// answered.
func callReadServer(args []string) (code int, ok bool) {
	home, want := readServerWanted(args)
	if !want || !stdioServable() {
		return 0, false
	}
	dir, sock := readServerPaths(home)
	conn, err := dialReadServer(dir, sock)
	if err != nil {
		startReadServer(dir)
		noteReadServer("one-shot")
		return 0, false
	}
	defer func() { _ = conn.Close() }()
	uc, isUnix := conn.(*net.UnixConn)
	if !isUnix {
		return 0, false
	}
	code, err = askReadServer(uc, args)
	if errors.Is(err, errServerLost) {
		// The command ran in the server and part of its answer may already
		// show: running it again here would print that part twice.
		noteReadServer("lost")
		_, _ = os.Stderr.WriteString(readServerLostMsg)
		return 1, true
	}
	if err != nil {
		noteReadServer("one-shot")
		return 0, false
	}
	noteReadServer("served")
	return code, true
}

// readServerLostMsg is what the CLI prints when the read server stopped
// while it ran the command (devco:D-838).
const readServerLostMsg = "lore: the local read server stopped while it was answering, so the answer above may be cut short; run the command again (LORE_READ_SERVER=0 runs it without the server)\n"

// sockPathMax is the longest socket path a unix socket address holds on
// every supported system (sun_path: 104 bytes on darwin, 108 on linux,
// with its NUL). A longer one is reached by its name from inside its
// folder.
const sockPathMax = 100

// dialReadServer connects to the socket. A path too long for a socket
// address (a deep lore home) is dialled by its bare name from inside the
// run folder, and the working directory is put back at once.
func dialReadServer(dir, sock string) (net.Conn, error) {
	if len(sock) <= sockPathMax {
		return net.DialTimeout("unix", sock, readDialWait)
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.Chdir(dir); err != nil {
		return nil, err
	}
	conn, derr := net.DialTimeout("unix", readServerSock, readDialWait)
	if err := os.Chdir(wd); err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, err
	}
	return conn, derr
}

// listenReadServer listens on the socket, by its bare name from inside
// the run folder when the path is too long for a socket address (the
// server's working directory is / again afterwards).
func listenReadServer(dir, sock string) (*net.UnixListener, error) {
	if len(sock) <= sockPathMax {
		return net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	}
	if err := os.Chdir(dir); err != nil {
		return nil, err
	}
	defer func() { _ = os.Chdir("/") }()
	return net.ListenUnix("unix", &net.UnixAddr{Name: readServerSock, Net: "unix"})
}

// stdioServable reports whether this process's stdin, stdout and stderr
// can be handed to the server: all open and in blocking mode (the server
// writes with blocking writes; a non-blocking pipe it shares could refuse
// a write).
func stdioServable() bool {
	for fd := 0; fd <= 2; fd++ {
		fl, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
		if err != nil || fl&unix.O_NONBLOCK != 0 {
			return false
		}
	}
	return true
}

// askReadServer sends the request with stdin, stdout and stderr, and waits
// for the server to accept and run it.
func askReadServer(conn *net.UnixConn, args []string) (int, error) {
	wd, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	mask := unix.Umask(0)
	unix.Umask(mask)
	body, err := json.Marshal(readRequest{V: readServerProto, Stamp: binaryStamp(), Args: args, Dir: wd, Env: os.Environ(), Parent: os.Getppid(), Umask: mask})
	if err != nil {
		return 0, err
	}
	msg := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	msg = append(msg, body...)
	if _, _, err := conn.WriteMsgUnix(msg, unix.UnixRights(0, 1, 2), nil); err != nil {
		return 0, err
	}
	r := bufio.NewReader(conn)
	if err := conn.SetReadDeadline(time.Now().Add(readAcceptWait)); err != nil {
		return 0, err
	}
	first, err := readReplyLine(r)
	if err != nil || !first.Accepted {
		return 0, errNotServed // busy, declined (first.Why), or gone
	}
	if _, err := conn.Write([]byte{readGoByte}); err != nil {
		return 0, err
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return 0, err
	}
	running, err := readReplyLine(r)
	if err != nil || !running.Running {
		return 0, errNotServed // gone before the command started: nothing printed, run it here
	}
	last, err := readReplyLine(r)
	if err != nil || !last.Served {
		return 0, errServerLost // gone mid-command: part of the answer may show already
	}
	if last.Pipe {
		dieOfClosedPipe()
	}
	return last.Code, nil
}

// dieOfClosedPipe ends the CLI by SIGPIPE, as a one-shot lore ends when
// it writes to a closed stdout or stderr: it writes one byte to the one
// whose reader is gone (poll says POLLERR), and the Go runtime ends a
// process whose write to fd 1 or 2 fails with EPIPE by that signal.
func dieOfClosedPipe() {
	for _, fd := range closedStdio() {
		f := os.Stdout
		if fd == 2 {
			f = os.Stderr
		}
		_, _ = f.Write([]byte{'\n'})
	}
}

// closedStdio is which of stdout (1) and stderr (2) is a pipe whose
// reader is gone (poll says POLLERR).
func closedStdio() []int {
	var closed []int
	for _, fd := range []int{1, 2} {
		p := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
		if n, err := unix.Poll(p, 0); err == nil && n == 1 && p[0].Revents&unix.POLLERR != 0 {
			closed = append(closed, fd)
		}
	}
	return closed
}

// readReplyLine reads one JSON reply line.
func readReplyLine(r *bufio.Reader) (readReply, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return readReply{}, err
	}
	var rep readReply
	return rep, json.Unmarshal(line, &rep)
}

// startReadServer starts `lore serve-reads` detached (its own session, no
// stdio, working directory /), unless a start was asked for within
// readServerRestart. Best effort: the command runs one-shot meanwhile.
func startReadServer(dir string) {
	if err := ensureRunDir(dir); err != nil {
		return
	}
	mark := filepath.Join(dir, readServerStart)
	if fi, err := os.Stat(mark); err == nil && time.Since(fi.ModTime()) < readServerRestart {
		return
	}
	if err := os.WriteFile(mark, nil, 0o600); err != nil {
		return
	}
	_ = os.Chtimes(mark, time.Now(), time.Now())
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, readServerWord)
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

// ensureRunDir makes the run folder 0700 and refuses one that is a link,
// not a folder, or not this user's.
func ensureRunDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok || int(st.Uid) != os.Getuid() {
		return errors.New("read server: " + dir + " is not this user's folder")
	}
	if fi.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// readServer is one running server: its registry (to tell a read), its
// stamp, its socket and when it last served.
type readServer struct {
	reg      *op.Registry
	defaults map[string]string // defaultVerbPaths: what a bare group runs
	stamp    string
	sock     string
	sockID   string
	listener *net.UnixListener
	idle     time.Duration
	started  time.Time
	mu       sync.Mutex // one command at a time: run() swaps process state
	// pipes gets SIGPIPE: a write to a client's closed pipe fails with
	// EPIPE instead of ending the server, and runFor tells the client.
	pipes chan os.Signal
	// piped is set by closedPipe when the command's error was that write:
	// the signal above reaches runFor later, and may come after the
	// command returned.
	piped atomic.Bool
}

// closedPipe is cli.ClosedPipe while the server runs a command: err is a
// write that hit the client's closed stdout or stderr. It silences both
// at once, so the command's error line never shows (a one-shot lore is
// dead at that write); waiting for the SIGPIPE to reach runFor raced the
// command's own error print (devco:D-838: `lore sql … | head -c 5` served
// printed "ERROR: [E_INTERNAL] internal error", one-shot nothing).
func (s *readServer) closedPipe(err error) bool {
	if !errors.Is(err, syscall.EPIPE) || len(closedStdio()) == 0 {
		return false
	}
	silenceStdio()
	s.piped.Store(true)
	return true
}

// serveReads is `lore serve-reads`: the server of this lore home until it
// idles out, its socket goes, or the binary changes. Always exit 0; it
// prints nothing (its stdio is /dev/null).
// Main applied the binary's Options before it called this (applyOptions),
// so its stamp ends in the same version as every client's.
func serveReads() int {
	debug.SetGCPercent(readServerGCPercent)
	debug.SetMemoryLimit(readServerMemLimit)
	home, err := lorehome.Resolve()
	if err != nil {
		return 0
	}
	dir, sock := readServerPaths(home.Dir)
	if ensureRunDir(dir) != nil {
		return 0
	}
	lock, err := os.OpenFile(filepath.Join(dir, readServerLock), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 0
	}
	defer func() { _ = lock.Close() }()
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return 0 // another server of this home runs
	}
	reg, err := wire.Registry(&wire.App{})
	if err != nil {
		return 0
	}
	_ = os.Remove(sock)
	ln, err := listenReadServer(dir, sock)
	if err != nil {
		return 0
	}
	ln.SetUnlinkOnClose(false) // removed below only while it is still ours
	if os.Chmod(sock, 0o600) != nil {
		_ = ln.Close()
		return 0
	}
	s := &readServer{reg: reg, defaults: defaultVerbPaths(), stamp: binaryStamp(), sock: sock, sockID: fileID(sock), listener: ln, idle: readServerIdleFor(), started: time.Now(), pipes: make(chan os.Signal, 1)}
	signal.Notify(s.pipes, syscall.SIGPIPE)
	cli.ClosedPipe = s.closedPipe
	stopLenses := sqlite.EnableLensReuse()
	defer stopLenses()
	stopObjects := gitfast.ShareObjectStores() // decoded git objects kept across calls (D-838)
	defer stopObjects()
	done := make(chan struct{})
	go s.watch(done)
	s.loop()
	close(done)
	_ = ln.Close()
	if fileID(sock) == s.sockID {
		_ = os.Remove(sock)
	}
	return 0
}

// fileID is the device and inode of path ("" when it cannot be read).
func fileID(path string) string {
	fi, err := os.Lstat(path)
	if err != nil {
		return ""
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return strconv.FormatUint(uint64(st.Dev), 10) + ":" + strconv.FormatUint(st.Ino, 10)
}

// watch closes the listener once the socket is not this server's any
// more (removed, or the lore home with it) or the binary changed.
func (s *readServer) watch(done <-chan struct{}) {
	t := time.NewTicker(readWatchEvery)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if fileID(s.sock) != s.sockID || binaryStamp() != s.stamp || time.Since(s.started) > readServerMaxLife {
				_ = s.listener.Close()
				return
			}
		}
	}
}

// loop accepts calls until the idle time passes without one or the
// listener closes.
func (s *readServer) loop() {
	for {
		if s.listener.SetDeadline(time.Now().Add(s.idle)) != nil {
			return
		}
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			return // idle, closed by watch, or broken
		}
		s.handle(conn)
		if time.Since(s.started) > readServerMaxLife || heapLive() > readServerMaxLive {
			return
		}
	}
}

// handle serves one connection.
func (s *readServer) handle(conn *net.UnixConn) {
	defer func() { _ = conn.Close() }()
	req, fds, err := readRequestFrom(conn)
	defer closeFDs(fds)
	reply := func(r readReply) bool {
		b, _ := json.Marshal(r)
		_, werr := conn.Write(append(b, '\n'))
		return werr == nil
	}
	switch {
	case err != nil:
		reply(readReply{Why: "bad request"})
		return
	case peerUID(conn) != os.Getuid():
		reply(readReply{Why: "another user"})
		return
	case req.Stamp != s.stamp:
		reply(readReply{Why: "another lore binary"}) // it runs one-shot; this server exits once its own binary changes (watch)
		return
	case len(fds) != 3:
		reply(readReply{Why: "no stdio"})
		return
	}
	if _, ok := cli.ReadCall(s.reg, req.Args, s.defaults); !ok {
		reply(readReply{Why: "not a read"})
		return
	}
	if fi, err := os.Stat(req.Dir); err != nil || !fi.IsDir() {
		reply(readReply{Why: "no working directory"})
		return
	}
	if !reply(readReply{Accepted: true}) {
		return
	}
	if conn.SetReadDeadline(time.Now().Add(readGoWait)) != nil {
		return
	}
	var goByte [1]byte
	if _, err := io.ReadFull(conn, goByte[:]); err != nil || goByte[0] != readGoByte {
		return // the CLI gave up and runs it itself
	}
	_ = conn.SetReadDeadline(time.Time{})
	if readServerTestDie(req.Env) == readServerDieBefore {
		os.Exit(0) // test builds only: a server gone before the command ran
	}
	if !reply(readReply{Running: true}) {
		return
	}
	if readServerTestDie(req.Env) == readServerDiePrinting {
		_, _ = unix.Write(fds[1], []byte(readServerDiePrint))
		os.Exit(0) // test builds only: a server gone after it printed part of an answer
	}
	code, pipe := s.runFor(conn, req, fds)
	reply(readReply{Served: true, Code: code, Pipe: pipe})
	noteServerHeap(req.Env)
}

// readRequestFrom reads the length-prefixed request and the stdio file
// descriptors sent with it.
func readRequestFrom(conn *net.UnixConn) (readRequest, []int, error) {
	if err := conn.SetReadDeadline(time.Now().Add(readGoWait)); err != nil {
		return readRequest{}, nil, err
	}
	buf := make([]byte, 64<<10)
	oob := make([]byte, unix.CmsgSpace(3*4))
	n, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return readRequest{}, nil, err
	}
	fds, ferr := rightsOf(oob[:oobn])
	if ferr != nil || n < 4 {
		return readRequest{}, fds, errors.Join(ferr, errNotServed)
	}
	size := int(binary.BigEndian.Uint32(buf[:4]))
	if size <= 0 || size > readMaxRequest {
		return readRequest{}, fds, errNotServed
	}
	body := make([]byte, 0, size)
	body = append(body, buf[4:n]...)
	if len(body) < size {
		rest := make([]byte, size-len(body))
		if _, err := io.ReadFull(conn, rest); err != nil {
			return readRequest{}, fds, err
		}
		body = append(body, rest...)
	}
	if len(body) != size {
		return readRequest{}, fds, errNotServed
	}
	var req readRequest
	if err := json.Unmarshal(body, &req); err != nil || req.V != readServerProto {
		return readRequest{}, fds, errors.Join(err, errNotServed)
	}
	return req, fds, nil
}

// rightsOf is the file descriptors of a control message.
func rightsOf(oob []byte) ([]int, error) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	var fds []int
	for _, m := range msgs {
		got, err := unix.ParseUnixRights(&m)
		if err != nil {
			return fds, err
		}
		fds = append(fds, got...)
	}
	return fds, nil
}

// closeFDs closes the received descriptors.
func closeFDs(fds []int) {
	for _, fd := range fds {
		_ = unix.Close(fd)
	}
}

// runFor runs the command with the client's working directory,
// environment, arguments, parent and stdio in place of the server's, and
// puts the server's back afterwards. The command is cancelled when the
// client goes away or a write hits its closed pipe (pipe is then true).
func (s *readServer) runFor(conn *net.UnixConn, req readRequest, fds []int) (code int, pipe bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	restore, err := swapProcess(req, fds)
	if err != nil {
		restore()
		return 1, false
	}
	defer restore()
	for len(s.pipes) > 0 { // a SIGPIPE of an earlier command
		<-s.pipes
	}
	s.piped.Store(false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { // the CLI never writes after go: a read returns when it goes away
		var b [1]byte
		_, _ = conn.Read(b[:])
		cancel()
	}()
	done, piped := make(chan struct{}), make(chan bool, 1)
	go func() {
		select {
		case <-s.pipes:
			// A one-shot lore is dead at this write: nothing after it may
			// show (an error line about the write), so stdout and stderr go
			// to /dev/null until the command returns.
			silenceStdio()
			cancel()
			piped <- true
		case <-done:
			piped <- false
		}
	}()
	code = run(ctx, req.Args)
	close(done)
	_ = conn.SetReadDeadline(time.Now()) // ends the watcher above
	return code, <-piped || s.piped.Load()
}

// silenceStdio points fds 1 and 2 at /dev/null (swapProcess's restore
// puts the server's own back).
func silenceStdio() {
	null, err := unix.Open(os.DevNull, unix.O_WRONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	_ = unix.Dup2(null, 1)
	_ = unix.Dup2(null, 2)
	_ = unix.Close(null)
}

// swapProcess puts the client's state in place of the server's: working
// directory, environment, os.Args, the parent agent detection walks from,
// and stdin/stdout/stderr (dup2 over 0, 1, 2, so every *os.File and
// isatty check of the process sees the client's). restore undoes what was
// done, also after an error.
func swapProcess(req readRequest, fds []int) (restore func(), err error) {
	var undo []func()
	restore = func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}
	env, args := os.Environ(), os.Args
	undo = append(undo, func() { setEnv(env); os.Args = args })
	setEnv(req.Env)
	os.Args = append([]string{args[0]}, req.Args...)
	if err := os.Chdir(req.Dir); err != nil {
		return restore, err
	}
	undo = append(undo, func() { _ = os.Chdir("/") })
	undo = append(undo, procwalk.AsParentOf(req.Parent))
	old := unix.Umask(req.Umask)
	undo = append(undo, func() { unix.Umask(old) })
	for i, fd := range fds {
		saved, err := unix.Dup(i)
		if err != nil {
			return restore, err
		}
		target := i
		undo = append(undo, func() { _ = unix.Dup2(saved, target); _ = unix.Close(saved) })
		if err := unix.Dup2(fd, target); err != nil {
			return restore, err
		}
	}
	return restore, nil
}

// setEnv makes kv (KEY=value pairs) the whole environment.
func setEnv(kv []string) {
	os.Clearenv()
	for _, e := range kv {
		for i := 1; i < len(e); i++ { // a name may start with '=' on no unix, but never be empty
			if e[i] == '=' {
				_ = os.Setenv(e[:i], e[i+1:])
				break
			}
		}
	}
}
