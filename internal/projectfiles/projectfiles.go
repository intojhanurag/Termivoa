// Package projectfiles reads the session's project folder for the Files view
// (files.list, files.read, files.search). It only reads. Every file access
// goes through an os.Root opened at the files root, so .. and symlinks cannot
// leave it, and .git is never listed or read.
package projectfiles

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/gitchanges"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	// MaxFileBytes is the largest file files.read opens (too_large above).
	MaxFileBytes = 50 << 20
	// textBudget bounds the JSON-escaped text so a files.content reply stays
	// under protocol.MaxOutboundMessageBytes (1 MiB) whatever the content:
	// 768 KiB of text plus a path of at most 4096 escaped runes.
	textBudget  = 768 << 10
	binaryProbe = 8 << 10 // a NUL byte in the first 8 KiB means binary
	// invalidShare: more than 1 in 100 bytes of invalid UTF-8 means binary;
	// fewer are replaced with U+FFFD.
	invalidShare = 100
)

// Variables so tests can lower them.
var (
	maxDirScan = 20000 // entries read from one folder before files.listed stops (truncated)
	maxWalk    = 20000 // entries visited by a search outside a repository
)

// CleanPath checks a path from the phone and returns it normalized: relative
// to the files root, / separators, no empty or . segments. "" is the root.
// Absolute paths and .. segments are refused.
func CleanPath(name string) (string, error) {
	if filepath.Separator == '\\' {
		name = strings.ReplaceAll(name, `\`, "/")
	}
	if name == "" {
		return "", nil
	}
	if err := protocol.ValidateChangePath(name); err != nil {
		return "", err
	}
	var parts []string
	for _, part := range strings.Split(name, "/") {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/"), nil
}

// gitName reports whether one path segment names .git. It ignores case
// (macOS and Windows), the trailing dots and spaces Windows drops, and the
// 8.3 short name Windows may give .git (GIT~1).
func gitName(segment string) bool {
	segment = strings.TrimRight(segment, ". ")
	if strings.EqualFold(segment, ".git") {
		return true
	}
	upper := strings.ToUpper(segment)
	return strings.HasPrefix(upper, "GIT~") && len(upper) > 4 && strings.Trim(upper[4:], "0123456789") == ""
}

func inGit(name string) bool {
	for _, segment := range strings.Split(name, "/") {
		if gitName(segment) {
			return true
		}
	}
	return false
}

// findRoot returns the files root of folder: the top level of its git
// repository, else folder itself (also when git is missing).
func findRoot(ctx context.Context, folder string) (root string, repo bool) {
	if top, state := gitchanges.TopLevel(ctx, folder); state == protocol.ChangesStateReady {
		return top, true
	}
	return folder, false
}

func slashRoot(root string) *string {
	slashed := filepath.ToSlash(root)
	if slashed == "" || utf8.RuneCountInString(slashed) > 4096 || !utf8.ValidString(slashed) {
		return nil
	}
	return &slashed
}

// missing tells a path that is not there (or escapes the root, or passes
// through a file) from one that could not be read.
func missing(err error) bool {
	return !errors.Is(err, fs.ErrPermission)
}

func rootName(name string) string {
	if name == "" {
		return "."
	}
	return name
}

// List lists one folder of the files root. name must come from CleanPath.
func List(ctx context.Context, folder, name string) protocol.FilesListedPayload {
	reply := protocol.FilesListedPayload{State: protocol.FilesListStateUnavailable, Path: name, Entries: []protocol.FileEntry{}}
	root, _ := findRoot(ctx, folder)
	reply.Root = slashRoot(root)
	if inGit(name) {
		reply.State = protocol.FilesListStateNotFound
		return reply
	}
	files, err := os.OpenRoot(root)
	if err != nil {
		return reply
	}
	defer files.Close()
	dir, err := files.Open(rootName(name))
	if err != nil {
		if missing(err) {
			reply.State = protocol.FilesListStateNotFound
		}
		return reply
	}
	defer dir.Close()
	if info, err := dir.Stat(); err != nil {
		return reply
	} else if !info.IsDir() {
		reply.State = protocol.NotDir
		return reply
	}
	entries, truncated, err := readEntries(ctx, files, dir, name)
	if err != nil {
		return reply
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if (a.Kind == protocol.Dir) != (b.Kind == protocol.Dir) {
			return a.Kind == protocol.Dir
		}
		if la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name); la != lb {
			return la < lb
		}
		return a.Name < b.Name
	})
	if len(entries) > protocol.MaxFileEntries {
		entries, truncated = entries[:protocol.MaxFileEntries], true
	}
	reply.State, reply.Entries, reply.Truncated = protocol.FilesListStateReady, entries, truncated
	return reply
}

func readEntries(ctx context.Context, files *os.Root, dir *os.File, name string) ([]protocol.FileEntry, bool, error) {
	entries := []protocol.FileEntry{}
	truncated, scanned := false, 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		batch, err := dir.ReadDir(256)
		for _, item := range batch {
			if gitName(item.Name()) {
				continue
			}
			entry, ok := fileEntry(files, path.Join(name, item.Name()), item)
			if !ok {
				truncated = true
				continue
			}
			entries = append(entries, entry)
		}
		scanned += len(batch)
		if errors.Is(err, io.EOF) || (err == nil && len(batch) == 0) {
			return entries, truncated, nil
		}
		if err != nil {
			return nil, false, err
		}
		if scanned >= maxDirScan {
			return entries, true, nil
		}
	}
}

// fileEntry describes one folder entry. A symlink shows as its target's kind
// when the target is inside the root, else as link. ok is false for a name
// the protocol cannot carry or an entry that vanished.
func fileEntry(files *os.Root, name string, item fs.DirEntry) (protocol.FileEntry, bool) {
	entry := protocol.FileEntry{Name: strings.ToValidUTF8(item.Name(), string(utf8.RuneError)), Kind: protocol.FileKindOther}
	if utf8.RuneCountInString(entry.Name) > 255 {
		return entry, false
	}
	mode := item.Type()
	var info fs.FileInfo
	var err error
	switch {
	case mode&fs.ModeSymlink != 0:
		entry.Kind = protocol.Link
		if info, err = files.Stat(name); err != nil {
			return entry, true // Points outside the root, or nowhere.
		}
		mode = info.Mode().Type()
	case mode.IsRegular():
		if info, err = item.Info(); err != nil {
			return entry, false
		}
	}
	switch {
	case mode.IsDir():
		entry.Kind = protocol.Dir
	case mode.IsRegular():
		size := info.Size()
		entry.Kind, entry.Size = protocol.File, &size
	}
	return entry, true
}

// Read returns the text of one file of the files root. name must come from
// CleanPath and not be empty.
func Read(ctx context.Context, folder, name string) protocol.FilesContentPayload {
	reply := protocol.FilesContentPayload{State: protocol.FilesReadStateUnavailable, Path: name}
	root, _ := findRoot(ctx, folder)
	if inGit(name) {
		reply.State = protocol.FilesReadStateNotFound
		return reply
	}
	files, err := os.OpenRoot(root)
	if err != nil {
		return reply
	}
	defer files.Close()
	info, err := files.Stat(name)
	if err != nil {
		if missing(err) {
			reply.State = protocol.FilesReadStateNotFound
		}
		return reply
	}
	if !info.Mode().IsRegular() {
		reply.State = protocol.NotFile
		return reply
	}
	size := info.Size()
	reply.Size = &size
	if size > MaxFileBytes {
		reply.State = protocol.TooLarge
		return reply
	}
	// ponytail: a file swapped for a FIFO between Stat and Open would block
	// this read until its viewer leaves; the same holds for projecttasks.
	file, err := files.Open(name)
	if err != nil {
		return reply
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, protocol.MaxFileTextBytes+1))
	if err != nil {
		return reply
	}
	if bytes.IndexByte(data[:min(len(data), binaryProbe)], 0) >= 0 {
		reply.State = protocol.FilesReadStateBinary
		return reply
	}
	truncated := len(data) > protocol.MaxFileTextBytes
	if truncated {
		data = cutAtLine(data[:protocol.MaxFileTextBytes])
	}
	if (invalidBytes(data)+bytes.Count(data, []byte{0}))*invalidShare > len(data) {
		reply.State = protocol.FilesReadStateBinary
		return reply
	}
	// A NUL past the probe is replaced like invalid UTF-8: text never holds one.
	text := strings.ReplaceAll(strings.ToValidUTF8(string(data), string(utf8.RuneError)), "\x00", string(utf8.RuneError))
	text, cut := fitText(text)
	reply.State, reply.Text, reply.Truncated = protocol.FilesReadStateReady, &text, truncated || cut
	return reply
}

// cutAtLine keeps data up to and including its last line break, or, with
// none, drops a rune the cut split.
func cutAtLine(data []byte) []byte {
	if end := bytes.LastIndexByte(data, '\n'); end >= 0 {
		return data[:end+1]
	}
	for back := 1; back <= utf8.UTFMax && back <= len(data); back++ {
		if utf8.RuneStart(data[len(data)-back]) {
			if !utf8.FullRune(data[len(data)-back:]) {
				return data[:len(data)-back]
			}
			break
		}
	}
	return data
}

func invalidBytes(data []byte) int {
	invalid := 0
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			invalid++
		}
		data = data[size:]
	}
	return invalid
}

// fitText cuts valid UTF-8 text at a line break so it has at most
// MaxFileTextBytes bytes and its JSON form at most textBudget bytes. U+FFFD
// replacements can grow the text, and encoding/json escapes some runes to six
// bytes (control characters, <, >, &, U+2028, U+2029).
func fitText(text string) (string, bool) {
	cost, lastLine := 0, -1
	for index, r := range text {
		size := utf8.RuneLen(r)
		switch {
		case r == '"' || r == '\\' || r == '\n' || r == '\r' || r == '\t':
			cost += 2
		case r < 0x20 || r == '<' || r == '>' || r == '&' || r == ' ' || r == ' ':
			cost += 6
		default:
			cost += size
		}
		if cost > textBudget || index+size > protocol.MaxFileTextBytes {
			if lastLine >= 0 {
				return text[:lastLine+1], true
			}
			return text[:index], true
		}
		if r == '\n' {
			lastLine = index
		}
	}
	return text, false
}

// Search finds files of the files root whose relative path contains query,
// ignoring case.
func Search(ctx context.Context, folder, query string) protocol.FilesFoundPayload {
	reply := protocol.FilesFoundPayload{State: protocol.FilesSearchStateUnavailable, Query: query, Paths: []string{}}
	root, repo := findRoot(ctx, folder)
	var candidates []string
	var truncated bool
	var err error
	if repo {
		candidates, truncated, err = gitchanges.Files(ctx, root)
	} else {
		candidates, truncated, err = walk(ctx, root)
	}
	if err != nil {
		return reply
	}
	needle := strings.ToLower(query)
	type match struct {
		path     string
		baseName bool
	}
	seen := map[string]bool{}
	var matches []match
	for _, raw := range candidates {
		name := strings.ToValidUTF8(raw, string(utf8.RuneError))
		lower := strings.ToLower(name)
		if seen[name] || strings.HasSuffix(name, "/") || !strings.Contains(lower, needle) || inGit(name) {
			continue
		}
		seen[name] = true
		if utf8.RuneCountInString(name) > 4096 {
			truncated = true
			continue
		}
		matches = append(matches, match{name, strings.Contains(path.Base(lower), needle)})
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.baseName != b.baseName {
			return a.baseName
		}
		if len(a.path) != len(b.path) {
			return len(a.path) < len(b.path)
		}
		return a.path < b.path
	})
	if len(matches) > protocol.MaxFoundPaths {
		matches, truncated = matches[:protocol.MaxFoundPaths], true
	}
	for _, found := range matches {
		reply.Paths = append(reply.Paths, found.path)
	}
	reply.State, reply.Truncated = protocol.FilesSearchStateReady, truncated
	return reply
}

// walk lists the files under root outside a repository: at most maxWalk
// entries, without .git and node_modules folders and without following
// symlinks.
func walk(ctx context.Context, root string) ([]string, bool, error) {
	files, err := os.OpenRoot(root)
	if err != nil {
		return nil, false, err
	}
	defer files.Close()
	var found []string
	visited, truncated := 0, false
	err = fs.WalkDir(files.FS(), ".", func(name string, item fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if name == "." {
			return err
		}
		if visited++; visited > maxWalk {
			truncated = true
			return fs.SkipAll
		}
		if err != nil {
			return nil // An unreadable folder is skipped.
		}
		if item.IsDir() {
			if gitName(item.Name()) || item.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		found = append(found, name)
		return nil
	})
	return found, truncated, err
}
