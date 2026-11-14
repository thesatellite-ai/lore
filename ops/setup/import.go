package setup

// import.go — setup.import_scan and setup.import (owner request "Import
// from existing lore", fix-all item 2).
//
//	page ─▶ setup.import_scan ─▶ Prober (ImportScanner) ─▶ adapters/importscan (read-only)
//	     ◀─ proposals: a setup entry + every field's file and line
//	page ─▶ setup.import {setups} ─▶ Store.PutSetup per entry (the setup.edit write path)
//
// The scan writes nothing (adapters/importscan TestScanWritesNothing). The
// import takes the entries themselves, so the page can import a proposal
// as previewed (or edited), and the "create a setup from this repo"
// banner can import its own entry the same way. Each entry is validated
// with the whole list by PutSetup; a taken name or an invalid entry is
// reported as skipped and the others still import.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Op names (page allowlist).
const (
	OpImportScan = "setup.import_scan"
	OpImport     = "setup.import"
)

// ImportScanIn is setup.import_scan's input.
type ImportScanIn struct {
	Folders []string `json:"folders,omitempty" flag:"folder" desc:"also walk these folders for lore repos (repeat)"`
	Depth   int      `json:"depth,omitempty" flag:"depth" min:"1" desc:"how deep the folder walk goes (default 4, at most 8)"`
}

// ImportSource is one file or folder the scan read: state is found,
// missing, empty, malformed or unreadable.
type ImportSource struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// ImportField is one proposed value and the file and line it came from
// (line 0: the source has no lines).
type ImportField struct {
	Field string `json:"field"`
	Value string `json:"value"`
	File  string `json:"file,omitempty"`
	Line  int    `json:"line,omitempty"`
	Note  string `json:"note,omitempty"`
}

// ImportRepo is one lore repo found (from machine.db or the folder walk).
type ImportRepo struct {
	Root   string   `json:"root"`
	From   string   `json:"from"`
	Org    string   `json:"org,omitempty"`
	Stores []string `json:"stores,omitempty"`
}

// ImportProposal is one setup to import.
type ImportProposal struct {
	Setup  onboard.SetupEntry `json:"setup"`
	Fields []ImportField      `json:"fields"`
	Repos  []string           `json:"repos,omitempty"`
}

// ImportScanOut is setup.import_scan's output.
type ImportScanOut struct {
	Proposals []ImportProposal `json:"proposals"`
	Sources   []ImportSource   `json:"sources"`
	Repos     []ImportRepo     `json:"repos"`
	// Covered names proposals left out because a setup already covers them.
	Covered   []string `json:"covered,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	// Found is what the banner names (importFound over the sources and
	// the solo proposal).
	Found ImportFound `json:"found"`
	// Banner is Home's one line about the proposals (onboard.ImportBanner),
	// "" when nothing is proposed; BannerKey its dismiss key; Dismissed is
	// set when setup.import_dismiss hid it for these values.
	Banner    string `json:"banner"`
	BannerKey string `json:"banner_key"`
	Dismissed bool   `json:"dismissed"`
}

// ImportFound is what the scan found that the banner names.
type ImportFound struct {
	// LegacyFiles are files of a lore before setups (identity.yaml/.toml,
	// never their *.migrated backups), written with ~; empty when there are none.
	LegacyFiles []string `json:"legacy_files,omitempty"`
	// Identity is config.yaml's identity name (else key), else a legacy file's.
	Identity string `json:"identity,omitempty"`
	// PersonalStore is config.yaml's personal.store.
	PersonalStore string `json:"personal_store,omitempty"`
	// Config is config.yaml's path, written with ~.
	Config string `json:"config,omitempty"`
}

// ImportScanner is the optional Prober upgrade setup.import_scan needs
// (wire's Prober has it). Invariant: writes nothing.
type ImportScanner interface {
	ImportScan(ctx context.Context, in ImportScanIn) (ImportScanOut, error)
}

// ImportIn is setup.import's input: the entries to write.
type ImportIn struct {
	Setups ImportSetups `json:"setups" flag:"setups" desc:"the setup entries to write, as a JSON list (setup.import_scan proposes them)"`
}

// ImportSetups is setup.import's list of entries. It is one text value to
// the op registry (a --setups flag holding a JSON list; op.Define takes
// only flat item fields) and a plain JSON list on the wire, the way
// onboard.Answers carries step answers.
type ImportSetups []onboard.SetupEntry

// MarshalText implements encoding.TextMarshaler (the JSON list).
func (s ImportSetups) MarshalText() ([]byte, error) {
	if s == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]onboard.SetupEntry(s))
}

// UnmarshalText implements encoding.TextUnmarshaler (the JSON list).
func (s *ImportSetups) UnmarshalText(b []byte) error {
	var l []onboard.SetupEntry
	if len(strings.TrimSpace(string(b))) > 0 {
		if err := json.Unmarshal(b, &l); err != nil {
			return errs.Wrap(errs.InvalidInput, "setup import: setups is not a JSON list of setup entries", err)
		}
	}
	*s = l
	return nil
}

// MarshalJSON keeps the JSON form a list (not a quoted string).
func (s ImportSetups) MarshalJSON() ([]byte, error) { return s.MarshalText() }

// UnmarshalJSON reads the list, or a string holding it (the OpenAPI
// document describes a text field as a string, so a client may send it so).
func (s *ImportSetups) UnmarshalJSON(b []byte) error {
	if t := strings.TrimSpace(string(b)); strings.HasPrefix(t, `"`) {
		var inner string
		if err := json.Unmarshal(b, &inner); err != nil {
			return errs.Wrap(errs.InvalidInput, "setup import: setups is not a JSON string", err)
		}
		return s.UnmarshalText([]byte(inner))
	}
	return s.UnmarshalText(b)
}

