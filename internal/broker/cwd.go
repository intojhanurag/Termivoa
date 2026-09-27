package broker

import (
	"context"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

// Live working folder (docs/live-folder.md). Two sources, both display-only:
// the operating system (macOS: lsof, Linux: /proc; see
// ptyhost.WorkingDirectory) and OSC 7 / OSC 9;9 reports in terminal output
// (the PowerShell hook on Windows, or a shell that emits them itself). Work is
// bounded: one check cwdCheckDelay after output or input, at most one per
// cwdCheckEvery, one at start, and none while the session is quiet.
const (
	cwdCheckDelay   = 500 * time.Millisecond
	cwdCheckEvery   = 1500 * time.Millisecond
	cwdCheckTimeout = 3 * time.Second
)

type directoryReader interface {
	WorkingDirectory(context.Context) (string, error)
}

type directoryReporter interface {
	DirectoryReport() terminal.DirectoryReport
}

// readOSDirectory returns process's OS working-directory reader, or one that
// always reports nothing (Windows, tests).
func readOSDirectory(process processRuntime) func(context.Context) (string, error) {
	if reader, ok := process.(directoryReader); ok {
		return reader.WorkingDirectory
	}
	return func(context.Context) (string, error) { return "", nil }
}

// cwdTracker picks the published folder. A fresh OSC report wins; the OS
// value then takes over only once it changes from what it was at that report,
// so a shell reporting a logical path (/tmp) does not flap against the OS
// physical path (/private/tmp). Actor-owned.
type cwdTracker struct {
	current  string
	oscSeq   uint64
	oscMode  bool
	osAtOSC  string
	checking bool
}

// next applies one check. osPath is the OS value ("" when unknown); report is
// the terminal's newest OSC report. It returns whether current changed.
func (tracker *cwdTracker) next(osPath string, report terminal.DirectoryReport) bool {
	previous := tracker.current
	osPath = safeDirectory(osPath)
	fresh := report.Seq != tracker.oscSeq
	tracker.oscSeq = report.Seq
	switch reported := directoryFromReport(report.Payload); {
	case fresh && reported != "":
		tracker.current, tracker.oscMode, tracker.osAtOSC = reported, true, osPath
	case osPath == "":
	case !tracker.oscMode || osPath != tracker.osAtOSC:
		tracker.current, tracker.oscMode = osPath, false
	}
	return tracker.current != previous
}

// directoryFromReport extracts the path of an OSC 7 ("7;file://host/path",
// percent-encoded) or OSC 9;9 ("9;9;\"C:\\dir\"") payload. It returns "" for
// anything malformed, relative or unsafe to display.
func directoryFromReport(payload string) string {
	if rest, ok := strings.CutPrefix(payload, "9;9;"); ok {
		if len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"' {
			rest = rest[1 : len(rest)-1]
		}
		return safeDirectory(rest)
	}
	rest, ok := strings.CutPrefix(payload, "7;")
	if !ok {
		return ""
	}
	location, err := url.Parse(rest)
	if err != nil || !strings.EqualFold(location.Scheme, "file") || location.Opaque != "" ||
		location.User != nil || location.RawQuery != "" || location.Fragment != "" {
		return ""
	}
	path := location.Path
	// file:///C:/dir names a Windows drive path.
	if len(path) >= 4 && path[0] == '/' && path[2] == ':' && path[3] == '/' {
		path = path[1:]
	}
	return safeDirectory(path)
}

// safeDirectory returns path when it is a valid protocol cwd, else "". Like
// the screen preview, it shows other non-printable runes (bidi overrides,
// format and private-use characters) as U+FFFD.
func safeDirectory(path string) string {
	if protocol.ValidateWorkingDirectory(path) != nil {
		return ""
	}
	return strings.Map(func(character rune) rune {
		if character == ' ' || character == zeroWidthJoiner || unicode.IsPrint(character) {
			return character
		}
		return utf8.RuneError
	}, path)
}

func cwdPayload(cwd string) *string {
	if cwd == "" {
		return nil
	}
	return &cwd
}
