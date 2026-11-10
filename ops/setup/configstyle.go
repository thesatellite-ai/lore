package setup

// configstyle.go — config.yaml written on one line (doctor's config_style
// warning, wire DoctorConfigStyle) on the setup page (OWN7-6, owner
// 2026-11-10). setup.list reports the keys a list or mapping of lists sits
// on one flow line for; Home and Doctor show a box with the fix
// `lore config fmt` and a Run it that calls config.fmt (on the page
// allowlist: it only changes the file's layout, refused when the schema
// would read the result differently).
//
//	setup.list ─▶ ConfigStyleSource.ConfigStyle ─▶ ListOut.ConfigStyle (zero: block style)
//	page Run it ─▶ POST config.fmt ─▶ opsconfig.Format ─▶ setup.list again: no box

import "context"

// ConfigFmtCommand is the config_style fix the page shows and runs.
var ConfigFmtCommand = []string{"lore", "config", "fmt"}

// ConfigStyle is config.yaml's one-line keys: File is its path, Keys the
// dotted keys written in flow style that block style would spread over
// lines, Command the fix. Zero (left out of setup.list's JSON) when the
// file reads in block style, is absent, or does not parse (the config
// problems report that).
type ConfigStyle struct {
	File    string   `json:"file"`
	Keys    []string `json:"keys"`
	Command []string `json:"command"`
}

// IsZero reports a file with nothing on one line (json omitzero).
func (c ConfigStyle) IsZero() bool { return len(c.Keys) == 0 }

// ConfigStyleSource is an optional upgrade of Onboard: config.yaml's flow
// keys (wire: opsconfig.FlowKeys over the file). Read only; it never
// rewrites the file.
type ConfigStyleSource interface {
	ConfigFlowKeys(ctx context.Context) (file string, keys []string)
}

// configStyle is setup.list's ConfigStyle from env (zero without the
// upgrade or with nothing on one line).
func (e engine) configStyle(ctx context.Context) ConfigStyle {
	src, ok := e.env.(ConfigStyleSource)
	if !ok {
		return ConfigStyle{}
	}
	file, keys := src.ConfigFlowKeys(ctx)
	if len(keys) == 0 {
		return ConfigStyle{}
	}
	return ConfigStyle{File: file, Keys: keys, Command: append([]string(nil), ConfigFmtCommand...)}
}
