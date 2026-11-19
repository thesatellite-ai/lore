package wire

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opsmember "github.com/thesatellite-ai/lore/ops/member"
	"github.com/thesatellite-ai/lore/ops/profilestore"
)

var _ opsinfra.ProjectMemberLister = (*App)(nil)

// ProjectMembers implements opsinfra.ProjectMemberLister (devco D-492):
// project show names the members project member list prints, one
// "@handle role, role" line per current stint, by running that list on
// the roster (the members live in the org roster, not in the project's
// store). nil means the roster could not be read here (show leaves the
// members out, never fails); an empty list means none.
func (a *App) ProjectMembers(ctx context.Context, projectID string) ([]string, error) {
	e, ok := a.Registry().Lookup(profilestore.OpMemberList)
	if !ok {
		return nil, nil
	}
	body, err := json.Marshal(map[string]string{"project": projectID})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "project show: encode the member list input", err)
	}
	c, _ := engine.CallerOf(ctx)
	res, err := a.Run(ctx, e, op.JSONDecoder{Body: body}, c)
	if err != nil {
		// no roster here, or one this place may not read (a client repo:
		// E_LEAK_BLOCKED): show the project without its members;
		// `lore project member list` says why
		return nil, nil
	}
	raw, err := res.JSON()
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "project show: read the member list", err)
	}
	var out opsmember.MemberListOut
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, errs.Wrap(errs.Internal, "project show: decode the member list", err)
	}
	lines := make([]string, 0, len(out.Members)) // non-nil: the roster was read
	for _, m := range out.Members {
		if m.EndedOn != "" {
			continue
		}
		lines = append(lines, strings.TrimSpace(m.Actor+"  "+strings.Join(m.Roles, ", ")))
	}
	return lines, nil
}
