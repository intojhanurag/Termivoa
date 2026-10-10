package transcript

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Binding (docs/chat-view.md, section 4.3 and review S2, S4, S5, F1). It
// never guesses: no process-name matching, no newest file, no folder-only
// match. Nothing is read from a transcript before its header matches.

const (
	maxPIDFile      = 4 << 10
	maxHeaderRead   = 64 << 10
	maxSessionFiles = 256
	startSlack      = 5 * time.Second
	sameStart       = 2 * time.Second // Linux start times move with btime
)

var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// maxProjectDirs bounds the project folders a bind reads (a variable for tests).
var maxProjectDirs = 10000

var (
	errRefused        = errors.New("transcript: file refused")
	errTooManyFolders = errors.New("transcript: too many project folders")
)

// Process is one process of the session's tree.
type Process struct {
	PID   int
	Start time.Time
}

// Config names the agent folder; cmd/pterm builds it from the user's home.
type Config struct {
	// Claude is Claude Code's folder (~/.claude). Empty: no agent folder.
	Claude string
	// CustomRoot is set when CLAUDE_CONFIG_DIR is set for the broker: the
	// agent may write elsewhere, which v1 does not follow.
	CustomRoot bool
	// Dir is the OS working folder of a process (lsof on macOS, /proc on
	// Linux); called only at bind time (review F11).
	Dir func(context.Context, int) (string, error)
}

// Link is the result of a bind.
type Link struct {
	State      string // a protocol.ChatLink* word
	Reason     string // a protocol.ChatReason* word, with none
	Transcript *Transcript
}

type pidRecord struct {
	PID       int             `json:"pid"`
	SessionID string          `json:"sessionId"`
	StartedAt json.RawMessage `json:"startedAt"`
}

// Bind links the session to one Claude Code transcript: a process of the
// tree with its own <claude>/sessions/<pid>.json (pid, start within 5 s,
// session UUID), exactly one projects/*/<sessionId>.jsonl, in the folder
// that is Claude's encoding of the process's OS working folder, whose
// header names the same session and folder.
func Bind(ctx context.Context, config Config, processes []Process) Link {
	if config.Claude == "" {
		return Link{State: protocol.ChatLinkUnsupported}
	}
	guardRoot(config.Claude)
	if ctx.Err() != nil {
		return Link{State: protocol.ChatLinkOff}
	}
	root, err := os.OpenRoot(config.Claude)
	if err != nil {
		if config.CustomRoot {
			return none(protocol.ChatReasonCustomRoot)
		}
		return Link{State: protocol.ChatLinkUnsupported}
	}
	defer root.Close()
	var agents []Process
	var sessionIDs []string
	for _, process := range processes {
		if sessionID, ok := readPIDFile(root, process); ok {
			agents, sessionIDs = append(agents, process), append(sessionIDs, sessionID)
		}
	}
	switch {
	case len(agents) == 0 && config.CustomRoot:
		return none(protocol.ChatReasonCustomRoot)
	case len(agents) == 0:
		return Link{State: protocol.ChatLinkUnsupported}
	case len(agents) > 1:
		return none(protocol.ChatReasonSeveralAgents)
	}
	agent, sessionID := agents[0], sessionIDs[0]
	if config.Dir == nil {
		return none(protocol.ChatReasonNoFile)
	}
	cwd, err := config.Dir(ctx, agent.PID)
	if err != nil || cwd == "" || !path.IsAbs(cwd) {
		return none(protocol.ChatReasonNoFile)
	}
	folders, err := sessionFolders(root, sessionID)
	switch {
	case err != nil || len(folders) > 1 || len(folders) == 1 && folders[0] != ProjectFolder(cwd):
		return none(protocol.ChatReasonNoFile)
	case len(folders) == 0:
		return Link{State: protocol.ChatLinkSearching}
	}
	name := "projects/" + folders[0] + "/" + sessionID + ".jsonl"
	file, info, dev, ino, err := openSafe(root, name)
	if err != nil {
		return none(protocol.ChatReasonFileRefused)
	}
	switch header(file, sessionID, cwd) {
	case headerMissing:
		_ = file.Close()
		return Link{State: protocol.ChatLinkSearching}
	case headerWrong:
		_ = file.Close()
		return none(protocol.ChatReasonNoFile)
	}
	transcript := newTranscript(file, agent, sessionID, cwd, name, dev, ino, info.Size())
	return Link{State: protocol.ChatLinkLinked, Transcript: transcript}
}

