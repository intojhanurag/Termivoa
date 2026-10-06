package github

import "golang.org/x/sys/unix"

// renameExclusive renames from to to and fails with EEXIST when to exists
// (os.Rename would replace an empty folder).
func renameExclusive(from, to string) error {
	return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
}
