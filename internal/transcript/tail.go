package transcript

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	maxIndex        = 256     // tool calls and results remembered (design section 4.1)
	seedRead        = 1 << 20 // read back at bind for the drift window and the last uuid
	maxCursors      = 1024    // cursors issued per history
	driftWindow     = 200     // lines in the drift check
	driftLimit      = 0.20    // unknown share above which the format is unknown
	pageBudget      = protocol.MaxChatPageBytes - 16<<10
	blockOverhead   = 1 // the comma between blocks
	thoughtMinimum  = time.Second
	thoughtMaximum  = time.Hour
	maxPollRead     = 8 << 20 // the most one poll reads; the rest waits for the next
	maxVersionBytes = 32
)

// ErrCursor is a cursor this transcript did not issue for its history.
var ErrCursor = errors.New("transcript: unknown cursor")

// Transcript is one linked transcript file, read only through the handle
// opened at bind time (review S5). It is safe for concurrent use: the tail
// loop polls while page requests read.
type Transcript struct {
	file      *os.File
	reader    countingReader
	Agent     Process
	SessionID string
	cwd       string
	name      string // root-relative path, for rechecks only; never sent or logged
	dev, ino  uint64

	mu       sync.Mutex
	history  string
	live     int64 // the end of the last complete line read by Poll
	size     int64
	grewAt   time.Time
	calls    map[string]protocol.ChatBlock
	results  map[string]toolResult
	order    []string
	lastUUID string // the uuid of the last line Poll read; "" when unknown
	big      *bigScan
	bigStart int64 // where the unfinished long line began
	bigPos   int64 // where its scan stopped
	cursors  map[string]cursor
	issued   []string
	drift    [driftWindow]bool
	driftN   int
	driftAt  int
	version  string
}

type cursor struct {
	history string
	off     int64
	turn    string // after: the turn open at off
	anchor  string // before: the uuid the older page's chain ends at
}

func newTranscript(file *os.File, agent Process, sessionID, cwd, name string, dev, ino uint64, size int64) *Transcript {
	transcript := &Transcript{
		file: file, Agent: agent, SessionID: sessionID, cwd: cwd, name: name, dev: dev, ino: ino,
		calls: map[string]protocol.ChatBlock{}, results: map[string]toolResult{},
		cursors: map[string]cursor{}, grewAt: time.Now(),
	}
	transcript.reader = countingReader{file: file, read: new(atomic.Int64)}
	transcript.history = newHistoryID()
	transcript.live, transcript.size = lastLineEnd(transcript.reader, size), size
	transcript.seed()
	return transcript
}

// countingReader counts the bytes read, so tests can hold the read budget.
type countingReader struct {
	file *os.File
	read *atomic.Int64
}

func (reader countingReader) ReadAt(buffer []byte, off int64) (int, error) {
	count, err := reader.file.ReadAt(buffer, off)
	reader.read.Add(int64(count))
	return count, err
}

// BytesRead is how many bytes were read from the file so far.
func (transcript *Transcript) BytesRead() int64 { return transcript.reader.read.Load() }

// Close releases the file.
func (transcript *Transcript) Close() error { return transcript.file.Close() }

// History is the current history ID.
func (transcript *Transcript) History() string {
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	return transcript.history
}

// Version is the agent version seen last ("" when none).
func (transcript *Transcript) Version() string {
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	return transcript.version
}

// Unknown is the share of the last lines (at most 200) the parser did not
// understand, 0 to 1.
func (transcript *Transcript) Unknown() float64 {
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	return transcript.unknownLocked()
}

func (transcript *Transcript) unknownLocked() float64 {
	if transcript.driftN == 0 {
		return 0
	}
	bad := 0
	for index := range transcript.driftN {
		if transcript.drift[index] {
			bad++
		}
	}
	return float64(bad) / float64(transcript.driftN)
}

// FormatUnknown reports the drift check (design section 10): over 20% of
// the last 200 lines not understood, after at least 20 lines.
func (transcript *Transcript) FormatUnknown() bool {
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	return transcript.driftN >= 20 && transcript.unknownLocked() > driftLimit
}

