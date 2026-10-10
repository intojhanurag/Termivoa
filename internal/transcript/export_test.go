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
