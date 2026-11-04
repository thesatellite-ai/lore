//go:build darwin

package procwalk

import (
	"bytes"
	"encoding/binary"

	"golang.org/x/sys/unix"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// argcLen is the int32 argc that starts kern.procargs2.
const argcLen = 4

// read returns pid's process and its parent pid (sysctl, pure Go).
func read(pid int) (agentdetect.Process, int, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp.Proc.P_pid != int32(pid) {
		return agentdetect.Process{}, 0, false
	}
	comm := string(bytes.TrimRight(kp.Proc.P_comm[:], "\x00"))
	pr := agentdetect.Process{PID: pid, Exe: comm}
	// kern.procargs2: argc, exec path, NULs, then argv[0..argc).
	if raw, err := unix.SysctlRaw("kern.procargs2", pid); err == nil && len(raw) > argcLen {
		argc := int(binary.LittleEndian.Uint32(raw[:argcLen]))
		rest := raw[argcLen:]
		if i := bytes.IndexByte(rest, 0); i >= 0 {
			pr.Exe = string(rest[:i])
			rest = bytes.TrimLeft(rest[i:], "\x00")
		}
		var argv []string
		for len(argv) < argc && len(rest) > 0 {
			i := bytes.IndexByte(rest, 0)
			if i < 0 {
				argv = append(argv, string(rest))
				break
			}
			argv = append(argv, string(rest[:i]))
			rest = rest[i+1:]
		}
		if len(argv) > 1 {
			pr.Args = argv[1:]
		}
	}
	return pr, int(kp.Eproc.Ppid), true
}