// IdleFor is how long the file has not grown.
func (transcript *Transcript) IdleFor(now time.Time) time.Duration {
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	return now.Sub(transcript.grewAt)
}

// PollResult is what one poll saw.
type PollResult struct {
	Grew      bool // new complete lines exist: send chat.changed
	Rewound   bool // the conversation was rewound: a new history ID
	Truncated bool // the file shrank: re-bind
}

// Poll is the 250 ms tick: fstat the held handle and read new complete
// lines (at most 8 MiB a tick) into the tool index, the rewind check and the
// drift check. A long line that is not finished keeps its scan state, so
// its bytes are read once (review C-1).
func (transcript *Transcript) Poll() (PollResult, error) {
	info, err := transcript.file.Stat()
	if err != nil {
		return PollResult{}, err
	}
	transcript.mu.Lock()
	live, size, scan, start, from := transcript.live, info.Size(), transcript.big, transcript.bigStart, transcript.live
	if scan != nil {
		from = transcript.bigPos
	}
	transcript.mu.Unlock()
	if size < from {
		return PollResult{Truncated: true}, nil
	}
	if size == from {
		return PollResult{}, nil
	}
	reader := newLineReader(transcript.reader, from, min(size, from+maxPollRead))
	if scan != nil {
		reader.resume(scan, start)
	}
	var result PollResult
	for {
		data, off, big, ok := reader.next()
		if !ok {
			break
		}
		item := transcript.parse(data, off, big)
		transcript.mu.Lock()
		if transcript.observe(item, true) {
			result.Rewound = true
		}
		transcript.mu.Unlock()
		result.Grew = true
	}
	transcript.mu.Lock()
	if reader.done > live {
		transcript.live = reader.done
		transcript.grewAt = time.Now()
	}
	transcript.big, transcript.bigStart, transcript.bigPos = reader.big, reader.bigStart, reader.off
	if result.Rewound {
		transcript.newHistoryLocked()
	}
	transcript.size = size
	transcript.mu.Unlock()
	return result, nil
}

func (transcript *Transcript) parse(data []byte, off int64, big *bigScan) entry {
	if big != nil {
		return big.entry(off)
	}
	return parseClaudeLine(data, off, transcript.cwd)
}

// observe feeds the tool index. Live lines (Poll and the bind seed) also
// feed the drift window, the version and the rewind check; old lines read
// for a page never do (review S-8). It reports a rewind: a new prompt whose
// parent is not the line read just before it (review C-3). After a line
// whose uuid is unknown (a long line) there is no decision.
func (transcript *Transcript) observe(item entry, live bool) bool {
	rewound := false
	if live {
		bad := item.kind == kindBad || item.kind == kindUnknown
		transcript.drift[transcript.driftAt] = bad
		transcript.driftAt = (transcript.driftAt + 1) % driftWindow
		transcript.driftN = min(transcript.driftN+1, driftWindow)
		if item.version != "" && len(item.version) <= maxVersionBytes && protocol.ValidChatVersion(item.version) {
			transcript.version = item.version
		}
		if item.kind == kindPrompt && item.parent != "" && transcript.lastUUID != "" && item.parent != transcript.lastUUID {
			rewound = true
		}
		switch {
		case item.sidechain:
		case item.kind == kindBig:
			transcript.lastUUID = ""
		case item.uuid != "":
			transcript.lastUUID = item.uuid
		}
	}
	for _, block := range item.blocks {
		if block.Tool != nil && block.ID[0] == 't' {
			transcript.remember(block.ID, &block, nil)
		}
	}
	for _, result := range item.results {
		transcript.remember("t:"+result.id, nil, &result)
	}
	return rewound
}

