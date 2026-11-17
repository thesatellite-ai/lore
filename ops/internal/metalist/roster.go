package metalist

// RosterStore is the --store flag of every read of the org roster (team
// list|show, member list|show, team invite list, project member list,
// role list, actor contact list, contact-type list, actor period list).
// devco D-499: from a folder that is no repo, store clone or setup
// folder those reads had no way to name the org, while `rule list
// --store <org>` read it from anywhere; wire reads the org it names
// (wire/rosterstore.go). Embedded, so every roster read takes it from this
// one definition.
type RosterStore struct {
	Store string `json:"store,omitempty" flag:"store" alias:"to" desc:"read this org's roster: --store <org> (default the org of this folder)"`
}
