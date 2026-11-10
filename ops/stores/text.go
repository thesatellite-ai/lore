package stores

// Human lines of the 0.1.18 store operations (CLI_REFERENCE 3.21 examples,
// G-CORE-4): one ✓ line saying what changed and where it was written, plus
// what the person does next. --json is unchanged: the wrappers below embed
// the old output types and add only fields the JSON never shows.

import (
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// File names the lines name (spec 8).
const (
	storesFileShown = ".lore/stores.yaml"
	configFileShown = "~/.lore/config.yaml"
	markerShown     = "lore-store.yaml"
)

// folderSep joins a store and a folder in the name of a folder entry of
// stores.yaml ("<store>/<folder>", the name --to and store list use;
// engine/attach.Parse enforces the shape).
const folderSep = "/"

// Backend words of an add line: what the url's scheme picks (SPEC 7.2).
const (
	backendFolder = "folder backend"
	backendGit    = "git backend"
	fileScheme    = "file://"
)

// Attached is an entry as store.add and store.remove print it: Entry's
// fields those two ever filled, same json names, so the --json output is
// the one Entry gave (op outputs may not embed a struct).
type Attached struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Folder   string `json:"folder,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
	Branch   string `json:"branch,omitempty"`
}

// AddOut is store.add's output: the attached entry (its JSON unchanged)
// and, for the human line only, the visibility the store declares.
type AddOut struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Folder   string `json:"folder,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
	Branch   string `json:"branch,omitempty"`
	// Declared is the store's visibility as read after attaching ("" when
	// it could not be read); the JSON keeps Entry's fields only.
	Declared string `json:"-"`
}

// Text is `✓ attached store <name> (<what>, <backend>, <visibility>) in
// .lore/stores.yaml; commit it to share`.
func (o AddOut) Text() string {
	what := []string{"whole store"}
	if o.Folder != "" {
		what[0] = "folder " + o.Folder
	}
	if strings.HasPrefix(o.URL, fileScheme) {
		what = append(what, backendFolder)
	} else {
		what = append(what, backendGit)
	}
	if o.Branch != "" {
		what = append(what, "branch "+o.Branch)
	}
	if o.Declared != "" {
		what = append(what, o.Declared)
	}
	if o.ReadOnly {
		what = append(what, "read-only")
	}
	return "✓ attached store " + o.Name + " (" + strings.Join(what, ", ") + ") in " + storesFileShown + "; commit it to share\n"
}

// RemoveOut is store.remove's output: the detached entry, plus the folder
// entries (<store>/<folder>) detached with a whole store.
type RemoveOut struct {
	Name     string   `json:"name"`
	URL      string   `json:"url"`
	Folder   string   `json:"folder,omitempty"`
	ReadOnly bool     `json:"read_only,omitempty"`
	Branch   string   `json:"branch,omitempty"`
	Folders  []string `json:"folders,omitempty"`
}

// Text is the detach line: the rows and the local copy stay.
func (o RemoveOut) Text() string {
	what := "store "
	if strings.Contains(o.Name, folderSep) {
		what = "folder entry "
	}
	line := "✓ detached " + what + o.Name + " from " + storesFileShown + "; its rows stay in the store (`lore store forget` drops this machine's copy); commit the file to share\n"
	if len(o.Folders) > 0 {
		line += "  folder entries detached with it: " + strings.Join(o.Folders, ", ") + "\n"
	}
	return line
}

// Text is the init line: the new (or adopted) store, its id and where it
// was recorded (config.yaml for me, stores.yaml for every other name).
func (o InitOut) Text() string {
	where := storesFileShown + "; commit it to share"
	if o.Personal {
		where = configFileShown + " (personal.store, the target of --to me)"
	}
	line := "✓ created store " + o.Name + " (" + o.ID + ") at " + o.URL + "; recorded in " + where + "\n"
	if o.Already {
		line = "✓ " + o.URL + " is already a lore store (" + o.ID + "); recorded as " + o.Name + " in " + where + "\n"
	}
	if len(o.Following) > 0 {
		line += "  your setups " + strings.Join(o.Following, ", ") + " were on ~/.lore/stores/me and now use it too (one personal store per person)\n"
	}
	return line
}

// Text is the visibility line, plus the repos to re-render when a store
// went back to internal (their committed files may hold its rows).
func (o VisibilityOut) Text() string {
	if !o.Changed {
		return o.Name + " is already " + o.Visibility + "; nothing changed\n"
	}
	var b strings.Builder
	b.WriteString("✓ " + o.Name + " is " + o.Visibility + " (was " + o.Previous + "; " + markerShown + " written)\n")
	if len(o.Repos) > 0 {
		b.WriteString("  re-render these repos (`lore render`): their committed files may hold its rows\n")
		for _, r := range o.Repos {
			b.WriteString("    " + r + "\n")
		}
	}
	if o.Note != "" {
		b.WriteString("  note: " + o.Note + "\n")
	}
	return b.String()
}

// Text is the folder line (STORE TARGETS: shown only where attached).
func (o FolderAddOut) Text() string {
	if o.Already {
		return o.Store + " already has a folder " + o.Folder + " (" + o.ProjectID + "); nothing changed\n"
	}
	return "✓ created folder " + o.Folder + " in " + o.Store + " (" + o.ProjectID + "; shown only in repos that attach it)\n"
}

// Text is the set-url line; the id and the rows stay.
func (o SetURLOut) Text() string {
	var b strings.Builder
	if !o.Changed {
		b.WriteString("store " + o.Name + " is already at " + o.URL + "; nothing changed\n")
	} else if o.URL != o.Previous {
		b.WriteString("✓ store " + o.Name + " (" + o.ID + ") is now at " + o.URL + " (was " + o.Previous + "); its rows stay\n")
	} else {
		b.WriteString("✓ store " + o.Name + " (" + o.ID + ") stays at " + o.URL + "; its folder entries now point there too\n")
	}
	if len(o.Folders) > 0 {
		b.WriteString("  folder entries re-pointed: " + strings.Join(o.Folders, ", ") + "\n")
	}
	if len(o.Places) > 0 {
		b.WriteString("  now naming it: " + strings.Join(o.Places, ", ") + "\n")
	}
	for _, f := range o.OrgFiles {
		if f != "" {
			b.WriteString("  " + f + "\n")
		}
	}
	if o.Note != "" {
		b.WriteString("  note: " + o.Note + "\n")
	}
	return b.String()
}

// storeSchemes are the url schemes 0.1.18 has a backend for (SPEC 7.2:
// the folder backend for file://, git for the rest). The scp form
// git@host:owner/repo has no scheme and is git's.
var storeSchemes = []string{"file", "ssh", "https", "http"}

// CheckScheme refuses a store url whose scheme no 0.1.18 backend takes
// (s3://, postgres://, lore://) with E_STORE_SCHEME before anything is
// opened or written (CLI_REFERENCE 3.21). A url without a scheme (the scp
// form, a bare path) is left to the backends, which refuse what they
// cannot use with their own message.
func CheckScheme(raw string) error {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(raw), "://")
	if !ok || scheme == "" || rest == "" || strings.ContainsAny(scheme, "@/:") {
		return nil
	}
	if slices.Contains(storeSchemes, strings.ToLower(scheme)) {
		return nil
	}
	return errs.New(errs.StoreScheme, "no backend for "+strconv.Quote(scheme+"://")+" in lore 0.1.18; use a git url or file:///<absolute path>").
		WithHint("nothing written")
}
