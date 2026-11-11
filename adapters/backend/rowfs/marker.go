package rowfs

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// MarkerFile is the store marker at a store's root (spec 6.1, F12): its
// presence is what makes a folder or repo a lore store.
const MarkerFile = "lore-store.yaml"

// MarkerVersion is the marker format written by this binary (v0.1.17 wrote
// 1 without an id; a marker without an id is read with a zero ID and the
// migration assigns one).
const MarkerVersion = 2

// maxMarkerSize caps lore-store.yaml: a few lines; anything bigger is not
// a marker.
const maxMarkerSize = 64 << 10

// Visibility says whether a store's content may appear in a customer org's
// committed files (spec 7.3, CFG4).
type Visibility string

// The visibilities.
const (
	// VisibilityInternal (the default) renders only into private,
	// uncommitted files.
	VisibilityInternal Visibility = "internal"
	// VisibilityShareable may appear in committed renders.
	VisibilityShareable Visibility = "shareable"
)

// Visibilities lists every Visibility, for validation and help text.
var Visibilities = []Visibility{VisibilityInternal, VisibilityShareable}

// Valid reports a known visibility.
func (v Visibility) Valid() bool { return slices.Contains(Visibilities, v) }

// Marker is lore-store.yaml.
//
// Invariant after ReadMarker: Version >= 1 and Visibility is valid (an
// absent visibility reads as VisibilityInternal, fail closed).
type Marker struct {
	Version    int
	ID         ids.ID[ids.Store] // zero only for a v0.1.17 marker
	Visibility Visibility
	Created    string // YYYY-MM-DD, informational
	OrgFolder  string // the suggested company-wide folder, informational
	// Epoch counts the compactions of the store's history (`lore store
	// compact`): a clone whose history carries a lower epoch re-fetches
	// instead of merging. 0 = never compacted.
	Epoch int
	// Push holds the store's shared push and compaction settings as
	// written (storepush.Raw; set by an org admin with `lore store set`).
	// They are read leniently: a bad value is reported by `lore store
	// show` and `lore doctor`, and the store pushes as mode now.
	Push storepush.Raw
}

// MarkerDateLayout is the created: date of a marker (YYYY-MM-DD).
const MarkerDateLayout = "2006-01-02"

// NewMarker is the marker `lore store init` writes: a new sto_ id,
// visibility internal, created now (UTC date), the org folder suggestion
// FolderOrg. Every first write to a store with no marker writes this one,
// so a store is never left holding lore files without its marker.
func NewMarker(now time.Time) (Marker, error) {
	id, err := ids.New[ids.Store]()
	if err != nil {
		return Marker{}, errs.Wrap(errs.Internal, "rowfs: new store id", err)
	}
	return Marker{Version: MarkerVersion, ID: id, Visibility: VisibilityInternal, Created: now.UTC().Format(MarkerDateLayout), OrgFolder: FolderOrg}, nil
}

// markerWire is the YAML shape; field names are a file format.
type markerWire struct {
	LoreStore  int    `yaml:"lore_store"`
	ID         string `yaml:"id,omitempty"`
	Visibility string `yaml:"visibility,omitempty"`
	Created    string `yaml:"created,omitempty"`
	OrgFolder  string `yaml:"org_folder,omitempty"`
	Epoch      int    `yaml:"epoch,omitempty"`
	// The storepush keys (storepush.Key*).
	PushMode     string `yaml:"push,omitempty"`
	PushEvery    string `yaml:"push_every,omitempty"`
	Compact      string `yaml:"compact,omitempty"`
	CompactAfter string `yaml:"compact_after,omitempty"`
}

// ParseMarker parses marker bytes.
func ParseMarker(b []byte) (Marker, error) {
	if len(b) > maxMarkerSize {
		return Marker{}, errs.New(errs.ConfigInvalid, "rowfs: "+MarkerFile+" is too large")
	}
	var w markerWire
	if err := yamlerr.Decode(b, &w, yamlerr.Lenient); err != nil { // line + key, never a Go type
		return Marker{}, errs.Wrap(errs.ConfigInvalid, "rowfs: "+MarkerFile+" is not valid: "+err.Error(), err)
	}
	if w.LoreStore < 1 {
		return Marker{}, errs.New(errs.ConfigInvalid, "rowfs: "+MarkerFile+": lore_store must be a positive version")
	}
	if w.LoreStore > MarkerVersion {
		return Marker{}, errs.New(errs.OldLore, "rowfs: "+MarkerFile+" was written by a newer lore")
	}
	if w.Epoch < 0 {
		return Marker{}, errs.New(errs.ConfigInvalid, "rowfs: "+MarkerFile+": epoch must not be negative")
	}
	m := Marker{Version: w.LoreStore, Visibility: Visibility(w.Visibility), Created: w.Created, OrgFolder: w.OrgFolder,
		Epoch: w.Epoch, Push: storepush.Raw{Push: w.PushMode, PushEvery: w.PushEvery, Compact: w.Compact, CompactAfter: w.CompactAfter}}
	if m.Visibility == "" {
		m.Visibility = VisibilityInternal
	}
	if !m.Visibility.Valid() {
		return Marker{}, errs.New(errs.ConfigInvalid, "rowfs: "+MarkerFile+": visibility must be internal or shareable")
	}
	if w.ID != "" {
		id, err := ids.Parse[ids.Store](w.ID)
		if err != nil {
			return Marker{}, errs.Wrap(errs.ConfigInvalid, "rowfs: "+MarkerFile+": id is not a sto_ id", err)
		}
		m.ID = id
	}
	return m, nil
}

// Encode renders the marker as YAML with Version set to MarkerVersion.
func (m Marker) Encode() ([]byte, error) {
	if m.ID.IsZero() {
		return nil, errs.New(errs.InvalidInput, "rowfs: a new marker needs a store id")
	}
	if m.Visibility == "" {
		m.Visibility = VisibilityInternal
	}
	if !m.Visibility.Valid() {
		return nil, errs.New(errs.InvalidInput, "rowfs: visibility must be internal or shareable")
	}
	w := markerWire{LoreStore: MarkerVersion, ID: m.ID.String(), Visibility: string(m.Visibility), Created: m.Created, OrgFolder: m.OrgFolder,
		Epoch: m.Epoch, PushMode: m.Push.Push, PushEvery: m.Push.PushEvery, Compact: m.Push.Compact, CompactAfter: m.Push.CompactAfter}
	b, err := yaml.Marshal(w)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "rowfs: encode "+MarkerFile, err)
	}
	return b, nil
}

// ReadMarker reads <dir>/lore-store.yaml; a missing marker is
// store.ErrNotAStore (E_NOT_A_STORE).
func ReadMarker(dir string) (Marker, error) {
	b, err := readCapped(filepath.Join(dir, MarkerFile), MarkerFile)
	if errors.Is(err, fs.ErrNotExist) {
		return Marker{}, errs.Wrap(errs.NotAStore, "rowfs: no "+MarkerFile+" at the store root", store.ErrNotAStore).
			WithHint("an empty repo or folder becomes a lore store with lore store init <name> <url>; else point the url at an existing lore store")
	}
	if err != nil {
		return Marker{}, err
	}
	return ParseMarker(b)
}

// WriteMarker writes <dir>/lore-store.yaml atomically.
func WriteMarker(dir string, m Marker) error {
	b, err := m.Encode()
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, MarkerFile), b, privateModes)
}
