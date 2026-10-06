package github

import "golang.org/x/sys/unix"

// renameExclusive renames from to to and fails with EEXIST when to exists
// (os.Rename would replace an empty folder).
func renameExclusive(from, to string) error { return unix.RenamexNp(from, to, unix.RENAME_EXCL) }
