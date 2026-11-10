package machine

import (
	"os"
	"os/user"
	"runtime"
)

// NameVersion is an {name, version} pair (os, kernel, libc).
type NameVersion struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// CPU is the processor model and logical core count.
type CPU struct {
	Model string `json:"model,omitempty"`
	Cores int    `json:"cores,omitempty"`
}

// Profile is WHAT this machine is (AGENT_IDENTITY_DESIGN 6.8, MACHINE
// PROFILE + OQ-AV): read from env vars, local files and system calls only,
// no network, no subprocess (the one macOS ioreg for the id aside). It
// carries no lore version (that is each row's _w.v) and no cloud account
// id. Label is set by `lore whoami --machine-label`, never collected.
type Profile struct {
	Hostname string      `json:"hostname,omitempty"`
	OS       NameVersion `json:"os"`
	Kernel   NameVersion `json:"kernel"`
	Libc     NameVersion `json:"libc"`
	Arch     string      `json:"arch"`
	CPU      CPU         `json:"cpu"`
	RAMMB    int64       `json:"ram_mb,omitempty"`
	OSUser   string      `json:"os_user,omitempty"`
	Runtime  Runtime     `json:"runtime"`
}

// Collect reads this machine's profile. Every reader that fails leaves its
// field blank (never guessed, never an error).
func Collect(getenv func(string) string, fsys Files) Profile {
	pr := Profile{Arch: runtime.GOARCH, Runtime: DetectRuntime(getenv, fsys)}
	if !Ephemeral(pr.Runtime) {
		if h, err := os.Hostname(); err == nil {
			pr.Hostname = h
		}
	}
	if u, err := user.Current(); err == nil {
		pr.OSUser = u.Username
	}
	pr.OS, pr.Kernel, pr.Libc = osInfo(fsys), kernelInfo(), libcInfo(fsys)
	pr.CPU, pr.RAMMB = cpuInfo(fsys), ramMB(fsys)
	if pr.CPU.Cores == 0 {
		pr.CPU.Cores = runtime.NumCPU()
	}
	return pr
}
