package rowfs

// projectformat.go — a project's format is the rule, never the binary's
// (round "formatpin", decision D12). The format of a project is what its
// committed .lore/data/_meta.json records:
//
//	"format": 2            the format the project's rows are written in
//	"migrated_by": "…"     the person who ran `lore migrate run` that raised
//	                       it, "Name <email>" (never an agent: T-1)
//	"migrated_via": "…"    the agent the person ran it through, when one
//	                       did ("claude_code (agent:auto/…)"); audit only,
//	                       as the Lore-Agent trailer of a commit
//	"migrated_at": "…"     when (RFC 3339, UTC)
//
// A _meta.json without "format" says it the older way: min_lore_version
// set means an earlier 0.1.18 build migrated it (format 2); neither key
// leaves the answer to the rows (wire.ProjectFormatAt: v1 rows → format
// 1, else 2). The keys sit beside the format declaration (format.go) and
// every other key of the file is kept byte for byte.

import (
	"encoding/json"
	"path/filepath"
	"strconv"

	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Keys of the project format (file format: they must not change).
const (
	MetaKeyFormat      = "format"
	MetaKeyMigratedBy  = "migrated_by"
	MetaKeyMigratedVia = "migrated_via"
	MetaKeyMigratedAt  = "migrated_at"
)

// The project formats.
const (
	// ProjectFormatUnknown: the file does not say (no "format", no
	// min_lore_version); the rows decide (wire.ProjectFormatAt).
	ProjectFormatUnknown = 0
	// ProjectFormatV017 is the format lore 0.1.17 writes (rows at _v: 1).
	ProjectFormatV017 = 1
	// ProjectFormatCurrent is the format this lore writes (rows at _v: 2).
	ProjectFormatCurrent = 2
)

// ProjectFormat is what a project's _meta.json records about its format.
type ProjectFormat struct {
	// Format is the project's format (ProjectFormat*); Unknown when the
	// file says nothing.
	Format int `json:"format"`
	// Recorded is true when the "format" key itself is in the file (not
	// derived from min_lore_version or the rows).
	Recorded bool `json:"recorded"`
	// MigratedBy and MigratedAt are the person who ran the migration that
	// raised the format ("Name <email>"), and when ("" when not recorded).
	MigratedBy string `json:"migrated_by,omitempty"`
	MigratedAt string `json:"migrated_at,omitempty"`
	// MigratedVia is the agent the person ran it through ("" when the
	// person ran it themselves): an audit field, never shown as who.
	MigratedVia string `json:"migrated_via,omitempty"`
	// MinLoreVersion is the file's min_lore_version ("" when unset).
	MinLoreVersion string `json:"min_lore_version,omitempty"`
}

// FormatName names a project format for people: "the lore 0.1.17 format
// (format 1)".
func FormatName(f int) string {
	switch f {
	case ProjectFormatV017:
		return "the lore 0.1.17 format (format 1)"
	case ProjectFormatCurrent:
		return "the lore 0.1.18 format (format 2)"
	case ProjectFormatUnknown:
		return "an unknown format"
	}
	return "format " + strconv.Itoa(f) + " (written by a newer lore)"
}

// ReadProjectFormat reads the project format of the checkout at top (the
// folder holding .lore). A missing _meta.json or one without the keys is
// Format Unknown; a file that does not parse, or a key of the wrong shape,
// fails closed (E_OLD_LORE, as the format declaration).
func ReadProjectFormat(top string) (ProjectFormat, error) {
	if err := checkChain(top, RepoMetaPath); err != nil {
		return ProjectFormat{}, err
	}
	d, doc, exists, err := readFormatFile(filepath.Join(top, LoreDir, DataDir, MetaFileName), RepoMetaPath)
	if err != nil || !exists {
		return ProjectFormat{}, err
	}
	return projectFormatOf(d, doc)
}

// ParseProjectFormat is ReadProjectFormat over the bytes of a _meta.json
// read from somewhere other than the work tree: the copy a fetched branch
// holds (`git show origin/main:.lore/data/_meta.json`), so a stale
// checkout can tell that its team already migrated (K-11).
func ParseProjectFormat(doc []byte) (ProjectFormat, error) {
	o, err := decodeObject(doc)
	if err != nil {
		return ProjectFormat{}, unreadableDoc(RepoMetaPath, doc)
	}
	d, err := decodeFormat(o, RepoMetaPath)
	if err != nil {
		return ProjectFormat{}, err
	}
	return projectFormatOf(d, doc)
}

// projectFormatOf reads the format keys of a decoded _meta.json.
func projectFormatOf(d formatgate.Declared, doc []byte) (ProjectFormat, error) {
	o, err := decodeObject(doc)
	if err != nil {
		return ProjectFormat{}, err
	}
	pf := ProjectFormat{MinLoreVersion: d.MinLoreVersion}
	if raw, ok := o[MetaKeyFormat]; ok {
		if jerr := json.Unmarshal(raw, &pf.Format); jerr != nil || pf.Format < ProjectFormatV017 {
			return ProjectFormat{}, unreadableMeta("format is not a positive number")
		}
		pf.Recorded = true
	} else if d.MinLoreVersion != "" {
		pf.Format = ProjectFormatCurrent
	}
	for key, dst := range map[string]*string{MetaKeyMigratedBy: &pf.MigratedBy, MetaKeyMigratedVia: &pf.MigratedVia, MetaKeyMigratedAt: &pf.MigratedAt} {
		if raw, ok := o[key]; ok {
			if jerr := json.Unmarshal(raw, dst); jerr != nil {
				return ProjectFormat{}, unreadableMeta(key + " is not a string")
			}
		}
	}
	return pf, nil
}

// ProjectMigration is who raised a project's format and when
// (WriteProjectFormat): By is the person ("Name <email>"), Via the agent
// they ran it through ("" for none), At the RFC 3339 time. An empty field
// is left out of the file.
type ProjectMigration struct {
	By, Via, At string
}

// WriteProjectFormat records format f, who raised it and when in the
// checkout's _meta.json (every other key kept) and returns the file's
// path relative to top. It is a .lore/data change like any row: committed
// with the rows it belongs to. A checkout without _meta.json is
// E_NOT_FOUND (run lore init first).
func WriteProjectFormat(top string, f int, m ProjectMigration) (string, error) {
	if err := checkChain(top, RepoMetaPath); err != nil {
		return "", err
	}
	full := filepath.Join(top, LoreDir, DataDir, MetaFileName)
	_, doc, exists, err := readFormatFile(full, RepoMetaPath)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", errs.New(errs.NotFound, "rowfs: "+RepoMetaPath+" does not exist").WithHint("run `lore init` in the repository first")
	}
	o, err := decodeObject(doc)
	if err != nil {
		return "", unreadableDoc(RepoMetaPath, doc)
	}
	o[MetaKeyFormat] = json.RawMessage(strconv.Itoa(f))
	for key, v := range map[string]string{MetaKeyMigratedBy: m.By, MetaKeyMigratedVia: m.Via, MetaKeyMigratedAt: m.At} {
		if v == "" {
			delete(o, key)
			continue
		}
		raw, merr := json.Marshal(v)
		if merr != nil {
			return "", errs.Wrap(errs.Internal, "rowfs: encode "+key, merr)
		}
		o[key] = raw
	}
	out, err := encodeObject(o, RepoMetaPath)
	if err != nil {
		return "", err
	}
	if err := writeAtomic(full, out, sharedModes); err != nil {
		return "", err
	}
	return RepoMetaPath, nil
}

// unreadableMeta is the fail-closed error of a project format key.
func unreadableMeta(why string) error { return formatgate.Unreadable(RepoMetaPath, why) }
