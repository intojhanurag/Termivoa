package platform

import (
	"io/fs"
	"os"
	"syscall"
)

// OpenRegular opens a regular file under root without blocking: a FIFO
// swapped in after the caller's own stat opens at once (O_NONBLOCK) and is
// refused with notRegular. Windows ignores O_NONBLOCK and has no FIFOs in
// folders.
func OpenRegular(root *os.Root, name string, notRegular error) (*os.File, fs.FileInfo, error) {
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = notRegular
	}
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, info, nil
}
