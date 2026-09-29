// Package gitchanges reads the uncommitted changes of a git repository for the
// Changes view (docs/changes.md). It runs only read-only git commands, never
// a shell, and bounds time, output and result size. Nothing writes to the
// repository: optional locks, the fsmonitor and the untracked cache are off.
package gitchanges

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	timeout       = 5 * time.Second
	maxOutput     = 4 << 20  // bytes of git stdout read per command
	patchBudget   = 96 << 10 // bytes of diff text per changes.patch
	pathBudget    = 128 << 10
	untrackedRead = 1 << 20 // bytes read per untracked file to count lines
	binaryProbe   = 8000    // git's rule: a NUL in the first 8000 bytes means binary
)

var gitFlags = []string{
	"-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "color.ui=false", "--literal-pathspecs",
}

var diffFlags = []string{"--no-ext-diff", "--no-textconv", "--no-color", "-M"}

// entry is one changed file. raw and rawOld are git's paths; file holds their
// UTF-8 form, which the phone sees and sends back.
type entry struct {
	file        protocol.ChangedFile
	raw, rawOld string
}

type repository struct {
	root string
	head string // short HEAD commit; empty before the first commit
}

// Summary lists the changes against HEAD (staged and unstaged) plus untracked
// files that git does not ignore, for the repository that holds dir.
func Summary(ctx context.Context, dir string) protocol.ChangesSummaryPayload {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	summary := protocol.ChangesSummaryPayload{State: protocol.ChangesStateUnavailable, Files: []protocol.ChangedFile{}}
	repo, state := open(ctx, dir)
	if state != protocol.ChangesStateReady {
		summary.State = state
		return summary
	}
	entries, truncated, err := list(ctx, repo)
	if err != nil {
		return summary
	}
	budget := pathBudget
	for _, change := range entries {
		size := len(change.file.Path) + len(valueOf(change.file.OldPath))
		if len(summary.Files) == protocol.MaxChangedFiles || size > budget {
			truncated = true
			break
		}
		budget -= size
		if ctx.Err() != nil {
			return protocol.ChangesSummaryPayload{State: protocol.ChangesStateUnavailable, Files: []protocol.ChangedFile{}}
		}
		if change.file.Status == protocol.Untracked {
			change.file.Additions, change.file.Binary = countLines(filepath.Join(repo.root, filepath.FromSlash(change.raw)))
		}
		summary.Files = append(summary.Files, change.file)
	}
	summary.State, summary.Truncated = protocol.ChangesStateReady, truncated
	if root := validUTF8(repo.root); utf8.RuneCountInString(root) <= 4096 {
		summary.Root = &root
	}
	branch := repo.head
	if out, _, err := run(ctx, repo.root, "symbolic-ref", "-q", "--short", "HEAD"); err == nil {
		branch = validUTF8(strings.TrimSpace(string(out)))
	}
	if branch != "" && utf8.RuneCountInString(branch) <= 256 {
		summary.Branch = &branch
	}
	return summary
}

// Patch returns the diff of one path of a fresh listing (its new or old path).
// Any other path is missing, so a viewer cannot read files that are unchanged
// or ignored.
func Patch(ctx context.Context, dir, path string) protocol.ChangesPatchPayload {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	patch := protocol.ChangesPatchPayload{Path: path, State: protocol.PatchStateUnavailable, Lines: []protocol.DiffLine{}}
	repo, state := open(ctx, dir)
	if state == protocol.NotGit {
		patch.State = protocol.Missing
	}
	if state != protocol.ChangesStateReady {
		return patch
	}
	entries, _, err := list(ctx, repo)
	if err != nil {
		return patch
	}
	var change *entry
	for index := range entries {
		if entries[index].file.Path == path || valueOf(entries[index].file.OldPath) == path {
			change = &entries[index]
			break
		}
	}
	if change == nil {
		patch.State = protocol.Missing
		return patch
	}
	var out []byte
	var truncated bool
	if change.file.Status == protocol.Untracked {
		// --no-index exits 1 when the files differ, which they always do.
		out, truncated, err = run(ctx, repo.root, append(append([]string{"diff", "--no-index"}, diffFlags...), "--", os.DevNull, change.raw)...)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			err = nil
		}
	} else {
		var base string
		if base, err = diffBase(ctx, repo); err == nil {
			args := append(append([]string{"diff"}, diffFlags...), "-U3", base, "--")
			if change.rawOld != "" {
				args = append(args, change.rawOld)
			}
			out, truncated, err = run(ctx, repo.root, append(args, change.raw)...)
		}
	}
	if err != nil {
		return patch
	}
	lines, binary, cut := parseDiff(out)
	if binary && len(lines) == 0 {
		patch.State = protocol.Binary
		return patch
	}
	patch.State, patch.Lines, patch.Truncated = protocol.PatchStateReady, lines, cut || truncated
	return patch
}

