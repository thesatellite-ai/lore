// pretty_id.go — strict ID validator.
//
// id and the "T-19" / "M-3" pretty forms were removed; every CLI
// argument that takes an entity ID must now be the opaque <prefix>_<32hex>
// form. This function validates that and returns the ID unchanged, or an
// error referring the user to `list` to discover real IDs.
package main

import (
	"context"
	"fmt"

	"dbent/gen/ent"
	"saas/pkg/aicoder/ids"
)

func resolvePrettyID(_ context.Context, _ *ent.Client, idArg string) (string, error) {
	if err := ids.ValidateAny(idArg); err != nil {
		return "", fmt.Errorf("invalid id %q: %w (use `lore <kind> list` to find the real id)", idArg, err)
	}
	return idArg, nil
}
