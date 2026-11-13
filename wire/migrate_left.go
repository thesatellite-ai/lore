package wire

// migrate_left.go — what `lore migrate run` rewrites, and the files it
// leaves at `_v: 1`, named (owner item T-2).
//
// A trial migration of a 0.1.17 repo (`lore migrate run --offline
// --no-push`) left 12 files at `_v: 1`: 8 actors, a project, a repo, an
// entity_tags link and the data root's _meta.json, and said nothing.
// Actors, projects and repos are core tables like any other (the 0.1.17
// writer that once kept them at v1 is gone with saas), so the run now
// rewrites every synced table the core defines (FormatTables); the rest is
// named with why it stays and what converts it:
//
//	v1 row of a FormatTables table, dry run   ──▶ rewritten by the run (not listed)
//	v1 row of a FormatTables table, real run  ──▶ listed: changed while the run ran
//	entity_tags row                           ──▶ listed: 0.1.17's tag links, no
//	                                              0.1.18 table; folded into tag_ids
//	                                              by the online run, the file stays
//	machine-local table row                   ──▶ listed: never a repo row; its
//	                                              rows are copied from lore.db
//	other table of the registry               ──▶ listed: not a table the run rewrites
//	any other table                           ──▶ listed: no table in this lore
//	_meta.json / _purged.json at "_v": 1      ──▶ listed: a folder file, not a row;
//	                                              1 is that file's own format

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/row"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/store"
)

var _ opsmigrate.LeftReporter = (*App)(nil)

// FormatTables is every table `lore migrate run` rewrites from `_v: 1` to
// `_v: 2`: CoreTables first (their order), then every other synced,
// non-machine-local table of the built-in registry by name (actors,
// projects, repos, …). Status counts the same tables as pending.
func FormatTables() []row.Table {
	out := CoreTables()
	reg, err := builtinRegistry()
	if err != nil {
		return out // the built-in registry is checked by its own tests
	}
	for _, def := range reg.Tables() {
		if def.Synced && !def.MachineLocal && def.Kind.IsZero() && !slices.Contains(out, def.Name) {
			out = append(out, def.Name)
		}
	}
	return out
}

// Reasons a file stays at `_v: 1` (LeftV1).
const (
	leftWhyEntityTagsOffline = "0.1.18 has no entity_tags table: `lore migrate run` without --offline folds these links into the tag_ids of the rows they tag; the files stay as 0.1.17 history (no lore reads them after that)"
	leftWhyEntityTags        = "0.1.18 has no entity_tags table: this run folded these links into the tag_ids of the rows they tag; the files stay as 0.1.17 history (no lore reads them after that)"
	leftWhyMachineLocal      = "a machine-local table: its rows never live in a shared store; `lore migrate run` copies them from .lore/lore.db into this machine's store, and these files can be removed"
	leftWhyNotRewritten      = "a table lore migrate run does not rewrite (not a synced table): its rows are read as they are; nothing converts them"
	leftWhyUnknown           = "no table of this name in this lore: nothing converts it (remove the files if no lore uses the table, or upgrade lore if a newer one wrote them)"
	leftWhyRaced             = "changed while the run ran (or could not be rewritten): run `lore migrate run` again"
	leftWhyFolderFile        = "a folder file, not a row: 1 is that file's own format, which this lore reads and writes as it is; nothing to convert"
)

// LeftV1 implements opsmigrate.LeftReporter: the v1 row files of the
// places run rewrote that it did not rewrite, grouped per store and table,
// plus the repo's folder files at "_v": 1.
func (a *App) LeftV1(ctx context.Context, tables []row.Table, repoOnly, dryRun bool) ([]opsmigrate.Left, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	reg, err := builtinRegistry()
	if err != nil {
		return nil, err
	}
	skip := map[store.Source]bool{}
	if !repoOnly {
		if skip, err = a.flattenedUnderDryRun(dryRun); err != nil {
			return nil, err
		}
	}
	var out []opsmigrate.Left
	names := st.eng.Stores()
	for i, b := range st.backends {
		if skip[names[i]] || (repoOnly && i > 0) {
			continue
		}
		rows, _, err := b.Read(ctx, "")
		if err != nil {
			return nil, err
		}
		byTable := map[row.Table][]string{}
		for _, sr := range rows {
			r, derr := sr.Decode()
			if derr != nil || r.SourceVersion >= row.FormatVersion {
				continue // an unreadable file is a read problem doctor names
			}
			if dryRun && slices.Contains(tables, sr.Table) {
				continue // the real run rewrites it
			}
			byTable[sr.Table] = append(byTable[sr.Table], sr.Table.String()+"/"+sr.ID.String())
		}
		for _, t := range slices.Sorted(maps.Keys(byTable)) {
			what := t.String()
			if i > 0 {
				what += " (store " + string(names[i]) + ")"
			}
			why := leftWhyUnknown
			switch def, ok := reg.Table(t); {
			case slices.Contains(tables, t):
				why = leftWhyRaced
			case t.String() == entityTagsTable && repoOnly:
				why = leftWhyEntityTagsOffline
			case t.String() == entityTagsTable:
				why = leftWhyEntityTags
			case ok && def.MachineLocal:
				why = leftWhyMachineLocal
			case ok:
				why = leftWhyNotRewritten
			}
			out = append(out, opsmigrate.Left{What: what, Table: t.String(), Files: byTable[t], Why: why})
		}
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		return nil, err
	}
	files, err := v1FolderFiles(top)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		out = append(out, opsmigrate.Left{What: f, Files: []string{f}, Why: leftWhyFolderFile})
	}
	return out, nil
}

// v1FolderFiles lists the repo's folder files (_meta.json, _purged.json
// of .lore/data and its folders) whose "_v" is 1, as slash paths from the
// repo top. A file that is not a JSON object is not listed (doctor names
// it).
func v1FolderFiles(top string) ([]string, error) {
	if top == "" {
		return nil, nil
	}
	data := filepath.Join(top, rowfs.LoreDir, rowfs.DataDir)
	var out []string
	err := filepath.WalkDir(data, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if p == data && errors.Is(werr, fs.ErrNotExist) {
				return filepath.SkipAll
			}
			return werr
		}
		if d.IsDir() || (d.Name() != rowfs.MetaFileName && d.Name() != rowfs.PurgedFileName) {
			return nil
		}
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			return err
		}
		var doc struct {
			V json.RawMessage `json:"_v"`
		}
		if json.Unmarshal(b, &doc) != nil || strings.TrimSpace(string(doc.V)) != "1" {
			return nil
		}
		rel, err := filepath.Rel(top, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}