// open finds the repository root and HEAD. A folder outside any repository is
// not_git; a missing git or any other failure is unavailable.
func open(ctx context.Context, dir string) (repository, protocol.ChangesState) {
	out, _, err := run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return repository{}, protocol.NotGit
		}
		return repository{}, protocol.ChangesStateUnavailable
	}
	repo := repository{root: strings.TrimSuffix(string(out), "\n")}
	if repo.root == "" {
		return repository{}, protocol.ChangesStateUnavailable
	}
	if out, _, err := run(ctx, repo.root, "rev-parse", "-q", "--verify", "--short", "HEAD"); err == nil {
		repo.head = strings.TrimSpace(string(out))
	}
	return repo, protocol.ChangesStateReady
}

// diffBase is HEAD, or the empty tree of the repository's hash before the
// first commit.
func diffBase(ctx context.Context, repo repository) (string, error) {
	if repo.head != "" {
		return "HEAD", nil
	}
	out, _, err := run(ctx, repo.root, "hash-object", "-t", "tree", "--stdin")
	return strings.TrimSpace(string(out)), err
}

// list returns every change sorted by path: tracked changes against the diff
// base (renames, deletes, type changes, +/- counts) and untracked files.
// Untracked counts are left to the caller. truncated means git output was cut
// or a path could not be sent.
func list(ctx context.Context, repo repository) ([]entry, bool, error) {
	base, err := diffBase(ctx, repo)
	if err != nil {
		return nil, false, err
	}
	out, truncated, err := run(ctx, repo.root, append(append([]string{"diff", "--raw", "--numstat", "-z"}, diffFlags...), base, "--")...)
	if err != nil {
		return nil, false, err
	}
	entries := parseRawNumstat(out)
	out, cut, err := run(ctx, repo.root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, false, err
	}
	truncated = truncated || cut
	for _, raw := range nulFields(out) {
		entries = append(entries, entry{raw: raw, file: protocol.ChangedFile{Status: protocol.Untracked}})
	}
	kept := entries[:0]
	for _, change := range entries {
		change.file.Path = validUTF8(change.raw)
		if change.rawOld != "" {
			old := validUTF8(change.rawOld)
			change.file.OldPath = &old
		}
		if protocol.ValidateChangePath(change.file.Path) != nil ||
			(change.file.OldPath != nil && protocol.ValidateChangePath(*change.file.OldPath) != nil) {
			truncated = true
			continue
		}
		kept = append(kept, change)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].file.Path < kept[j].file.Path })
	return kept, truncated, nil
}

// parseRawNumstat reads `git diff --raw --numstat -z`: first one raw record
// per file (":modes hashes STATUS\0path\0", two paths for a rename), then one
// numstat record ("add\tdel\tpath\0", or "add\tdel\t\0old\0new\0").
func parseRawNumstat(out []byte) []entry {
	tokens := nulFields(out)
	var entries []entry
	index := map[string]int{}
	for i := 0; i < len(tokens); {
		if strings.HasPrefix(tokens[i], ":") {
			fields := strings.Fields(tokens[i])
			paths := 1
			if len(fields) == 5 && (fields[4][0] == 'R' || fields[4][0] == 'C') {
				paths = 2
			}
			if len(fields) != 5 || i+paths >= len(tokens) {
				break
			}
			change := entry{raw: tokens[i+paths], file: protocol.ChangedFile{Status: changeStatus(fields[4][0])}}
			if paths == 2 && fields[4][0] == 'R' {
				change.rawOld = tokens[i+1]
			}
			if _, seen := index[change.raw]; !seen {
				index[change.raw] = len(entries)
				entries = append(entries, change)
			}
			i += 1 + paths
			continue
		}
		counts := strings.SplitN(tokens[i], "\t", 3)
		if len(counts) != 3 {
			break
		}
		path, step := counts[2], 1
		if path == "" {
			if i+2 >= len(tokens) {
				break
			}
			path, step = tokens[i+2], 3
		}
		if position, ok := index[path]; ok {
			file := &entries[position].file
			if counts[0] == "-" {
				file.Binary = true
			} else {
				file.Additions, _ = strconv.ParseInt(counts[0], 10, 64)
				file.Deletions, _ = strconv.ParseInt(counts[1], 10, 64)
			}
		}
		i += step
	}
	return entries
}

func changeStatus(letter byte) protocol.ChangeStatus {
	switch letter {
	case 'A', 'C':
		return protocol.Added
	case 'D':
		return protocol.Deleted
	case 'R':
		return protocol.Renamed
	case 'T':
		return protocol.Typechange
	default: // M, U (unmerged) and anything newer
		return protocol.Modified
	}
}

