//go:build journeys && !windows

package journeys

import (
	"path/filepath"
	"strings"
	"testing"
)

// Persona is one person: a name and email that may change over the journey,
// and the machines they use (a laptop on day 1, a new laptop later, a desktop).
type Persona struct {
	w        *World
	Name     string
	Email    string
	machines []*Machine
}

// Persona adds a person with their first machine, labelled "laptop".
func (w *World) Persona(name, email string) *Persona {
	w.T.Helper()
	p := &Persona{w: w, Name: name, Email: email}
	w.personas = append(w.personas, p)
	p.NewLaptop("laptop")
	return p
}

// Laptop is the persona's first machine.
func (p *Persona) Laptop() *Machine { return p.machines[0] }

// Machine is the persona's machine with this label, or nil.
func (p *Persona) Machine(label string) *Machine {
	for _, m := range p.machines {
		if m.Label == label {
			return m
		}
	}
	return nil
}

// Machines is every machine of the persona, oldest first.
func (p *Persona) Machines() []*Machine { return append([]*Machine(nil), p.machines...) }

// NewLaptop gives the persona a fresh machine: an empty HOME with only their
// git identity, as a new computer has before lore is installed. It joins the
// World's clock and every later step's invariants.
func (p *Persona) NewLaptop(label string) *Machine {
	p.w.T.Helper()
	if p.Machine(label) != nil {
		p.w.T.Fatalf("persona %s already has a machine %q", p.Name, label)
	}
	a := p.w.Human(slug(p.Name)+"-"+label, p.Email)
	m := &Machine{Actor: a, Persona: p, Label: label, w: p.w}
	m.SetNow(p.w.now)
	// A clean PATH: the shim (git wrapper + the lore under test) and the
	// system folders, never the developer's own (their installed lore
	// would show up in doctor's "multiple lore binaries" and could run).
	m.SetEnv("PATH", filepath.Join(p.w.Root, "shim")+":/usr/bin:/bin")
	m.setGitIdentity(p.w.T, p.Name, p.Email) // the person's name, not the machine label
	p.machines = append(p.machines, m)
	p.w.machines = append(p.w.machines, m)
	return m
}

// Rename changes the person's display name and email from now on (their git
// identity on every machine follows, as a person updates ~/.gitconfig). What
// lore does with the old name is what the journey then checks.
func (p *Persona) Rename(t testing.TB, name, email string) {
	t.Helper()
	p.Name, p.Email = name, email
	for _, m := range p.machines {
		m.setGitIdentity(t, name, email)
	}
}

// slug makes a folder-safe label from a display name.
func slug(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '-'
	}, s)
}
