package profile

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// actor.edit (TEAM_PROFILES 3.1, TP-5): validates each profile field of
// 2.1 and writes one update to the actor row. status, leave dates and
// active/left_at are not edited here: they belong to actor.status,
// offboard and rejoin (ops/period), which keep them in step (2.7).

// Birthday forms (2.1): YYYY-MM-DD, or a year-less --MM-DD stored as
// 0000-MM-DD (a person may not want to give the year).
const (
	yearlessPrefix = "--"
	yearlessYear   = "0000-"
)

// Photo URL schemes (2.1: an http(s) URL).
const (
	photoSchemeHTTP  = "http"
	photoSchemeHTTPS = "https"
)

// timezoneLocal is the one name time.LoadLocation accepts that is not an
// IANA zone: the machine's own zone, which means nothing to a teammate.
const timezoneLocal = "Local"

// clearSep separates the field names of --clear.
const clearSep = ","

// clearable lists the fields --clear may empty: the TP-5 profile fields.
// display_name is required on the actor row, so it can be changed, never
// cleared.
func clearable() []string {
	return []string{schema.FieldActorPhotoURL, schema.FieldActorTimezone, schema.FieldActorLocation,
		schema.FieldActorTitle, schema.FieldActorBio, schema.FieldActorBirthday}
}

// ActorEditIn is the input of actor.edit. An empty flag leaves its field
// as it is; --clear empties named fields.
type ActorEditIn struct {
	Actor string `json:"actor" arg:"0" desc:"the person: @handle, act_ id or stable key"`
	// Name is the actor's name at its source (SOURCE_OF_TRUTH actors):
	// the roster row in a team, your own identity in a repo with no org.
	Name        string `json:"name,omitempty" flag:"name" desc:"the name shown everywhere: the roster row in a team, your identity (setup) in a repo with no org"`
	DisplayName string `json:"display_name,omitempty" flag:"display-name" desc:"the same as --name (kept for older scripts)"`
	Title       string `json:"title,omitempty" flag:"title" desc:"the profile headline (Backend engineer)"`
	Timezone    string `json:"timezone,omitempty" flag:"timezone" desc:"an IANA zone name (Europe/Istanbul)"`
	Location    string `json:"location,omitempty" flag:"location" desc:"free text (Ankara, TR)"`
	PhotoURL    string `json:"photo_url,omitempty" flag:"photo-url" desc:"an http(s) URL of a photo"`
	Bio         string `json:"bio,omitempty" flag:"bio" desc:"markdown"`
	Birthday    string `json:"birthday,omitempty" flag:"birthday" desc:"YYYY-MM-DD, or --MM-DD without the year"`
	Clear       string `json:"clear,omitempty" flag:"clear" desc:"comma-separated fields to empty: photo_url,timezone,location,title,bio,birthday"`
}

// ActorEditOut is the edited actor.
type ActorEditOut struct {
	ID   string `json:"id"`
	Name string `json:"display_name"`
	// Changed lists the fields set or cleared, in input order.
	Changed []string `json:"changed"`
	Where   string   `json:"where"`
	// Warnings are the engine's warnings of the write
	// (profilestore.WarningLine), printed after the success line.
	Warnings []string `json:"warnings,omitempty"`
}

// Text is "✓ <id> <name> edited (title, timezone) in <where>".
func (o ActorEditOut) Text() string { return o.text() + profilestore.WarningText(o.Warnings) }

// text is Text without the warnings.
func (o ActorEditOut) text() string {
	return work.Success + " " + o.ID + " " + o.Name + " edited (" + strings.Join(o.Changed, ", ") + ") in " + o.Where + "\n"
}

// actorEdit builds the patch, refuses a bad value before anything is
// written, then writes one update.
func actorEdit(ctx context.Context, env Env, in ActorEditIn) (ActorEditOut, error) {
	in, err := nameFlags(in)
	if err != nil {
		return ActorEditOut{}, err
	}
	patch, changed, err := actorPatch(in)
	if err != nil {
		return ActorEditOut{}, err
	}
	var out ActorEditOut
	opened := false
	err = openWrite(ctx, env, &out.Warnings, func(r profilestore.Roster) (bool, error) {
		opened = true
		a, err := r.ResolveActor(ctx, strings.TrimSpace(in.Actor))
		if err != nil {
			return false, err
		}
		// SOURCE_OF_TRUTH actors: the roster row is changed by that
		// person or an org admin only.
		if g, ok := r.(profilestore.PersonEditGate); ok {
			if err := g.MayEditPerson(ctx, a.ID); err != nil {
				return false, err
			}
		}
		u, err := r.Update(ctx, schema.TableActors, a.ID, patch)
		if err != nil {
			return false, errs.Prefix("actor edit "+a.ID.String(), err)
		}
		out = ActorEditOut{ID: u.ID.String(), Name: text(u, schema.FieldActorDisplayName), Changed: changed, Where: r.Where()}
		return true, nil
	})
	if !opened && errs.Has(err, errs.Refused) && len(patch) == 1 && in.DisplayName != "" {
		// No org here (TS-1 refused the roster): the source of a solo
		// actor is the person's own identity, so --name renames that.
		if rn, ok := upgrade.As[profilestore.IdentityRenamer](env); ok {
			got, rerr := rn.RenameIdentity(ctx, in.Actor, strings.TrimSpace(in.DisplayName))
			if rerr != nil {
				return ActorEditOut{}, rerr
			}
			return ActorEditOut{ID: got.ID, Name: got.To, Changed: []string{schema.FieldActorDisplayName}, Where: got.Where}, nil
		}
	}
	return out, err
}