func none(reason string) Link { return Link{State: protocol.ChatLinkNone, Reason: reason} }

// ProjectFolder is Claude Code's folder name for a working folder: every
// character other than an ASCII letter or digit becomes "-". A long path
// that Claude shortens does not match, so it fails closed (no_file).
func ProjectFolder(cwd string) string {
	var out strings.Builder
	for index := 0; index < len(cwd); index++ {
		if c := cwd[index]; c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			out.WriteByte(c)
		} else {
			out.WriteByte('-')
		}
	}
	return out.String()
}

// readPIDFile reads <claude>/sessions/<pid>.json and checks it names this
// process: the same pid, a start within 5 s, a session UUID.
func readPIDFile(root *os.Root, process Process) (string, bool) {
	data, err := readSmall(root, pidFileName(process.PID))
	if err != nil {
		return "", false
	}
	var record pidRecord
	if json.Unmarshal(data, &record) != nil || record.PID != process.PID || !sessionIDPattern.MatchString(record.SessionID) {
		return "", false
	}
	started, ok := startedAt(record.StartedAt)
	if !ok || process.Start.IsZero() {
		return "", false
	}
	if gap := started.Sub(process.Start); gap > startSlack || gap < -startSlack {
		return "", false // a stale file of an earlier process with this pid
	}
	return record.SessionID, true
}

func pidFileName(pid int) string { return "sessions/" + strconv.Itoa(pid) + ".json" }

