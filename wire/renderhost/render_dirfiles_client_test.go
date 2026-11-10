package renderhost

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/wire"
)

// TestDirFilesClientDefault (CQ10 on cmd/lore, 8e36e02a): folder files
// are on by default in an own repo and off by default in a client repo,
// and the client answer comes from the App (App.ClientRepo) when the
// binary hands the Host no RepoIsClient hook, as cmd/lore does. A client
// check that fails is a client repo (fail closed). An explicit
// context.dir_files row decides in both kinds of repo. The matrix crosses
// the App's client answer (own, client, check error) × the Host hook
// (none, says own, says client) × the policy row (unset, on, off).
func TestDirFilesClientDefault(t *testing.T) {
	errCheck := errors.New("roster unreadable")
	apps := []struct {
		name   string
		answer func(context.Context) (bool, error)
		client bool // what the App reports (an error is a client repo)
	}{
		{"own repo", func(context.Context) (bool, error) { return false, nil }, false},
		{"client repo", func(context.Context) (bool, error) { return true, nil }, true},
		{"check fails", func(context.Context) (bool, error) { return false, errCheck }, true},
	}
	type hook struct {
		name string
		fn   func(context.Context) (bool, error)
	}
	hooks := []hook{
		{"no hook", nil},
		{"hook says own", func(context.Context) (bool, error) { return false, nil }},
		{"hook says client", func(context.Context) (bool, error) { return true, nil }},
		{"hook fails", func(context.Context) (bool, error) { return false, errCheck }},
	}
	rows := []struct {
		name  string
		value string // "" = no row
	}{{"unset", ""}, {"on", "on"}, {"off", "off"}}

	for _, ap := range apps {
		for _, row := range rows {
			h := opstest.New(t, func(o *wire.Options) { o.RepoIsClient = ap.answer })
			if row.value != "" {
				h.MustYes("policy.set", `{"key":"context.dir_files","value":"`+row.value+`"}`)
			}
			for _, hk := range hooks {
				t.Run(ap.name+"/"+row.name+"/"+hk.name, func(t *testing.T) {
					host := New(Options{App: h.App, Home: t.TempDir(), RepoIsClient: hk.fn})
					client := ap.client
					switch {
					case hk.fn == nil:
					case hk.name == "hook says own":
						client = false
					default:
						client = true // says client, or fails closed
					}
					want := !client
					if row.value != "" {
						want = row.value == "on"
					}
					if got := host.loadDirSettings(context.Background()).On; got != want {
						t.Errorf("folder files on = %v, want %v", got, want)
					}
				})
			}
		}
	}
}
