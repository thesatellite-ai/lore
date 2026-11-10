package profile

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// TestActorEditFields: every TP-5 field set, read back and cleared; the
// bad-value matrix refuses before anything is written.
func TestActorEditFields(t *testing.T) {
	ctx := context.Background()
	f, mira, _ := world(t)
	out, err := actorEdit(ctx, f, ActorEditIn{Actor: "@mira", Title: " Backend engineer ", Timezone: "Europe/Istanbul",
		Location: "Ankara, TR", PhotoURL: "https://img.example.com/mira.png", Bio: "Go and **SQL**", Birthday: "1990-04-17", DisplayName: "Mira P."})
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != mira.String() || len(out.Changed) != 7 || f.Pushes != 1 {
		t.Fatalf("out %+v pushes %d", out, f.Pushes)
	}
	a := live(f, schema.TableActors)
	var got map[string]string
	for _, x := range a {
		if x.ID == mira.Any() {
			got = map[string]string{}
			for k, v := range x.Fields {
				got[k] = v.String()
			}
		}
	}
	for k, want := range map[string]string{schema.FieldActorTitle: "Backend engineer", schema.FieldActorTimezone: "Europe/Istanbul",
		schema.FieldActorLocation: "Ankara, TR", schema.FieldActorPhotoURL: "https://img.example.com/mira.png",
		schema.FieldActorBio: "Go and **SQL**", schema.FieldActorBirthday: "1990-04-17", schema.FieldActorDisplayName: "Mira P."} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}

	// year-less birthday, by stable key
	if _, err := actorEdit(ctx, f, ActorEditIn{Actor: "human:mira@acme-agency.com", Birthday: "--02-29"}); err != nil {
		t.Fatalf("--02-29: %v", err)
	}
	r, _, _ := f.Roster(ctx, false)
	x, _ := r.Get(ctx, schema.TableActors, mira.Any())
	if b := text(x, schema.FieldActorBirthday); b != "0000-02-29" {
		t.Fatalf("year-less birthday stored %q", b)
	}

	// clear
	if _, err := actorEdit(ctx, f, ActorEditIn{Actor: "@mira", Clear: "photo_url, bio"}); err != nil {
		t.Fatal(err)
	}
	x, _ = r.Get(ctx, schema.TableActors, mira.Any())
	if text(x, schema.FieldActorPhotoURL) != "" || text(x, schema.FieldActorBio) != "" || text(x, schema.FieldActorTitle) == "" {
		t.Fatalf("clear left %v", x.Fields)
	}
}

// TestActorEditRefusals: each bad input answers its code and writes
// nothing.
func TestActorEditRefusals(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		in   ActorEditIn
		code errs.Code
	}{
		{"nothing", ActorEditIn{Actor: "@mira"}, errs.InvalidInput},
		{"blank title", ActorEditIn{Actor: "@mira", Title: "  "}, errs.InvalidInput},
		{"bad timezone", ActorEditIn{Actor: "@mira", Timezone: "Mars/Olympus"}, errs.InvalidInput},
		{"Local timezone", ActorEditIn{Actor: "@mira", Timezone: "Local"}, errs.InvalidInput},
		{"ftp photo", ActorEditIn{Actor: "@mira", PhotoURL: "ftp://x.example.com/a.png"}, errs.InvalidInput},
		{"hostless photo", ActorEditIn{Actor: "@mira", PhotoURL: "https:///a.png"}, errs.InvalidInput},
		{"photo with password", ActorEditIn{Actor: "@mira", PhotoURL: "https://" + strings.Join([]string{"u", "p"}, ":") + "@x.example.com/a.png"} /* built at run time: no credential-shaped literal in the tree */, errs.URLUserinfo},
		{"bad birthday", ActorEditIn{Actor: "@mira", Birthday: "17/04/1990"}, errs.InvalidInput},
		{"bad year-less day", ActorEditIn{Actor: "@mira", Birthday: "--02-30"}, errs.InvalidInput},
		{"clear unknown", ActorEditIn{Actor: "@mira", Clear: "status"}, errs.InvalidInput},
		{"clear display name", ActorEditIn{Actor: "@mira", Clear: "display_name"}, errs.InvalidInput},
		{"set and clear", ActorEditIn{Actor: "@mira", Title: "x", Clear: "title"}, errs.InvalidInput},
		{"unknown actor", ActorEditIn{Actor: "@nobody", Title: "x"}, errs.NotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, _, _ := world(t)
			_, err := actorEdit(ctx, f, c.in)
			wantCode(t, c.name, err, c.code)
			if f.Pushes != 0 {
				t.Fatalf("pushed after a refusal")
			}
		})
	}
	// TS-1 / TS-4: the roster refusals pass through.
	f, _, _ := world(t)
	f.Agent = true
	_, err := actorEdit(ctx, f, ActorEditIn{Actor: "@mira", Title: "x"})
	wantCode(t, "agent", err, errs.PolicyDenied)
	f.Agent, f.NoOrg = false, true
	_, err = actorEdit(ctx, f, ActorEditIn{Actor: "@mira", Title: "x"})
	wantCode(t, "no org", err, errs.Refused)
	if f.Opened != f.Closed {
		t.Fatalf("opened %d closed %d", f.Opened, f.Closed)
	}
}

