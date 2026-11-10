package wire

// setupconfigstyle.go — opssetup.ConfigStyleSource on *App: the keys of
// ~/.lore/config.yaml written on one flow line, the same reading doctor's
// config_style check does (doctor_configstyle.go), for setup.list's
// config_style (the setup page's box with Run it, OWN7-6).

import (
	"context"

	opsconfig "github.com/thesatellite-ai/lore/ops/config"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

var _ opssetup.ConfigStyleSource = (*App)(nil)

// ConfigFlowKeys implements opssetup.ConfigStyleSource: config.yaml's path
// and its flow-style keys; none for a file that is absent, unreadable or
// does not parse (setup.list's config problems report those).
func (a *App) ConfigFlowKeys(ctx context.Context) (string, []string) {
	f, err := a.ReadConfigFile(ctx)
	if err != nil {
		return "", nil
	}
	keys, err := opsconfig.FlowKeys(f)
	if err != nil {
		return f.Path, nil
	}
	return f.Path, keys
}