func startedAt(raw json.RawMessage) (time.Time, bool) {
	var millis int64
	if json.Unmarshal(raw, &millis) == nil && millis > 0 {
		return time.UnixMilli(millis), true
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if at, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

// readSmall reads a small safe file (at most 4 KiB).
func readSmall(root *os.Root, name string) ([]byte, error) {
	file, info, _, _, err := openSafe(root, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info.Size() > maxPIDFile {
		return nil, errRefused
	}
	return io.ReadAll(io.LimitReader(file, maxPIDFile))
}

// openSafe opens name under root through platform.OpenRegular after Lstat
// checks of every part: no symlink anywhere, a regular file with one name,
// owned by this user, not writable by group or others, and the opened
// handle is the file that was checked.
func openSafe(root *os.Root, name string) (*os.File, fs.FileInfo, uint64, uint64, error) {
	parts := strings.Split(name, "/")
	for index := 1; index < len(parts); index++ {
		info, err := root.Lstat(strings.Join(parts[:index], "/"))
		if err != nil || !info.IsDir() {
			return nil, nil, 0, 0, errRefused
		}
	}
	checked, err := root.Lstat(name)
	if err != nil || !checked.Mode().IsRegular() {
		return nil, nil, 0, 0, errRefused
	}
	dev, ino, safe := fileID(checked)
	if !safe {
		return nil, nil, 0, 0, errRefused
	}
	file, info, err := platform.OpenRegular(root, name, errRefused)
	if err != nil {
		return nil, nil, 0, 0, errRefused
	}
	if openDev, openIno, safe := fileID(info); !safe || openDev != dev || openIno != ino {
		_ = file.Close()
		return nil, nil, 0, 0, errRefused
	}
	return file, info, dev, ino, nil
}

// sessionFolders lists the project folders that hold <sessionID>.jsonl.
func sessionFolders(root *os.Root, sessionID string) ([]string, error) {
	projects, err := root.Open("projects")
	if err != nil {
		return nil, nil
	}
	defer projects.Close()
	// At most 10,000 project folders are read. With more, the folder that
	// holds the transcript may be unread, so the bind fails closed
	// (none(no_file)) instead of linking a wrong or partial match (S-7).
	entries, err := projects.ReadDir(maxProjectDirs)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) == maxProjectDirs {
		return nil, errTooManyFolders
	}
	var folders []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := root.Lstat("projects/" + entry.Name() + "/" + sessionID + ".jsonl"); err == nil {
			folders = append(folders, entry.Name())
		}
	}
	return folders, nil
}

type headerResult uint8

const (
	headerOK headerResult = iota
	headerMissing
	headerWrong
)

// header checks the first lines (at most 64 KiB) for the session and folder.
func header(file *os.File, sessionID, cwd string) headerResult {
	buffer := make([]byte, maxHeaderRead)
	count, _ := file.ReadAt(buffer, 0)
	lines := bytes.Split(buffer[:count], []byte{'\n'})
	for index, data := range lines {
		if index == len(lines)-1 {
			break // possibly partial
		}
		var line struct {
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
		}
		if json.Unmarshal(data, &line) != nil || line.SessionID == "" {
			continue
		}
		if line.SessionID != sessionID || line.Cwd != cwd {
			return headerWrong
		}
		return headerOK
	}
	return headerMissing
}

// Recheck results.
const (
	RecheckOK      = ""
	RecheckEnded   = "ended"
	RecheckRebind  = "rebind"
	RecheckCleared = "cleared"
)

// Recheck runs every 2 s while linked: is the agent still running, is the
// path still the file the handle holds, does the pid file still name this
// session, and (when idle) did /clear start a new conversation in the same
// folder (review S2: no guess, the link ends as none(cleared)).
func (transcript *Transcript) Recheck(config Config, processes []Process, idle bool) string {
	alive := false
	for _, process := range processes {
		if process.PID == transcript.Agent.PID && absDuration(process.Start.Sub(transcript.Agent.Start)) < sameStart {
			alive = true
			break
		}
	}
	if !alive {
		return RecheckEnded
	}
	guardRoot(config.Claude)
	root, err := os.OpenRoot(config.Claude)
	if err != nil {
		return RecheckRebind
	}
	defer root.Close()
	info, err := root.Lstat(transcript.name)
	if err != nil {
		return RecheckRebind
	}
	if dev, ino, _ := fileID(info); dev != transcript.dev || ino != transcript.ino {
		return RecheckRebind
	}
	if sessionID, ok := readPIDFile(root, transcript.Agent); ok && sessionID != transcript.SessionID {
		return RecheckRebind
	}
	// /clear only with evidence that ties it to this agent (review C-2): its
	// own pid file was written after our transcript stopped, and a newer
	// transcript in the folder is named by no pid file. Another claude (or
	// claude -p) in the same folder alone never ends the link.
	if idle {
		if pidInfo, err := root.Lstat(pidFileName(transcript.Agent.PID)); err == nil &&
			pidInfo.ModTime().After(info.ModTime()) && transcript.newerUnclaimed(root, info.ModTime()) {
			return RecheckCleared
		}
	}
	return RecheckOK
}

// newerUnclaimed reports a session file in the same folder written after
// ours whose session no live pid file names.
func (transcript *Transcript) newerUnclaimed(root *os.Root, written time.Time) bool {
	folder := path.Dir(transcript.name)
	directory, err := root.Open(folder)
	if err != nil {
		return false
	}
	entries, _ := directory.ReadDir(maxProjectDirs)
	_ = directory.Close()
	var newer []string
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".jsonl")
		if !ok || !sessionIDPattern.MatchString(name) || name == transcript.SessionID || !entry.Type().IsRegular() {
			continue
		}
		if info, err := entry.Info(); err == nil && info.ModTime().After(written) {
			newer = append(newer, name)
		}
	}
	if len(newer) == 0 {
		return false
	}
	claimed := map[string]bool{}
	if sessions, err := root.Open("sessions"); err == nil {
		files, _ := sessions.ReadDir(maxSessionFiles)
		_ = sessions.Close()
		for _, file := range files {
			if data, err := readSmall(root, "sessions/"+file.Name()); err == nil {
				var record pidRecord
				if json.Unmarshal(data, &record) == nil {
					claimed[record.SessionID] = true
				}
			}
		}
	}
	for _, name := range newer {
		if !claimed[name] {
			return true
		}
	}
	return false
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