// nameFlags folds --name into --display-name (one field, two spellings);
// two different values are refused.
func nameFlags(in ActorEditIn) (ActorEditIn, error) {
	n, d := strings.TrimSpace(in.Name), strings.TrimSpace(in.DisplayName)
	if n != "" && d != "" && n != d {
		return in, errs.New(errs.InvalidInput, "actor edit: --name "+strconv.Quote(n)+" and --display-name "+strconv.Quote(d)+" differ; they set the same field, give one")
	}
	if in.Name != "" {
		in.DisplayName = in.Name
	}
	return in, nil
}

// actorPatch validates in into an Update patch (value.Null clears) and
// the names it touches. Pure: no roster needed, so a bad value never
// opens the store.
func actorPatch(in ActorEditIn) (map[string]value.Value, []string, error) {
	patch := map[string]value.Value{}
	var changed []string
	set := func(field string, v value.Value) {
		patch[field] = v
		changed = append(changed, field)
	}
	type textField struct {
		name, val string
		check     func(string) (string, error)
	}
	for _, f := range []textField{
		{schema.FieldActorDisplayName, in.DisplayName, nonBlank},
		{schema.FieldActorTitle, in.Title, nonBlank},
		{schema.FieldActorTimezone, in.Timezone, checkTimezone},
		{schema.FieldActorLocation, in.Location, nonBlank},
		{schema.FieldActorPhotoURL, in.PhotoURL, checkPhotoURL},
		{schema.FieldActorBio, in.Bio, nonBlank},
	} {
		if f.val == "" {
			continue
		}
		s, err := f.check(f.val)
		if err != nil {
			return nil, nil, errs.Prefix("actor edit --"+strings.ReplaceAll(f.name, "_", "-"), err)
		}
		v, err := textValue(f.name, s)
		if err != nil {
			return nil, nil, err
		}
		set(f.name, v)
	}
	if in.Birthday != "" {
		d, err := parseBirthday(in.Birthday)
		if err != nil {
			return nil, nil, err
		}
		set(schema.FieldActorBirthday, d)
	}
	if strings.TrimSpace(in.Clear) != "" {
		for _, name := range strings.Split(in.Clear, clearSep) {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if !slices.Contains(clearable(), name) {
				return nil, nil, errs.New(errs.InvalidInput, "actor edit --clear: "+name+" is not one of "+strings.Join(clearable(), ", "))
			}
			if _, both := patch[name]; both {
				return nil, nil, errs.New(errs.InvalidInput, "actor edit: "+name+" is both set and cleared; give one")
			}
			set(name, value.Null{})
		}
	}
	if len(patch) == 0 {
		return nil, nil, errs.New(errs.InvalidInput, "actor edit: nothing to change").
			WithHint("give a field flag (--title, --timezone, …) or --clear f,f")
	}
	return patch, changed, nil
}

// timezoneHint is the hint of a refused --timezone.
const timezoneHint = "give an IANA zone name such as Europe/Istanbul or America/New_York"

// nonBlank trims s and refuses a blank value (an empty flag means "leave
// it"; emptying is --clear).
func nonBlank(s string) (string, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return "", errs.New(errs.InvalidInput, "a blank value; use --clear to empty a field")
	}
	return t, nil
}

// checkTimezone accepts an IANA zone name (time.LoadLocation), never
// "Local" or UTC's empty alias.
func checkTimezone(s string) (string, error) {
	t := strings.TrimSpace(s)
	if t == "" || t == timezoneLocal {
		return "", errs.New(errs.InvalidInput, strconv.Quote(s)+" is not an IANA zone name").WithHint(timezoneHint)
	}
	if _, err := time.LoadLocation(t); err != nil {
		return "", errs.Wrap(errs.InvalidInput, strconv.Quote(t)+" is not an IANA zone name", err).WithHint(timezoneHint)
	}
	return t, nil
}

// checkPhotoURL accepts an http(s) URL with a host and no credentials
// (it is shown plain, TP-6).
func checkPhotoURL(s string) (string, error) {
	t := strings.TrimSpace(s)
	u, err := url.Parse(t)
	// Neither refusal quotes the value: a url with user:password@ (even
	// one that does not parse) must never be echoed, Message is printed.
	if err == nil && u.User != nil {
		return "", errs.New(errs.URLUserinfo, "the url holds credentials; give the address without user:password@")
	}
	if err != nil || u.Host == "" || (u.Scheme != photoSchemeHTTP && u.Scheme != photoSchemeHTTPS) {
		return "", errs.New(errs.InvalidInput, "not an http(s) URL with a host").
			WithHint("give an address such as https://example.com/me.png")
	}
	return t, nil
}

// parseBirthday reads YYYY-MM-DD or a year-less --MM-DD (stored as
// 0000-MM-DD, 2.1). Year 0 is a leap year in Go's proleptic calendar, so
// --02-29 is accepted.
func parseBirthday(s string) (value.Value, error) {
	t := strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(t, yearlessPrefix); ok {
		t = yearlessYear + rest
	}
	d, err := value.DateOf(t)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "actor edit --birthday "+s+": want YYYY-MM-DD or --MM-DD", err)
	}
	return d, nil
}