// countLines counts the lines git would show as added for an untracked file.
// ponytail: only the first untrackedRead bytes are counted; a larger file
// shows a lower count.
func countLines(path string) (int64, bool) {
	info, err := os.Lstat(path)
	switch {
	case err != nil:
		return 0, false
	case info.Mode()&os.ModeSymlink != 0:
		return 1, false // git shows the link target as one line
	case !info.Mode().IsRegular():
		return 0, false
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer file.Close()
	data, _ := io.ReadAll(io.LimitReader(file, untrackedRead))
	if bytes.IndexByte(data[:min(len(data), binaryProbe)], 0) >= 0 {
		return 0, true
	}
	lines := int64(bytes.Count(data, []byte{'\n'}))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	return lines, false
}

// parseDiff turns unified diff output into lines without file headers. It
// reports "Binary files … differ" and whether the line or byte budget cut it.
func parseDiff(out []byte) (lines []protocol.DiffLine, binary, cut bool) {
	lines = []protocol.DiffLine{}
	var oldLine, newLine int64
	inHunk, used := false, 0
	for len(out) > 0 {
		var raw []byte
		raw, out, _ = bytes.Cut(out, []byte{'\n'})
		text := string(raw)
		line := protocol.DiffLine{}
		switch {
		case strings.HasPrefix(text, "@@ "):
			oldLine, newLine, inHunk = hunkStart(text)
			if !inHunk {
				continue
			}
			line.Kind = protocol.Hunk
		case inHunk && (text == "" || text[0] == ' '): // diff.suppressBlankEmpty drops the space
			line.Kind, line.OldLine, line.NewLine = protocol.Context, number(oldLine), number(newLine)
			oldLine, newLine = oldLine+1, newLine+1
			text = strings.TrimPrefix(text, " ")
		case inHunk && text[0] == '-':
			line.Kind, line.OldLine, text = protocol.Remove, number(oldLine), text[1:]
			oldLine++
		case inHunk && text[0] == '+':
			line.Kind, line.NewLine, text = protocol.Add, number(newLine), text[1:]
			newLine++
		case inHunk && text[0] == '\\': // "\ No newline at end of file"
			continue
		default: // file headers
			inHunk = false
			binary = binary || strings.HasPrefix(text, "Binary files ")
			continue
		}
		line.Text = clip(text)
		used += len(line.Text)
		if len(lines) == protocol.MaxDiffLines || used > patchBudget {
			return lines, binary, true
		}
		lines = append(lines, line)
	}
	return lines, binary, false
}

// hunkStart reads the first old and new line numbers of "@@ -a,b +c,d @@".
func hunkStart(text string) (int64, int64, bool) {
	fields := strings.Fields(text)
	if len(fields) < 4 || fields[3] != "@@" || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, false
	}
	oldStart, _, _ := strings.Cut(fields[1][1:], ",")
	newStart, _, _ := strings.Cut(fields[2][1:], ",")
	oldLine, oldErr := strconv.ParseInt(oldStart, 10, 64)
	newLine, newErr := strconv.ParseInt(newStart, 10, 64)
	return oldLine, newLine, oldErr == nil && newErr == nil && oldLine >= 0 && newLine >= 0
}

// number keeps line numbers valid (≥1) even for a malformed hunk.
func number(line int64) *int64 {
	return &[]int64{max(line, 1)}[0]
}

// clip makes text valid UTF-8 and cuts it to MaxDiffLineRunes runes.
func clip(text string) string {
	text = validUTF8(text)
	count := 0
	for index := range text {
		if count == protocol.MaxDiffLineRunes {
			return text[:index]
		}
		count++
	}
	return text
}

// nulFields splits -z output. A last field without its NUL was cut by the
// output limit and is dropped.
func nulFields(out []byte) []string {
	fields := strings.Split(string(out), "\x00")
	return fields[:len(fields)-1]
}

func valueOf(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// run executes git in dir with the safety flags and environment. It reads at
// most maxOutput bytes of stdout and kills git beyond it (truncated). The
// error includes the start of stderr so callers can tell "not a git
// repository" apart.
func run(ctx context.Context, dir string, args ...string) ([]byte, bool, error) {
	cmd := exec.CommandContext(ctx, "git", append(append([]string{}, gitFlags...), args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	cmd.WaitDelay = time.Second
	var stderr prefix
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, maxOutput+1))
	truncated := len(out) > maxOutput
	if truncated {
		out = out[:maxOutput]
		_ = cmd.Process.Kill()
	}
	err = cmd.Wait()
	if truncated && ctx.Err() == nil {
		return out, true, nil
	}
	if err == nil {
		err = readErr
	}
	if err != nil {
		return out, false, fmt.Errorf("git %s: %w: %s", args[0], err, bytes.TrimSpace(stderr))
	}
	return out, false, nil
}

// prefix keeps the first KiB of stderr, enough for git's error message.
type prefix []byte

func (buffer *prefix) Write(data []byte) (int, error) {
	if room := 1024 - len(*buffer); room > 0 {
		*buffer = append(*buffer, data[:min(room, len(data))]...)
	}
	return len(data), nil
}

// validUTF8 replaces bytes that are not UTF-8, which git allows in paths and
// file content, with U+FFFD.
func validUTF8(text string) string {
	return strings.ToValidUTF8(text, string(utf8.RuneError))
}