// seed reads back at most 1 MiB from the end at bind time: the last lines
// fill the drift window and the version, the last one gives the uuid the
// next prompt must name as its parent.
func (transcript *Transcript) seed() {
	from := max(transcript.live-seedRead, 0)
	if from > 0 {
		from = firstLineAfter(transcript.reader, from, transcript.live)
	}
	reader := newLineReader(transcript.reader, from, transcript.live)
	for {
		data, off, big, ok := reader.next()
		if !ok {
			return
		}
		transcript.observe(transcript.parse(data, off, big), true)
	}
}

func (transcript *Transcript) remember(id string, call *protocol.ChatBlock, result *toolResult) {
	_, known := transcript.calls[id]
	_, seen := transcript.results[id]
	if !known && !seen {
		transcript.order = append(transcript.order, id)
		if len(transcript.order) > maxIndex {
			delete(transcript.calls, transcript.order[0])
			delete(transcript.results, transcript.order[0])
			transcript.order = transcript.order[1:]
		}
	}
	if call != nil {
		transcript.calls[id] = *call
	}
	if result != nil {
		transcript.results[id] = *result
	}
}

func (transcript *Transcript) newHistoryLocked() {
	transcript.history = newHistoryID()
	transcript.cursors, transcript.issued = map[string]cursor{}, nil
}

// PageRequest asks for one page. Without a cursor it is the latest page.
type PageRequest struct {
	Before string
	After  string
	Turns  int
}

// Page reads one page through the held handle: the latest blocks, older
// blocks before a cursor, or newer blocks after one. It reads at most 8 MiB
// and returns at most 200 blocks within the page budget.
func (transcript *Transcript) Page(request PageRequest) (protocol.ChatPagePayload, error) {
	transcript.mu.Lock()
	history, end := transcript.history, transcript.live
	var from cursor
	if request.Before != "" || request.After != "" {
		token := request.Before + request.After
		found, ok := transcript.cursors[token]
		if !ok || found.history != history {
			transcript.mu.Unlock()
			return protocol.ChatPagePayload{}, ErrCursor
		}
		from = found
	}
	transcript.mu.Unlock()
	if request.After != "" {
		return transcript.pageAfter(history, from, end), nil
	}
	if request.Before != "" {
		end = from.off
	}
	return transcript.pageBefore(history, end, from.anchor, max(request.Turns, 1)), nil
}

// placed is a block and the offset of the line it came from.
type placed struct {
	block   protocol.ChatBlock
	off     int64
	size    int
	dropped bool
}

type assembler struct {
	transcript *Transcript
	out        []*placed
	bytes      int
	byID       map[string]*placed // a repeated ID replaces its block
	texts      map[string]bool    // message.id + text, for repeated assistant lines
	turn       string
	prevAt     time.Time
}

func (assembler *assembler) add(block protocol.ChatBlock, off int64) *placed {
	if block.Kind != protocol.UserText && assembler.turn != "" {
		block.Turn = ptr(assembler.turn)
	}
	if item, ok := assembler.byID[block.ID]; ok && !item.dropped {
		item.block = block
		assembler.resize(item)
		return item
	}
	encoded, _ := json.Marshal(block)
	item := &placed{block: block, off: off, size: len(encoded) + blockOverhead}
	assembler.out = append(assembler.out, item)
	assembler.bytes += item.size
	assembler.byID[block.ID] = item
	return item
}

func (assembler *assembler) resize(item *placed) {
	encoded, _ := json.Marshal(item.block)
	if !item.dropped {
		assembler.bytes += len(encoded) + blockOverhead - item.size
	}
	item.size = len(encoded) + blockOverhead
}

