package op

// SourceNamer is an optional method of an input struct that names the
// source an id is read from (`--from repo|<org>|<org>/<folder>`, v0.1.17
// fixed rule 5). The Runner reads it before the run to attach a source no
// open store answers to yet (another org entry's store, such as staging,
// at the named folder), so the operation finds it by name among the
// attached stores. "" means the normal lookup.
type SourceNamer interface {
	SourceName() string
}

// NamedSource returns the source an input names through SourceNamer; ok
// is false when the input has no such method or names none.
func (i Input) NamedSource() (name string, ok bool) {
	if !i.v.IsValid() || !i.v.CanInterface() {
		return "", false
	}
	// reflect.Value.Interface returns the stdlib's untyped value; it is
	// narrowed to SourceNamer on this line and never kept untyped.
	s, isNamer := i.v.Interface().(SourceNamer)
	if !isNamer {
		return "", false
	}
	n := s.SourceName()
	return n, n != ""
}
