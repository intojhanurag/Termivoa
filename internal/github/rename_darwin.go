package github

import "golang.org/x/sys/unix"

func renameNoReplaceSystem(from, to string) error { return unix.RenamexNp(from, to, unix.RENAME_EXCL) }
