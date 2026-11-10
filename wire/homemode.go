package wire

// homemode.go — the lore home (~/.lore) is owner-only, also when an older
// lore made it.
//
// lore 0.1.17 created ~/.lore with the umask's mode (0755 on most
// machines), so its identity.yaml (an email) and every later file were
// readable by other users of the machine. This lore makes the folder 0700
// when it creates it, but MkdirAll keeps an existing folder's mode, so an
// upgraded machine stayed open (upgrader journey, week2/laptop-upgrades).
// Every App now shuts group and others out of an existing lore home: a
// folder that is the person's own and only theirs to read. Nothing inside
// is changed (a file under a closed folder is private), a symlinked home is
// left alone (its target is the person's choice), and a failure (not the
// owner, a read-only file system) changes nothing and never stops lore.

import (
	"io/fs"
	"os"
)

// homeOpenBits are the permission bits of group and others.
const homeOpenBits fs.FileMode = 0o077

// closeLoreHome removes group and other access from an existing lore home.
func closeLoreHome(home string) {
	info, err := os.Lstat(home)
	if err != nil || !info.IsDir() || info.Mode().Perm()&homeOpenBits == 0 {
		return
	}
	_ = os.Chmod(home, info.Mode().Perm()&^homeOpenBits) // best effort, see the file comment
}
