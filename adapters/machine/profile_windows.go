//go:build windows

package machine

import (
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Registry paths of the profile (6.8; values unverified: blank if absent).
const (
	currentVersionKey = `SOFTWARE\Microsoft\Windows NT\CurrentVersion`
	cpuKey            = `HARDWARE\DESCRIPTION\System\CentralProcessor\0`
)

// regString reads one string value ("" when absent).
func regString(path, name string) string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return ""
	}
	defer func() { _ = k.Close() }()
	s, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return s
}

// regInt reads one integer value (0 when absent).
func regInt(path, name string) uint64 {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return 0
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetIntegerValue(name)
	if err != nil {
		return 0
	}
	return v
}

// osInfo is ProductName + DisplayVersion.
func osInfo(Files) NameVersion {
	name := regString(currentVersionKey, "ProductName")
	if name == "" {
		name = "windows"
	}
	return NameVersion{Name: name, Version: regString(currentVersionKey, "DisplayVersion")}
}

// kernelInfo is RtlGetVersion major.minor.build plus registry UBR.
func kernelInfo() NameVersion {
	v := windows.RtlGetVersion()
	s := strconv.FormatUint(uint64(v.MajorVersion), 10) + "." + strconv.FormatUint(uint64(v.MinorVersion), 10) + "." + strconv.FormatUint(uint64(v.BuildNumber), 10)
	if ubr := regInt(currentVersionKey, "UBR"); ubr != 0 {
		s += "." + strconv.FormatUint(ubr, 10)
	}
	return NameVersion{Name: "Windows", Version: s}
}

// libcInfo is blank on Windows.
func libcInfo(Files) NameVersion { return NameVersion{} }

// cpuInfo is ProcessorNameString (cores from runtime.NumCPU in Collect).
func cpuInfo(Files) CPU { return CPU{Model: regString(cpuKey, "ProcessorNameString")} }

// memoryStatusEx is MEMORYSTATUSEX (x/sys/windows has no wrapper).
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// bytesPerMB converts TotalPhys.
const bytesPerMB = 1 << 20

// ramMB is GlobalMemoryStatusEx TotalPhys, pure Go through the lazy DLL.
func ramMB(Files) int64 {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0
	}
	return int64(m.TotalPhys / bytesPerMB)
}
