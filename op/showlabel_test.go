package op

import (
	"reflect"
	"testing"
)

// slRecord, slEmbed and slShow are a show output shaped like the nouns'
// (data holding a record that embeds the one carrying _label).
type slRecord struct {
	ID    string `json:"id"`
	Label string `json:"_label,omitempty"`
}

type slEmbed struct {
	slRecord
	Status string `json:"status"`
}

type slShow struct {
	Data slEmbed `json:"data"`
}

func (o slShow) Text() string { return o.Data.ID + "\n  status: " + o.Data.Status + "\n" }

// TestShowLabelOnFirstLine: every show's text names its row's store/folder
// after the id (devco D-557); a repo row (no label) and a line already
// naming it are left as they are.
func TestShowLabelOnFirstLine(t *testing.T) {
	res := Result{v: reflect.ValueOf(slShow{Data: slEmbed{slRecord: slRecord{ID: "dec_1", Label: "[acme/sync]"}, Status: "open"}})}
	got, _ := res.WithShowLabel().Text()
	if want := "dec_1 [acme/sync]\n  status: open\n"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	twice, _ := res.WithShowLabel().WithShowLabel().Text()
	if twice != got {
		t.Fatalf("label printed twice: %q", twice)
	}
	repo := Result{v: reflect.ValueOf(slShow{Data: slEmbed{slRecord: slRecord{ID: "dec_2"}, Status: "open"}})}
	if s, _ := repo.WithShowLabel().Text(); s != "dec_2\n  status: open\n" {
		t.Fatalf("repo row text = %q", s)
	}
}