// TestActorEditRefusalSaysWhy: a refused field flag prints its reason and
// a hint, not only "actor edit --<flag>": the surfaces show an error's
// Message and Hint (never its Cause), so the reason must be in them
// (B2-team: --timezone 'Istanbul time' answered a bare E_INVALID_INPUT).
// A url with credentials is never echoed.
func TestActorEditRefusalSaysWhy(t *testing.T) {
	ctx := context.Background()
	secretURL := "https://" + strings.Join([]string{"u", "p4ss"}, ":") + "@x.example.com/a.png" // built at run time
	for _, c := range []struct {
		name      string
		in        ActorEditIn
		msg, hint string
	}{
		{"timezone in words", ActorEditIn{Actor: "@mira", Timezone: "Istanbul time"}, `actor edit --timezone: "Istanbul time" is not an IANA zone name`, "Europe/Istanbul"},
		{"Local timezone", ActorEditIn{Actor: "@mira", Timezone: "Local"}, "is not an IANA zone name", "Europe/Istanbul"},
		{"blank title", ActorEditIn{Actor: "@mira", Title: "  "}, "actor edit --title: a blank value", ""},
		{"blank location", ActorEditIn{Actor: "@mira", Location: " "}, "actor edit --location: a blank value", ""},
		{"ftp photo", ActorEditIn{Actor: "@mira", PhotoURL: "ftp://x.example.com/a.png"}, "actor edit --photo-url: not an http(s) URL", "https://"},
		{"photo with password", ActorEditIn{Actor: "@mira", PhotoURL: secretURL}, "actor edit --photo-url: the url holds credentials", ""},
		{"ftp photo with password", ActorEditIn{Actor: "@mira", PhotoURL: strings.Replace(secretURL, "https", "ftp", 1)}, "actor edit --photo-url: the url holds credentials", ""},
		{"bad birthday", ActorEditIn{Actor: "@mira", Birthday: "17/04/1990"}, "want YYYY-MM-DD", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, _, _ := world(t)
			_, err := actorEdit(ctx, f, c.in)
			body := errs.EnvelopeOf(err).Error
			if !strings.Contains(body.Message, c.msg) {
				t.Fatalf("message %q, want it to contain %q", body.Message, c.msg)
			}
			if c.hint != "" && !strings.Contains(body.Hint, c.hint) {
				t.Fatalf("hint %q, want it to contain %q", body.Hint, c.hint)
			}
			if strings.Contains(body.Message+body.Hint, "p4ss") {
				t.Fatalf("the url's password is echoed: %q / %q", body.Message, body.Hint)
			}
		})
	}
}