// ImportSkip is an entry that was not written, and why.
type ImportSkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// ImportOut is setup.import's output.
type ImportOut struct {
	Imported []string     `json:"imported"`
	Skipped  []ImportSkip `json:"skipped"`
}

// ImportScan is setup.import_scan.
func (e engine) ImportScan(ctx context.Context, in ImportScanIn) (ImportScanOut, error) {
	pr, err := e.env.SetupProber(ctx)
	if err != nil {
		return ImportScanOut{}, err
	}
	s, ok := pr.(ImportScanner)
	if !ok {
		return ImportScanOut{}, errs.New(errs.Unsupported, "this lore cannot scan for an existing install").
			WithHint("create the setups with the steps instead")
	}
	out, err := s.ImportScan(ctx, in)
	if err != nil {
		return out, err
	}
	out.Found = importFound(out)
	f := onboard.BannerFacts{Legacy: len(out.Found.LegacyFiles) > 0, LegacyFiles: out.Found.LegacyFiles,
		IdentityName: out.Found.Identity, PersonalStore: out.Found.PersonalStore, ConfigPath: out.Found.Config}
	for _, p := range out.Proposals {
		f.Proposals = append(f.Proposals, p.Setup.Name)
		if why := renamedWhy(p); why != "" {
			if f.Renamed == nil {
				f.Renamed = map[string]string{}
			}
			f.Renamed[p.Setup.Name] = why
		}
	}
	f.Covered = out.Covered
	out.Banner = onboard.ImportBanner(f)
	if out.Banner != "" {
		out.BannerKey = onboard.ImportBannerKey(f)
		if d, ok := e.env.(ImportDismisser); ok {
			out.Dismissed = d.ImportDismissed(ctx, out.BannerKey)
		}
	}
	return out, nil
}

// importFieldName is the proposal field that holds the setup name; its
// note says why a proposal was renamed (adapters/importscan nameNote).
const importFieldName = "name"

// renamedWhy is why p carries another name than its own ("" when it was
// not renamed).
func renamedWhy(p ImportProposal) string {
	for _, f := range p.Fields {
		if f.Field == importFieldName {
			return f.Note
		}
	}
	return ""
}

