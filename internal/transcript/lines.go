package transcript

import (
	"bufio"
	"bytes"
	"errors"
	"io"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	// maxLine is the one real memory cap of a line: a longer line is not
	// buffered but stream-scanned for its tool results (review F5).
	maxLine = 1 << 20
	// chunkSize is the read and reverse-scan unit.
	chunkSize = 64 << 10
	// maxPageRead is the most one page request reads (design section 6).
	maxPageRead = 8 << 20
)

// lineReader yields complete lines of [off, limit) of a file. A line not
// ended by a line feed at limit is left for a later read; a line over
// maxLine that is not finished stays in big, so a later reader can resume
// it without reading it again (review C-1). Memory: one bufio buffer plus at
// most maxLine of carry.
type lineReader struct {
	reader   *bufio.Reader
	off      int64 // bytes consumed
	done     int64 // the end of the last complete line
	carry    []byte
	read     int64    // bytes read so far
	big      *bigScan // an unfinished line over maxLine
	bigStart int64
}

func newLineReader(file io.ReaderAt, off, limit int64) *lineReader {
	return &lineReader{reader: bufio.NewReaderSize(io.NewSectionReader(file, off, max(limit-off, 0)), chunkSize), off: off, done: off}
}

// resume continues an unfinished long line that began at start; the reader
// itself starts where that line's scan stopped.
func (reader *lineReader) resume(scan *bigScan, start int64) {
	reader.big, reader.bigStart, reader.done = scan, start, start
}

// next returns the next complete line (without its line feed), its offset,
// and for a line over maxLine the scan of it instead (line is nil). ok is
// false at the end of the range.
func (reader *lineReader) next() (data []byte, off int64, big *bigScan, ok bool) {
	reader.carry = reader.carry[:0]
	start := reader.off
	if reader.big != nil {
		start = reader.bigStart
	}
	for {
		slice, err := reader.reader.ReadSlice('\n')
		reader.read += int64(len(slice))
		reader.off += int64(len(slice))
		switch {
		case err == nil:
			reader.done = reader.off
			if scan := reader.big; scan != nil {
				scan.feed(slice)
				reader.big = nil
				return nil, start, scan, true
			}
			if len(reader.carry) == 0 {
				return slice[:len(slice)-1], start, nil, true
			}
			reader.carry = append(reader.carry, slice...)
			return reader.carry[:len(reader.carry)-1], start, nil, true
		case errors.Is(err, bufio.ErrBufferFull):
			if reader.big != nil {
				reader.big.feed(slice)
				continue
			}
			if len(reader.carry)+len(slice) > maxLine {
				reader.big, reader.bigStart = &bigScan{ids: map[string]bool{}}, start
				reader.big.feed(reader.carry)
				reader.big.feed(slice)
				reader.carry = reader.carry[:0]
				continue
			}
			reader.carry = append(reader.carry, slice...)
		default:
			// The end of the range inside a line. A long line keeps its
			// scan state; a short one is left unread.
			if reader.big != nil {
				reader.big.feed(slice)
			} else {
				reader.off = start
			}
			return nil, start, nil, false
		}
	}
}

// bigScan reads a line over maxLine without keeping it: it finds the
// tool_use_id values and an is_error, the line type and sidechain flag.
// Claude Code writes compact JSON, so the keys are exact byte strings, and
// inside a JSON string a quote is escaped, so these never match string
// content.
type bigScan struct {
	tail      []byte
	ids       map[string]bool
	order     []string
	isError   bool
	user      bool
	assistant bool
	sidechain bool
	meta      bool // isMeta or isCompactSummary: never a turn start
	size      int64
}

var (
	toolUseKey   = []byte(`"tool_use_id":"`)
	isErrorKey   = []byte(`"is_error":true`)
	userKey      = []byte(`"type":"user"`)
	assistantKey = []byte(`"type":"assistant"`)
	sidechainKey = []byte(`"isSidechain":true`)
	metaKey      = []byte(`"isMeta":true`)
	compactKey   = []byte(`"isCompactSummary":true`)
)

const bigOverlap = 256

func (scan *bigScan) feed(chunk []byte) {
	scan.size += int64(len(chunk))
	window := append(scan.tail, chunk...)
	for rest := window; ; {
		index := bytes.Index(rest, toolUseKey)
		if index < 0 {
			break
		}
		rest = rest[index+len(toolUseKey):]
		end := bytes.IndexByte(rest, '"')
		if end < 0 {
			break // finished in the next chunk, inside the overlap
		}
		if id := string(rest[:end]); toolIDPattern.MatchString(id) && !scan.ids[id] && len(scan.ids) < 64 {
			scan.ids[id] = true
			scan.order = append(scan.order, id)
		}
	}
	scan.isError = scan.isError || bytes.Contains(window, isErrorKey)
	scan.user = scan.user || bytes.Contains(window, userKey)
	scan.assistant = scan.assistant || bytes.Contains(window, assistantKey)
	scan.sidechain = scan.sidechain || bytes.Contains(window, sidechainKey)
	scan.meta = scan.meta || bytes.Contains(window, metaKey) || bytes.Contains(window, compactKey)
	keep := min(len(window), bigOverlap)
	scan.tail = append(scan.tail[:0], window[len(window)-keep:]...)
}

// entry turns the scan into what the line shows: status updates of its tool
// calls (unknown when several share one is_error), or one omitted block.
func (scan *bigScan) entry(off int64) entry {
	result := entry{off: off, kind: kindBig}
	if scan.sidechain || !scan.user && !scan.assistant {
		result.kind = kindIgnored
		return result
	}
	if scan.user && len(scan.order) > 0 {
		status := protocol.ChatToolOK
		if scan.isError {
			status = protocol.ChatToolError
			if len(scan.order) > 1 {
				status = protocol.ChatToolUnknown
			}
		}
		for _, id := range scan.order {
			result.results = append(result.results, toolResult{id: id, status: status,
				result: &protocol.ChatToolResult{Preview: "", Bytes: scan.size, Truncated: true}})
		}
		return result
	}
	if scan.user && !scan.meta {
		// A prompt over the cap still opens a turn (review: low).
		id := blockID('u', off, 0)
		result.kind = kindPrompt
		result.blocks = []protocol.ChatBlock{{ID: id, Turn: ptr(id), Kind: protocol.UserText, Text: ptr("Message too large to show")}}
		return result
	}
	result.blocks = []protocol.ChatBlock{{ID: blockID('o', off, 0), Kind: protocol.Omitted, Text: ptr("Large message not shown")}}
	return result
}
