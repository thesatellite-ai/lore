//go:build linux

package machine

import (
	"bufio"
	"bytes"
	"debug/elf"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// osInfo parses /etc/os-release ID and VERSION_ID.
func osInfo(fsys Files) NameVersion {
	b, err := fsys.ReadFile("/etc/os-release")
	if err != nil {
		return NameVersion{Name: "linux"}
	}
	nv := NameVersion{}
	for line := range strings.SplitSeq(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			nv.Name = v
		case "VERSION_ID":
			nv.Version = v
		}
	}
	return nv
}

// kernelInfo is the uname system call.
func kernelInfo() NameVersion {
	var u unix.Utsname
	if unix.Uname(&u) != nil {
		return NameVersion{}
	}
	return NameVersion{Name: unix.ByteSliceToString(u.Sysname[:]), Version: unix.ByteSliceToString(u.Release[:])}
}

// libcFiles are where glibc's libc.so.6 usually lives.
var libcFiles = []string{"/lib/x86_64-linux-gnu/libc.so.6", "/lib/aarch64-linux-gnu/libc.so.6", "/lib64/libc.so.6", "/usr/lib64/libc.so.6", "/lib/libc.so.6", "/usr/lib/libc.so.6"}

// glibcRe is the release string inside libc.so.6.
var glibcRe = regexp.MustCompile(`GNU C Library [^\n]*?version ([0-9]+\.[0-9]+)`)

// maxLibcRead bounds the libc.so.6 scan (the file is ~2 MB).
const maxLibcRead = 8 << 20

// libcInfo is glibc or musl: the ELF interpreter of /bin/sh, then the
// version string in libc.so.6 (bounded read; no CGO call, no subprocess).
func libcInfo(Files) NameVersion {
	interp := ""
	if f, err := elf.Open("/bin/sh"); err == nil {
		for _, p := range f.Progs {
			if p.Type == elf.PT_INTERP {
				b, rerr := io.ReadAll(io.LimitReader(p.Open(), 4096))
				if rerr == nil {
					interp = string(bytes.TrimRight(b, "\x00"))
				}
			}
		}
		_ = f.Close()
	}
	if strings.Contains(interp, "ld-musl") {
		return NameVersion{Name: "musl"}
	}
	if interp == "" {
		if m, _ := filepath.Glob("/lib/ld-musl-*.so.1"); len(m) > 0 {
			return NameVersion{Name: "musl"}
		}
		return NameVersion{}
	}
	for _, p := range libcFiles {
		f, err := os.Open(filepath.Clean(p)) // p is one of the fixed system paths above
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(f, maxLibcRead))
		_ = f.Close()
		if m := glibcRe.FindSubmatch(b); m != nil {
			return NameVersion{Name: "glibc", Version: string(m[1])}
		}
		return NameVersion{Name: "glibc"}
	}
	return NameVersion{Name: "glibc"}
}

// cpuInfo is the first "model name" of /proc/cpuinfo.
func cpuInfo(fsys Files) CPU {
	c := CPU{Cores: runtime.NumCPU()}
	b, err := fsys.ReadFile("/proc/cpuinfo")
	if err != nil {
		return c
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), ":"); ok && strings.TrimSpace(k) == "model name" {
			c.Model = strings.TrimSpace(v)
			break
		}
	}
	return c
}

// kbPerMB converts /proc/meminfo kB to MB.
const kbPerMB = 1024

// ramMB is MemTotal of /proc/meminfo.
func ramMB(fsys Files) int64 {
	b, err := fsys.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			f := strings.Fields(rest)
			if len(f) > 0 {
				if kb, err := strconv.ParseInt(f[0], 10, 64); err == nil {
					return kb / kbPerMB
				}
			}
		}
	}
	return 0
}
