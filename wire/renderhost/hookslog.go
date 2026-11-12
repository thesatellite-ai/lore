package renderhost

// hookslog.go — <lore home>/logs/hooks.log, where an agent hook's
// failure goes (a hook must never print into the agent's context). The
// log is capped: once it passes hooksLogMax it is renamed hooks.log.1
// (replacing an older one) and a new file starts, so at most about twice
// the cap stays on disk.

import (
	"os"
	"path/filepath"
	"time"
)

// Hook log names and limits.
const (
	// HooksLogFile is the log, relative to the lore home.
	HooksLogFile = "logs/hooks.log"
	// hooksLogOld is the one rotated copy kept.
	hooksLogOldSuffix = ".1"
	// hooksLogMax is the size past which the log rotates (256 KiB).
	hooksLogMax      = 256 << 10
	hooksLogDirMode  = 0o700
	hooksLogFileMode = 0o600
)

// loreHome is the lore home this host writes under (Options.Home).
func (h *Host) loreHome() string { return h.opts.Home }

// appendHooksLog appends one timestamped line to home's hook log,
// rotating it first when it is over the cap. Best effort: a hook log
// failure must not reach the tool.
func appendHooksLog(home string, now time.Time, line string) {
	if home == "" {
		return
	}
	path := filepath.Join(home, filepath.FromSlash(HooksLogFile))
	if os.MkdirAll(filepath.Dir(path), hooksLogDirMode) != nil {
		return
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() >= hooksLogMax {
		_ = os.Rename(path, path+hooksLogOldSuffix) // best effort: a failed rotation keeps appending
	}
	fh, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, hooksLogFileMode)
	if err != nil {
		return
	}
	_, _ = fh.WriteString(now.Format(time.RFC3339) + " " + line + "\n")
	_ = fh.Close()
}
