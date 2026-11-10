package setupweb

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Browser openers per platform (ONB 5.1).
const (
	goosDarwin  = "darwin"
	goosWindows = "windows"
	openDarwin  = "open"
	openUnix    = "xdg-open"
	openWindows = "rundll32"
	// openWindowsArg is rundll32's entry point that hands a URL to the
	// default browser.
	openWindowsArg = "url.dll,FileProtocolHandler"
	envDisplay     = "DISPLAY"
	envWayland     = "WAYLAND_DISPLAY"
)

// OpenArgv is the command that opens url in the default browser on goos,
// and false when there is no display to open it on (a Linux or BSD
// session without DISPLAY or WAYLAND_DISPLAY: an ssh login). getenv is a
// parameter so every platform's branch is tested everywhere.
func OpenArgv(goos, url string, getenv func(string) string) ([]string, bool) {
	switch goos {
	case goosDarwin:
		return []string{openDarwin, url}, true
	case goosWindows:
		return []string{openWindows, openWindowsArg, url}, true
	}
	if getenv(envDisplay) == "" && getenv(envWayland) == "" {
		return nil, false
	}
	return []string{openUnix, url}, true
}

// OpenBrowser opens url with OpenArgv for this machine. With no display it
// returns E_UNSUPPORTED; the caller has already printed the URL.
func OpenBrowser(ctx context.Context, url string) error {
	argv, ok := OpenArgv(runtime.GOOS, url, os.Getenv)
	if !ok {
		return errs.New(errs.Unsupported, "no display")
	}
	// argv is a fixed opener and lore's own URL; no shell is involved.
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return errs.Wrap(errs.Unsupported, "start "+argv[0], err)
	}
	go func() { _ = cmd.Wait() }() // reap the opener; its exit status says nothing useful
	return nil
}
