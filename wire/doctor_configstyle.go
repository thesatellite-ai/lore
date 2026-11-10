package wire

import (
	"context"
	"strings"

	opsconfig "github.com/thesatellite-ai/lore/ops/config"
)

// DoctorCheckConfigStyle is the "config.yaml has lists on one line" check
// of `lore doctor`.
const DoctorCheckConfigStyle DoctorCheck = "config_style"

// cmdConfigFmt is the command that rewrites the file (ops/config fmt.go).
const cmdConfigFmt = "lore config fmt"

// DoctorConfigStyle warns when ~/.lore/config.yaml holds a list or mapping
// of lists or mappings on one flow line (`setups: [{name: me, …}]`): a
// file saved before the block-style writer (b6a39e32) stays that way until
// something writes it again, and doctor only reads, never rewrites. The
// fix is `lore config fmt` (block style, comments and key order kept);
// with fix (`lore doctor --fix`) doctor runs that same rewrite. A config
// that cannot be read or parsed gives no finding here: `lore config check`
// and the setup checks report it.
func (a *App) DoctorConfigStyle(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	f, err := a.ReadConfigFile(ctx)
	if err != nil {
		return nil, nil // unreadable: lore config check reports it
	}
	keys, err := opsconfig.FlowKeys(f)
	if err != nil || len(keys) == 0 {
		return nil, nil // does not parse: lore config check reports it
	}
	finding := DoctorFinding{Check: DoctorCheckConfigStyle, Level: DoctorWarning,
		Message: f.Path + " has " + strings.Join(keys, ", ") + " written on one line (flow style), hard to read and to edit by hand",
		Fix:     cmdConfigFmt + " (block style, comments and key order kept), or lore doctor --fix"}
	if fix {
		if _, ferr := opsconfig.Format(ctx, a, false); ferr != nil {
			finding.Message += "; the rewrite failed: " + ferr.Error()
			return []DoctorFinding{finding}, nil
		}
		finding.Fixed = true
	}
	return []DoctorFinding{finding}, nil
}
