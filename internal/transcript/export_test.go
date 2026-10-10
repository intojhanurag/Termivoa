package transcript

import "bytes"

// ParseAll parses every line of data, for the parse benchmark.
func ParseAll(data []byte) {
	for off := 0; off < len(data); {
		end := bytes.IndexByte(data[off:], '\n')
		if end < 0 {
			return
		}
		parseClaudeLine(data[off:off+end], int64(off), "/w")
		off += end + 1
	}
}

// SetMaxProjectDirs lowers the folder limit for a test; call the result to restore it.
func SetMaxProjectDirs(limit int) func() {
	previous := maxProjectDirs
	maxProjectDirs = limit
	return func() { maxProjectDirs = previous }
}