// Import is setup.import.
func (e engine) Import(ctx context.Context, in ImportIn) (ImportOut, error) {
	if len(in.Setups) == 0 {
		return ImportOut{}, errs.New(errs.InvalidInput, "setup import: no setups given").
			WithHint("lore setup import-scan --json lists the proposals")
	}
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return ImportOut{}, err
	}
	out := ImportOut{Imported: []string{}, Skipped: []ImportSkip{}}
	for _, s := range in.Setups {
		s.Name = strings.TrimSpace(s.Name)
		if !onboard.ValidSetupName(s.Name) {
			out.Skipped = append(out.Skipped, ImportSkip{Name: s.Name, Reason: "not a valid setup name"})
			continue
		}
		cfg, err := st.Config(ctx)
		if err != nil {
			return out, err
		}
		if _, taken := cfg.Setup(s.Name); taken {
			out.Skipped = append(out.Skipped, ImportSkip{Name: s.Name, Reason: "a setup of this name exists"})
			continue
		}
		if s.Default && hasDefault(cfg) {
			s.Default = false // the existing default stays (3.2: at most one)
		}
		if _, err := st.PutSetup(ctx, s); err != nil {
			if errs.CodeOf(err) == errs.ConfigInvalid {
				out.Skipped = append(out.Skipped, ImportSkip{Name: s.Name, Reason: err.Error()})
				continue
			}
			return out, err
		}
		out.Imported = append(out.Imported, s.Name)
	}
	return out, nil
}

// ImportOps returns setup.import_scan and setup.import bound to env; wire
// registers them next to GuidedOps.
func ImportOps(env Onboard) []op.Entry {
	e := newEngine(env, onboardCatalog{})
	return []op.Entry{
		op.Define(op.Spec{Name: OpImportScan, Local: true, RESTNoun: restNoun, Permission: op.PermRead, Access: op.AccessRead,
			Summary:  "Read an existing lore install (config.yaml, old identity files, known repos, stores.yaml) and propose setups to import; writes nothing",
			Errors:   errsPort,
			Examples: []op.Example{{Title: "what can be imported", CLI: "lore setup import-scan --folder ~/code --json", Want: ""}}},
			e.ImportScan),
		op.Define(op.Spec{Name: OpImport, Local: true, RESTNoun: restNoun, Permission: op.PermWrite, Access: op.AccessRead,
			Summary:  "Write setup entries (as setup.import_scan proposes them) to config.yaml; a taken name is skipped",
			Errors:   with(),
			Examples: []op.Example{{Title: "import a proposal", CLI: `lore setup import --setups '[{"name":"personal","kind":"solo"}]'`, Want: ""}}},
			e.Import),
	}
}

// Source kinds and states the banner reads (adapters/importscan names).
const (
	importKindConfig    = "config"
	importKindLegacy    = "identity_legacy"
	importStateFound    = "found"
	importConfigSuffix  = "/.lore/config.yaml"
	importHomeShorthand = "~"
)

// importFound is what the banner names: the legacy identity files found
// (a real lore before setups), and the identity and personal store of the
// solo proposal, so the banner never names a value no proposal uses.
func importFound(out ImportScanOut) ImportFound {
	var f ImportFound
	home := ""
	for _, src := range out.Sources {
		if src.Kind == importKindConfig {
			if h, ok := strings.CutSuffix(src.Path, importConfigSuffix); ok {
				home = h
			}
			f.Config = src.Path
		}
	}
	tilde := func(p string) string {
		if rest, ok := strings.CutPrefix(p, home+"/"); ok && home != "" {
			return importHomeShorthand + "/" + rest
		}
		return p
	}
	f.Config = tilde(f.Config)
	for _, src := range out.Sources {
		if src.Kind == importKindLegacy && src.State == importStateFound {
			f.LegacyFiles = append(f.LegacyFiles, tilde(src.Path))
		}
	}
	for _, p := range out.Proposals {
		if p.Setup.Kind != onboard.KindSolo {
			continue
		}
		f.Identity = p.Setup.Identity.Name
		if f.Identity == "" {
			f.Identity = p.Setup.Identity.Key
		}
		f.PersonalStore = p.Setup.PersonalStore
	}
	return f
}
