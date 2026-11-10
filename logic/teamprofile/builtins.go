package teamprofile

import "github.com/thesatellite-ai/lore/schema"

// TS-2: the starter contact types (TP-1) and roles (TP-8) are compiled in,
// never seeded by a write. The effective list is the built-ins with every
// live row laid over them by name (a row named github changes the
// built-in's label, template or retired; a new name adds a type), the same
// shape as the secret rules' built-in pack. Seeding would write rows from a
// read path, make one add/add merge per seeding clone, and leave an empty
// list on a clone that has not written yet.

// ContactType is one contact type: a built-in or a contact_types row.
type ContactType struct {
	Name  string
	Label string
	// Kind is one of ContactKinds: it picks Normalize's rule.
	Kind string
	// Identity: contacts of this type may be flagged for identity (TP-4).
	// A row may not change Kind or Identity of a built-in identity type
	// (github, gitlab, email): identity matching depends on them.
	Identity bool
	// URLTemplate links a value in `lore actor show`; {value} is replaced.
	URLTemplate string
	Retired     bool
}

// Role is one project role: a built-in or a roles row (TP-8).
type Role struct {
	Name        string
	Label       string
	Description string
	Retired     bool
}

// Built-in contact type names (TP-1).
const (
	ContactTypeGitHub   = "github"
	ContactTypeGitLab   = "gitlab"
	ContactTypeEmail    = "email"
	ContactTypePhone    = "phone"
	ContactTypeLinkedIn = "linkedin"
	ContactTypeX        = "x"
	ContactTypeSlack    = "slack"
	ContactTypeDiscord  = "discord"
	ContactTypeWebsite  = "website"
)

// URLValue is the placeholder of a URLTemplate.
const URLValue = "{value}"

// BuiltinContactTypes returns the compiled-in contact types (TP-1), a
// fresh slice the caller may change.
func BuiltinContactTypes() []ContactType {
	return []ContactType{
		{Name: ContactTypeGitHub, Label: "GitHub", Kind: schema.ContactKindHandle, Identity: true, URLTemplate: "https://github.com/" + URLValue},
		{Name: ContactTypeGitLab, Label: "GitLab", Kind: schema.ContactKindHandle, Identity: true, URLTemplate: "https://gitlab.com/" + URLValue},
		{Name: ContactTypeEmail, Label: "Email", Kind: schema.ContactKindEmail, Identity: true, URLTemplate: "mailto:" + URLValue},
		{Name: ContactTypePhone, Label: "Phone", Kind: schema.ContactKindPhone, URLTemplate: "tel:" + URLValue},
		{Name: ContactTypeLinkedIn, Label: "LinkedIn", Kind: schema.ContactKindHandle, URLTemplate: "https://www.linkedin.com/in/" + URLValue},
		{Name: ContactTypeX, Label: "X", Kind: schema.ContactKindHandle, URLTemplate: "https://x.com/" + URLValue},
		{Name: ContactTypeSlack, Label: "Slack", Kind: schema.ContactKindText},
		{Name: ContactTypeDiscord, Label: "Discord", Kind: schema.ContactKindText},
		{Name: ContactTypeWebsite, Label: "Website", Kind: schema.ContactKindURL, URLTemplate: URLValue},
	}
}

// Built-in role names (TP-8).
const (
	RoleLead     = "lead"
	RoleDev      = "dev"
	RoleDesigner = "designer"
	RoleQA       = "qa"
	RolePM       = "pm"
	RoleReviewer = "reviewer"
	RoleClient   = "client"
)

// BuiltinRoles returns the compiled-in roles (TP-8), a fresh slice.
func BuiltinRoles() []Role {
	return []Role{
		{Name: RoleLead, Label: "Lead", Description: "leads the project"},
		{Name: RoleDev, Label: "Developer", Description: "writes the code"},
		{Name: RoleDesigner, Label: "Designer", Description: "designs the product"},
		{Name: RoleQA, Label: "QA", Description: "tests the product"},
		{Name: RolePM, Label: "Project manager", Description: "runs the project"},
		{Name: RoleReviewer, Label: "Reviewer", Description: "reviews changes"},
		{Name: RoleClient, Label: "Client", Description: "the client's own person on the project"},
	}
}
