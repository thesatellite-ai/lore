package wire

// settings_report.go — the v0.1.17 settings step 12 cannot classify, for
// `lore doctor` (FORMAT_EVOLUTION step list row 12, CFG12: "a key lore
// cannot classify is reported by lore doctor and left in place, never
// guessed"; FB-DOC-4).
//
//	doctor ─► UnclassifiedSettings ─► migrateDBSettings(dryRun) ─► keep "left in place" lines

import (
	"context"
	"strings"
)

// settingLeftInPlace ends (or, with a hint after it, sits inside) every
// step 12 line about a setting the step does not move: a key with no
// 0.1.18 home, a value config.yaml cannot hold, two sources disagreeing,
// a lease pair that would break the lease rule.
const settingLeftInPlace = "left in place"

// UnclassifiedSettings returns step 12's lines for the v0.1.17 settings
// (project_configs rows and the checkout's lore.db `config` rows) that
// stay where they are. A dry run: it writes nothing, so doctor can call it
// on every run; settings already moved or already set are not listed.
func (a *App) UnclassifiedSettings(ctx context.Context) ([]string, error) {
	steps, _, err := a.migrateDBSettings(ctx, true)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range steps {
		if strings.Contains(s, settingLeftInPlace) {
			out = append(out, s)
		}
	}
	return out, nil
}
