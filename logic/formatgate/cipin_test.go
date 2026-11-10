package formatgate

import (
	"slices"
	"testing"
)

func TestCIPins(t *testing.T) {
	text := `name: lore
env:
  LORE_VERSION_PIN: "0.1.17"
jobs:
  check:
    steps:
      # lore upgrade --version 0.1.1 (a comment, ignored)
      - run: lore upgrade --version v0.1.16 && lore check leaks --staged
      - run: lore upgrade --version=0.1.18
      - run: curl -sSL https://github.com/thesatellite-ai/lore/releases/download/v0.1.15/lore_linux_amd64.tar.gz | tar xz
      - run: npm install --version 1.2.3
      - run: LORE_VERSION_PIN=0.1.0-dev lore doctor
`
	got := CIPins(text)
	want := []CIPin{{3, "0.1.17"}, {8, "v0.1.16"}, {9, "0.1.18"}, {10, "v0.1.15"}, {12, "0.1.0-dev"}}
	if !slices.Equal(got, want) {
		t.Fatalf("CIPins = %+v", got)
	}
	below := CIPinsBelow(got, "0.1.18")
	if !slices.Equal(below, []CIPin{{3, "0.1.17"}, {8, "v0.1.16"}, {10, "v0.1.15"}}) {
		t.Fatalf("CIPinsBelow = %+v", below)
	}
	if CIPinsBelow(got, "") != nil || CIPins("") != nil {
		t.Fatal("empty floor / text")
	}
}
