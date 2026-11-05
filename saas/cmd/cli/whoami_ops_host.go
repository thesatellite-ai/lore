// whoami_ops_host.go — whoamiHost, the machinery behind whoami.show
// (ops/whoami.Host, wire.Options.Whoami; PASS3 ONE-PATH, P3-ORG-G-3): the
// v0.1.17 org situation, identity chain and redaction of whoami_cmd.go,
// printed into a buffer (the human lines) and copied into the typed
// envelope data (--json, lore api).
package main

import (
	"bytes"
	"context"
	"os"
	"strings"

	opswhoami "github.com/thesatellite-ai/lore/ops/whoami"
)

// whoamiHost implements opswhoami.Host.
type whoamiHost struct{}

var _ opswhoami.Host = whoamiHost{}

// Whoami implements opswhoami.Host. P3-ORG-G-3: a resolution or identity
// error is the operation's error (same code and exit status as v0.1.17);
// the lines printed before it in v0.1.17 are not shown then.
func (whoamiHost) Whoami(ctx context.Context, in opswhoami.ShowIn) (opswhoami.Out, error) {
	dir, err := os.Getwd()
	if err != nil {
		return opswhoami.Out{}, hostErr(err)
	}
	s, err := gatherOrgSituation(ctx, dir)
	if err != nil {
		return opswhoami.Out{}, hostErr(err)
	}
	id, idErr := s.identity()
	red := newWhoamiRedactor(s, in.Share)
	if err := firstErr(s.ResolveErr, idErr); err != nil {
		return opswhoami.Out{}, hostErr(err)
	}
	var b bytes.Buffer
	printWhoami(&b, s, id, idErr, in.Explain, red)
	return opswhoami.Out{Data: whoamiOpData(whoamiData(s, id, idErr, red)), Lines: plainLines(b.String())}, nil
}

// SetMachineLabel implements opswhoami.Host.
func (whoamiHost) SetMachineLabel(ctx context.Context, label string) (opswhoami.MachineLabel, []string, error) {
	l, lines, err := setMachineLabel(ctx, label)
	return l, lines, hostErr(err)
}

// whoamiOpData copies the v0.1.17 JSON data into the operation's types
// (same keys, so --json keeps its shape; personal_store joins the v0.1.17
// folder list into one place, G-DECIDE-7).
func whoamiOpData(j whoamiJSON) opswhoami.Data {
	d := opswhoami.Data{Org: j.Org, MatchedBy: j.MatchedBy, Rule: j.Rule, RepoRole: string(j.RepoRole),
		Identity:  opswhoami.Identity{StableKey: j.Identity.StableKey, Name: j.Identity.Name, Kind: j.Identity.Kind, ActorID: j.Identity.ActorID, Source: j.Identity.Source, Session: j.Identity.Session},
		ProjectID: j.ProjectID, Remotes: j.Remotes, Personal: strings.Join(j.Personal, ", "), LinkStatus: j.LinkStatus, Warnings: j.Warnings, Error: j.Error, Machine: j.Machine,
		Stores: make([]opswhoami.Store, 0, len(j.Stores)), Trace: make([]opswhoami.Rule, 0, len(j.Trace))}
	for _, st := range j.Stores {
		d.Stores = append(d.Stores, opswhoami.Store{URL: st.URL, Branch: st.Branch, OrgFolder: st.OrgFolder})
	}
	for _, t := range j.Trace {
		d.Trace = append(d.Trace, opswhoami.Rule{Step: int(t.Step), Outcome: string(t.Outcome), Reason: t.Reason})
	}
	if a := j.Agent; a != nil {
		d.Agent = opswhoami.Agent{Tool: a.Tool, ToolSession: a.ToolSession, Version: a.Version, Model: a.Model, Signal: a.Signal,
			Outer: a.Outer, CI: a.CI, DeclaredHuman: a.DeclaredHuman, Flags: a.Flags, Sources: a.Sources}
	}
	return d
}