// take adds what one line shows.
func (assembler *assembler) take(item entry) {
	prevAt := assembler.prevAt
	if !item.at.IsZero() {
		assembler.prevAt = item.at
	}
	switch item.kind {
	case kindPrompt:
		assembler.turn = item.blocks[0].ID
	case kindAssistant:
		if item.thought && !prevAt.IsZero() && !item.at.IsZero() {
			if spent := item.at.Sub(prevAt); spent >= thoughtMinimum && spent <= thoughtMaximum {
				assembler.add(statusBlock(item.off, 99, "Thought for "+spent.Round(time.Second).String()), item.off)
			}
		}
	}
	for _, block := range item.blocks {
		if block.Kind == protocol.AssistantText && item.msgID != "" {
			key := item.msgID + "\x00" + *block.Text
			if assembler.texts[key] {
				continue
			}
			assembler.texts[key] = true
		}
		if block.At == nil && !item.at.IsZero() {
			block.At = ptr(item.at.Format(time.RFC3339Nano))
		}
		assembler.add(block, item.off)
	}
	for _, result := range item.results {
		id := "t:" + result.id
		if call, ok := assembler.byID[id]; ok && !call.dropped && call.block.Tool != nil {
			applyResult(&call.block, result)
			assembler.resize(call)
			continue
		}
		assembler.transcript.mu.Lock()
		call, ok := assembler.transcript.calls[id]
		assembler.transcript.mu.Unlock()
		if ok {
			applyResult(&call, result)
			assembler.add(call, item.off)
		}
	}
}

// finish applies results seen after the range to calls still running in it.
func (assembler *assembler) finish() []protocol.ChatBlock {
	assembler.transcript.mu.Lock()
	for _, item := range assembler.out {
		if item.dropped || item.block.Tool == nil || item.block.Tool.Status != protocol.ChatToolRunning {
			continue
		}
		if result, ok := assembler.transcript.results[item.block.ID]; ok {
			applyResult(&item.block, result)
			assembler.resize(item)
		}
	}
	assembler.transcript.mu.Unlock()
	blocks := make([]protocol.ChatBlock, 0, len(assembler.out))
	for _, item := range assembler.out {
		if !item.dropped {
			blocks = append(blocks, item.block)
		}
	}
	return blocks
}

func applyResult(block *protocol.ChatBlock, result toolResult) {
	tool := *block.Tool
	tool.Status, tool.Result = result.status, result.result
	block.Tool = &tool
}

func (transcript *Transcript) newAssembler(turn string) *assembler {
	return &assembler{transcript: transcript, byID: map[string]*placed{}, texts: map[string]bool{}, turn: turn}
}

func (transcript *Transcript) pageAfter(history string, from cursor, end int64) protocol.ChatPagePayload {
	assembler := transcript.newAssembler(from.turn)
	// end is a line end, so a long line inside is complete and is scanned
	// to its end, never re-read from its start (review C-1).
	reader := newLineReader(transcript.reader, from.off, end)
	next, complete := from.off, true
	for {
		if reader.read > maxPageRead {
			complete = false
			break
		}
		data, off, big, ok := reader.next()
		if !ok {
			break
		}
		before := len(assembler.out)
		turn := assembler.turn
		assembler.take(transcript.parse(data, off, big))
		if assembler.bytes > pageBudget || len(assembler.out) > protocol.MaxChatPageBlocks {
			// This line does not fit: undo it and stop before it.
			for _, item := range assembler.out[before:] {
				item.dropped = true
			}
			assembler.out, assembler.turn, complete = assembler.out[:before], turn, false
			if before == 0 {
				// One line alone is over the budget: skip it.
				next = reader.off
			}
			break
		}
		next = reader.off
	}
	blocks := assembler.finish()
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	return protocol.ChatPagePayload{
		HistoryID: history, Blocks: blocks, Complete: complete,
		After: transcript.issueLocked(cursor{history: history, off: next, turn: assembler.turn}),
	}
}

