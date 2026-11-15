//go:build !unix

package gitfast

// ownedByMe is false off unix: git's ownership rules there (Windows ACLs)
// are left to git, so every read runs the git program as before.
func ownedByMe(string) bool { return false }
