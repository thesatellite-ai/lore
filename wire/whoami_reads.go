package wire

// whoami_reads.go — whoami says which store place this folder reads
// (devco D-596, owner use pass 2 weak 10). In a repo linked to a store
// folder and inside a folder of a store clone, whoami printed "stores …
// (org folder org)" and "links known 1 link(s) …" and never the one thing
// a person asks it: where do this folder's notes come from. The "reads"
// row answers it in the <org>/<folder> spelling every other command uses:
//
//	cwd in a store clone        ─▶ "<org>/<folder> (this folder of the store clone)"
//	                               (the top: "<org> (the top of the store clone)")
//	repo linked to folders      ─▶ "<org>/<folder> (this repo's link)", one per link
//	own repo a resource names   ─▶ "<org>/<folder> (its resource <name> names this repo; not linked)"
//	none of these               ─▶ no row
//
// The org folder in the stores row is a place too ("org folder
// <org>/<folder>"): its folder is often literally named "org", and
// "(org folder org)" read like a typo.

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// withReadsRows puts the reads rows in front of whoami's "links" row
// (after the stores and links they explain); at the end when there is
// no links row.
func withReadsRows(lines, reads []string) []string {
	if len(reads) == 0 {
		return lines
	}
	rows := make([]string, 0, len(reads))
	for i, r := range reads {
		label := ""
		if i == 0 {
			label = "reads"
		}
		rows = append(rows, fmt.Sprintf("%-*s %s", whoamiLabelWidth, label, r))
	}
	at := len(lines)
	for i, l := range lines {
		if strings.HasPrefix(l, fmt.Sprintf("%-*s ", whoamiLabelWidth, "links")) {
			at = i
			break
		}
	}
	return append(lines[:at:at], append(rows, lines[at:]...)...)
}

// whoamiReads are the values of whoami's "reads" rows for s, in order;
// none when the folder reads no store place (no org, not linked, not a
// store clone). Best effort like every whoami store read: what cannot be
// worked out is left out rather than failing the diagnostic.
func (w *standaloneWhoami) whoamiReads(ctx context.Context, s *orgcfg.Situation) []string {
	if sc, ok := storeHere(optsProjectDir(w.opts)); ok {
		if e, err := storeHereEntry(w.opts.Home, sc); err == nil {
			if sc.folder == rowfs.FolderRoot {
				return []string{string(e.Name) + " (the top of the store clone)"}
			}
			return []string{string(e.Name) + " (this folder of the store clone)"}
		}
		return nil
	}
	if s == nil || s.ResolveErr != nil || s.Result.Org == nil {
		return nil
	}
	var out []string
	for _, l := range s.Result.Links {
		out = append(out, orgPlace(s.Result.Org, l.StoreURL, l.Folder)+" (this repo's link)")
	}
	if len(out) > 0 || w.app == nil {
		return out
	}
	app := w.app.boundApp()
	if app == nil || UntouchedRepo(ctx, app.opts.RepoDir) == "" || linkedByRemote(ctx, app.opts) != "" {
		return nil
	}
	if org := ownOrgOf(ctx, app.opts); org != "" {
		if f, ok := app.resourceFolderOf(ctx, org); ok {
			return []string{f.target() + " (its resource " + f.Resource + " names this repo; not linked: lore org link --org " + f.Org + " --project " + f.Folder + " --own)"}
		}
	}
	return nil
}

// orgPlace is the <org>/<folder> label of folder in the org store at
// storeURL: the org's name for the store holding the org folder (or when
// the url is none of the org's stores), the store's short name for the
// org's other stores, as store folder list and the write lines label it.
func orgPlace(org *orgconf.Org, storeURL, folder string) string {
	name := org.Name
	if want := normStore(storeURL); want != "" && len(org.Stores) > 1 {
		for _, sc := range org.Stores {
			if sc.OrgFolder == "" && normStore(sc.URL) == want {
				name = sc.ShortName()
				break
			}
		}
	}
	return name + targetSep + folder
}

// normStore is a store url as links record it (repourl.Normalize, an ssh
// alias resolved), "" when it is no url lore can read.
func normStore(raw string) string {
	n, err := repourl.Normalize(raw)
	if err != nil {
		return ""
	}
	return orgcfg.CanonicalRemote(n)
}
