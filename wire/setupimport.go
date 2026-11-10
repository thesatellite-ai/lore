package wire

// setupimport.go — setup.import_scan's Prober side
// (opssetup.ImportScanner): adapters/importscan over this App's lore home
// and the user's home, its answer mapped to the op's JSON types. Read-only
// (the adapter copies machine.db before reading it).

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/importscan"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

var _ opssetup.ImportScanner = setupProber{}

// ImportScan implements opssetup.ImportScanner.
func (sp setupProber) ImportScan(ctx context.Context, in opssetup.ImportScanIn) (opssetup.ImportScanOut, error) {
	res, err := importscan.Scan(ctx, importscan.Options{
		LoreHome: sp.a.opts.Home,
		Home:     userHome(SetupQuery{}),
		Folders:  in.Folders,
		Depth:    in.Depth,
	})
	if err != nil {
		return opssetup.ImportScanOut{}, err
	}
	return importScanOut(res), nil
}

// importScanOut maps the adapter's result to the op's output.
func importScanOut(res importscan.Result) opssetup.ImportScanOut {
	out := opssetup.ImportScanOut{
		Proposals: make([]opssetup.ImportProposal, 0, len(res.Proposals)),
		Sources:   make([]opssetup.ImportSource, 0, len(res.Sources)),
		Repos:     make([]opssetup.ImportRepo, 0, len(res.Repos)),
		Covered:   res.Covered,
		Truncated: res.Truncated,
	}
	for _, s := range res.Sources {
		out.Sources = append(out.Sources, opssetup.ImportSource{Kind: s.Kind, Path: s.Path, State: string(s.State), Detail: s.Detail})
	}
	for _, r := range res.Repos {
		ir := opssetup.ImportRepo{Root: r.Root, From: r.From, Org: r.Org}
		for _, s := range r.Stores {
			ir.Stores = append(ir.Stores, s.URL)
		}
		out.Repos = append(out.Repos, ir)
	}
	for _, p := range res.Proposals {
		ip := opssetup.ImportProposal{Setup: p.Entry, Repos: p.Repos, Fields: make([]opssetup.ImportField, 0, len(p.Fields))}
		for _, f := range p.Fields {
			ip.Fields = append(ip.Fields, opssetup.ImportField(f))
		}
		out.Proposals = append(out.Proposals, ip)
	}
	return out
}
