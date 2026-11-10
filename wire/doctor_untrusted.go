package wire

// doctor_untrusted.go — the untrusted-host row of `lore doctor` (LORE_TEAM_SPEC
// §6.6, §9.4; v0.1.17 doctor_stores.go, P3-CMDLORE-11): org stores
// configured on a machine whose hostname matches an untrusted_hosts pattern
// is a warning, since anyone with access to the machine can read the store
// clones. The old binary prints this row from its own doctor host, so the
// core check runs only in the binary StandaloneOptionsOrg built (cmd/lore).

import (
	"context"
	"os"
	"path"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
)

// DoctorCheckUntrustedHost is the untrusted-host check's name.
const DoctorCheckUntrustedHost DoctorCheck = "untrusted_host"

// DoctorUntrustedHost reports org stores configured on an untrusted host.
func (a *App) DoctorUntrustedHost(context.Context) ([]DoctorFinding, error) {
	if _, standalone := a.opts.OrgCommands.(*standaloneOrg); !standalone {
		return nil, nil // the old binary's own doctor host prints this row
	}
	host, err := os.Hostname()
	if err != nil {
		return nil, nil // no hostname: nothing to match
	}
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return nil, nil // a broken config is `lore config check`'s to report
	}
	hasStores := false
	for _, o := range cfg.Orgs {
		hasStores = hasStores || len(o.Stores) > 0
	}
	if !hasStores {
		return nil, nil
	}
	for _, pat := range cfg.UntrustedHosts {
		if ok, _ := path.Match(strings.ToLower(pat), strings.ToLower(host)); ok {
			return []DoctorFinding{{Check: DoctorCheckUntrustedHost, Level: DoctorWarning, Plain: true,
				Message: "this machine (" + host + ") matches untrusted_hosts " + pat + " but org stores are configured here: anyone with access to it can read ~/.lore/clones (§6.6); remove the stores from this machine's config"}}, nil
		}
	}
	return nil, nil
}