func (transcript *Transcript) pageBefore(history string, end int64, anchor string, turns int) protocol.ChatPagePayload {
	start := transcript.findStart(end, turns)
	var entries []entry
	reader := newLineReader(transcript.reader, start, end)
	for {
		data, off, big, ok := reader.next()
		if !ok {
			break
		}
		item := transcript.parse(data, off, big)
		transcript.mu.Lock()
		transcript.observe(item, false)
		transcript.mu.Unlock()
		// Keep only what a page can show: entries without blocks or
		// results cost nothing later.
		if len(item.blocks) > 0 || len(item.results) > 0 || item.uuid != "" {
			entries = append(entries, item)
		}
	}
	entries = onChain(entries, anchor)
	assembler := transcript.newAssembler("")
	front := start
	for _, item := range entries {
		assembler.take(item)
		// Over the budget: drop the oldest blocks; the older page covers
		// them again (a repeated block replaces itself by ID).
		for len(assembler.out) > 0 && (assembler.bytes > pageBudget || len(assembler.out) > protocol.MaxChatPageBlocks) {
			// Drop whole lines, so the older page (which ends at front)
			// shows every block of a line it covers.
			line := assembler.out[0].off
			for len(assembler.out) > 0 && assembler.out[0].off == line {
				assembler.out[0].dropped = true
				assembler.bytes -= assembler.out[0].size
				assembler.out = assembler.out[1:]
			}
			front = item.off
			if len(assembler.out) > 0 {
				front = assembler.out[0].off
			}
		}
	}
	blocks := assembler.finish()
	firstAnchor := ""
	for _, item := range entries {
		if item.off >= front && item.uuid != "" && (item.kind == kindPrompt || item.kind == kindAssistant || item.kind == kindResults) {
			firstAnchor = item.parent
			break
		}
	}
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	page := protocol.ChatPagePayload{
		HistoryID: history, Blocks: blocks, Complete: end >= transcript.live,
		After: transcript.issueLocked(cursor{history: history, off: end, turn: assembler.turn}),
	}
	if front > 0 {
		page.Before = ptr(transcript.issueLocked(cursor{history: history, off: front, anchor: firstAnchor}))
	}
	return page
}

// onChain drops lines of abandoned rewind branches (review F7): walking back
// from the anchor (or the last message line) by parent, every message line
// that is not reached is dropped. A compaction resets parentUuid; its
// logicalParentUuid carries the walk on. When the anchor is not in the
// range, nothing can be told and everything is kept.
func onChain(entries []entry, anchor string) []entry {
	byUUID := make(map[string]int, len(entries))
	last := ""
	for index, item := range entries {
		if item.uuid == "" {
			continue
		}
		byUUID[item.uuid] = index
		if item.kind == kindPrompt || item.kind == kindAssistant || item.kind == kindResults || item.kind == kindCompact {
			last = item.uuid
		}
	}
	if anchor == "" {
		anchor = last
	}
	if _, ok := byUUID[anchor]; !ok {
		return entries
	}
	keep := make(map[int]bool, len(entries))
	for current, steps := anchor, 0; steps <= len(entries); steps++ {
		index, ok := byUUID[current]
		if !ok || keep[index] {
			break
		}
		keep[index] = true
		current = entries[index].parent
	}
	kept := entries[:0]
	for index, item := range entries {
		message := item.kind == kindPrompt || item.kind == kindAssistant || item.kind == kindResults || item.kind == kindCompact
		if keep[index] || !message || item.uuid == "" {
			kept = append(kept, item)
		}
	}
	return kept
}

