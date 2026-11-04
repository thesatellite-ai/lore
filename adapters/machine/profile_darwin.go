//go:build darwin

package machine

import (
	"strings"

	"golang.org/x/sys/unix"
)

// osInfo is macOS kern.osproductversion.
func osInfo(Files) NameVersion {
	v, _ := unix.Sysctl("kern.osproductversion")
	return NameVersion{Name: "macos", Version: strings.TrimSpace(v)}
}

// kernelInfo is unix.Uname (kern.ostype, kern.osrelease on darwin).
func kernelInfo() NameVersion {
	var u unix.Utsname
	if unix.Uname(&u) != nil {
		return NameVersion{}
	}
	return NameVersion{Name: unix.ByteSliceToString(u.Sysname[:]), Version: unix.ByteSliceToString(u.Release[:])}
}

// libcInfo is blank on macOS (6.8).
func libcInfo(Files) NameVersion { return NameVersion{} }

// cpuInfo is machdep.cpu.brand_string and hw.ncpu.
func cpuInfo(Files) CPU {
	model, _ := unix.Sysctl("machdep.cpu.brand_string")
	n, _ := unix.SysctlUint32("hw.ncpu")
	return CPU{Model: strings.TrimSpace(model), Cores: int(n)}
}

// bytesPerMB converts hw.memsize.
const bytesPerMB = 1 << 20

// ramMB is hw.memsize.
func ramMB(Files) int64 {
	b, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return int64(b / bytesPerMB)
}
