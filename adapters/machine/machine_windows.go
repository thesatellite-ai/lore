//go:build windows

package machine

import (
	"golang.org/x/sys/windows/registry"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Where Windows keeps the machine GUID.
const (
	cryptographyKey = `SOFTWARE\Microsoft\Cryptography`
	machineGUIDName = "MachineGuid"
)

// osID returns MachineGuid from the registry (64-bit view, so a 32-bit
// build reads the same value).
func osID() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, cryptographyKey, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", errs.Wrap(errs.NotFound, "machine: cannot open the Cryptography registry key", err)
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetStringValue(machineGUIDName)
	if err != nil {
		return "", errs.Wrap(errs.NotFound, "machine: no MachineGuid in the registry", err)
	}
	return v, nil
}
