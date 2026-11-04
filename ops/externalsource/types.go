package externalsource

import (
	"strings"
	"time"
)

// Input and output structs of the external-source operations. Struct tags
// are the op contract (op/fields.go). --json keeps v0.1.17's envelope.

// KindList is v0.1.17's list envelope kind (constants.KindExternalSourceList).
const KindList = "external-source.list"

// AddIn is the input of external_source.add.
type AddIn struct {
	Name    string `json:"name" flag:"name" desc:"source name (required)"`
	Kind    string `json:"kind" flag:"kind" desc:"source kind (required, e.g. github | notion | gitlab)"`
	Config  string `json:"config,omitempty" flag:"config" desc:"JSON config blob"`
	Project string `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
	To      string `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
}

// ListIn is the input of external_source.list.
type ListIn struct {
	Project string `json:"project,omitempty" flag:"project" desc:"only sources of this project (prj_ id or name)"`
	Store   string `json:"store,omitempty" flag:"store" desc:"only sources of this store (default every attached store)"`
}

// ListOut is v0.1.17's list envelope.
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
}

// States v0.1.17's list line prints for enabled.
const (
	stateOn  = "on"
	stateOff = "off"
)

// Text prints v0.1.17's "[on|off] <name> (<kind>) <id>" lines, or
// "(no external sources)".
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no external sources)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		state := stateOff
		if r.Enabled {
			state = stateOn
		}
		b.WriteString("[" + state + "] " + r.Name + " (" + r.Kind + ") " + r.ID + "\n")
	}
	return b.String()
}

// Record is one external source in v0.1.17's --json form: ent
// ExternalSource's keys in its order (enabled omitted when false, as ent
// printed it). TagIDs (O11) and Store (`_source`) are 0.1.18 additions.
type Record struct {
	ID         string    `json:"id,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
	ProjectID  string    `json:"project_id,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Name       string    `json:"name,omitempty"`
	ConfigJSON string    `json:"config_json,omitempty"`
	Enabled    bool      `json:"enabled,omitempty"`
	TagIDs     []string  `json:"tag_ids,omitempty"`
	Store      string    `json:"_source,omitempty"`
}

// IDIn is the input of enable and disable.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the external source id (ext_…)"`
}