// findStart scans back from end in 64 KiB chunks for the start of the
// turns-th prompt, reading at most 8 MiB, and never the whole file. A line
// over maxLine is not kept: its keys are checked as it passes (a long
// prompt still counts). When one line alone is longer than the budget, the
// page starts at that line, so paging always moves (review C-1).
func (transcript *Transcript) findStart(end int64, turns int) int64 {
	floor := max(end-maxPageRead, 0)
	buffer := make([]byte, chunkSize)
	var carry []byte // the end of a line whose start is before the chunk
	var long *reverseScan
	found := 0
	prompt := func(start int64) bool {
		switch {
		case long != nil:
			if !long.prompt() {
				return false
			}
		case len(carry) == 0 || !isUserLine(carry) || parseClaudeLine(carry, start, transcript.cwd).kind != kindPrompt:
			return false
		}
		found++
		return found >= turns
	}
	for position := end; position > floor; {
		size := min(int64(chunkSize), position-floor)
		position -= size
		chunk := buffer[:size]
		if _, err := transcript.reader.ReadAt(chunk, position); err != nil {
			return end
		}
		stop := len(chunk)
		for index := stop - 1; index >= 0; index-- {
			if chunk[index] != '\n' {
				continue
			}
			start := position + int64(index) + 1
			if long != nil {
				long.add(chunk[index+1 : stop])
			} else {
				carry = append(append([]byte(nil), chunk[index+1:stop]...), carry...)
			}
			if prompt(start) {
				return start
			}
			carry, long, stop = nil, nil, index
		}
		switch {
		case long != nil:
			long.add(chunk[:stop])
		case stop+len(carry) > maxLine:
			long = &reverseScan{}
			long.add(carry)
			long.add(chunk[:stop])
			carry = nil
		default:
			carry = append(append([]byte(nil), chunk[:stop]...), carry...)
		}
	}
	if floor == 0 {
		return 0
	}
	// The budget ended inside the file: start at the first full line in it,
	// or, when one line fills the whole budget, at that line.
	if start := firstLineAfter(transcript.reader, floor, end); start < end {
		return start
	}
	return lineStart(transcript.reader, end)
}

// reverseScan checks a long line for its keys while it is read backward.
type reverseScan struct {
	head                           []byte // the first bytes of what was seen, for keys over a chunk edge
	user, toolUse, sidechain, meta bool
}

func (scan *reverseScan) add(part []byte) {
	window := append(append([]byte(nil), part...), scan.head...)
	scan.user = scan.user || bytes.Contains(window, userKey)
	scan.toolUse = scan.toolUse || bytes.Contains(window, toolUseKey)
	scan.sidechain = scan.sidechain || bytes.Contains(window, sidechainKey)
	scan.meta = scan.meta || bytes.Contains(window, metaKey) || bytes.Contains(window, compactKey)
	scan.head = window[:min(len(window), bigOverlap)]
}

func (scan *reverseScan) prompt() bool {
	return scan.user && !scan.toolUse && !scan.sidechain && !scan.meta
}

// lineStart is the start of the line that ends at end (reading back as far
// as that line goes).
func lineStart(file io.ReaderAt, end int64) int64 {
	buffer := make([]byte, chunkSize)
	limit := end - 1 // the line feed that ends the line
	for position := limit; position > 0; {
		count := min(int64(chunkSize), position)
		position -= count
		if _, err := file.ReadAt(buffer[:count], position); err != nil {
			return end
		}
		if index := bytes.LastIndexByte(buffer[:count], '\n'); index >= 0 {
			return position + int64(index) + 1
		}
	}
	return 0
}

func firstLineAfter(file io.ReaderAt, from, end int64) int64 {
	buffer := make([]byte, chunkSize)
	for position := from - 1; position < end; position += chunkSize {
		count, err := file.ReadAt(buffer[:min(int64(chunkSize), end-position)], position)
		if index := bytes.IndexByte(buffer[:count], '\n'); index >= 0 {
			return position + int64(index) + 1
		}
		if err != nil {
			break
		}
	}
	return end
}

// lastLineEnd is the offset after the last line feed at or before size, so
// live never starts inside a line (review: low).
func lastLineEnd(file io.ReaderAt, size int64) int64 {
	buffer := make([]byte, chunkSize)
	for position := size; position > 0; {
		count := min(int64(chunkSize), position)
		position -= count
		if _, err := file.ReadAt(buffer[:count], position); err != nil {
			return 0
		}
		if index := bytes.LastIndexByte(buffer[:count], '\n'); index >= 0 {
			return position + int64(index) + 1
		}
	}
	return 0
}

func (transcript *Transcript) issueLocked(position cursor) string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	transcript.cursors[token] = position
	transcript.issued = append(transcript.issued, token)
	if len(transcript.issued) > maxCursors {
		delete(transcript.cursors, transcript.issued[0])
		transcript.issued = transcript.issued[1:]
	}
	return token
}

func newHistoryID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}
