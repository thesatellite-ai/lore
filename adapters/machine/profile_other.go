//go:build !linux && !darwin && !windows

package machine

func osInfo(Files) NameVersion   { return NameVersion{} }
func kernelInfo() NameVersion    { return NameVersion{} }
func libcInfo(Files) NameVersion { return NameVersion{} }
func cpuInfo(Files) CPU          { return CPU{} }
func ramMB(Files) int64          { return 0 }
