// Package projecttasks lists the commands a project defines (docs/project-tasks.md):
// package.json scripts, Makefile targets, and the Cargo and Go defaults. It
// only reads files inside the folder and never runs anything.
package projecttasks

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// maxFileBytes bounds each file read. A longer package.json no longer parses
// and fails; a longer Makefile is parsed up to its last whole line.
const maxFileBytes = 256 << 10

var errSkipped = errors.New("projecttasks: not a regular file")

// List reads the project files in dir and returns the tasks, most useful
// kinds first. A folder that cannot be opened, or known files that all fail
// to read or parse, give unavailable; no known file gives none.
func List(ctx context.Context, dir string) protocol.TasksListedPayload {
	result := protocol.TasksListedPayload{State: protocol.TasksStateUnavailable, Tasks: []protocol.ProjectTask{}}
	if dir != "" && utf8.ValidString(dir) && utf8.RuneCountInString(dir) <= 4096 {
		result.Folder = &dir
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return result
	}
	defer root.Close()

	var tasks []protocol.ProjectTask
	found, failed := false, false
	add := func(source protocol.TaskSource, kind protocol.TaskKind, name, command string) {
		tasks = append(tasks, protocol.ProjectTask{Name: name, Command: command, Source: source, Kind: kind})
	}
	// note records a file's read result and reports whether it can be used.
	note := func(err error) bool {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errSkipped) {
			return false
		}
		found = true
		failed = failed || err != nil
		return err == nil
	}

	if data, err := readFile(root, "package.json"); note(err) {
		scripts, manager, err := parsePackageJSON(data)
		if note(err) {
			if lock := lockfileManager(root); lock != "" {
				manager = lock
			}
			for _, name := range scripts {
				add(protocol.PackageJSON, kindOf(name), name, runScript(manager, name))
			}
		}
	}
	// make's own lookup order; on a case-insensitive disk these are one file.
	for _, name := range []string{"GNUmakefile", "makefile", "Makefile"} {
		if data, err := readFile(root, name); note(err) {
			for _, target := range makeTargets(data) {
				add(protocol.Makefile, kindOf(target), target, "make "+target)
			}
			break
		}
	}
	if _, err := readFile(root, "Cargo.toml"); note(err) {
		add(protocol.CargoToml, protocol.Test, "test", "cargo test")
		add(protocol.CargoToml, protocol.Dev, "run", "cargo run")
		add(protocol.CargoToml, protocol.Build, "build", "cargo build")
		add(protocol.CargoToml, protocol.Lint, "clippy", "cargo clippy")
	}
	if _, err := readFile(root, "go.mod"); note(err) {
		add(protocol.GoMod, protocol.Test, "test", "go test ./...")
		add(protocol.GoMod, protocol.Build, "build", "go build ./...")
		add(protocol.GoMod, protocol.Lint, "vet", "go vet ./...")
	}
	if ctx.Err() != nil {
		return result
	}
	switch {
	case len(tasks) == 0 && failed:
		return result
	case !found:
		result.State = protocol.TasksStateNone
		return result
	}
	// Stable: source order within a kind. "other" sorts last, so the cap
	// drops it first.
	rank := []protocol.TaskKind{protocol.Test, protocol.Dev, protocol.Build, protocol.Lint, protocol.TaskKindOther}
	slices.SortStableFunc(tasks, func(a, b protocol.ProjectTask) int {
		return slices.Index(rank, a.Kind) - slices.Index(rank, b.Kind)
	})
	result.State, result.Tasks = protocol.TasksStateReady, append(result.Tasks, tasks[:min(len(tasks), protocol.MaxProjectTasks)]...)
	return result
}

// readFile reads at most maxFileBytes+1 bytes of a regular file inside root.
// A symlink or anything else that is not a regular file is errSkipped.
func readFile(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errSkipped
	}
	file, opened, err := platform.OpenRegular(root, name, errSkipped)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if !os.SameFile(info, opened) {
		return nil, errSkipped // Replaced between the check and the open.
	}
	return io.ReadAll(io.LimitReader(file, maxFileBytes+1))
}

// parsePackageJSON returns the safe script names in file order and the
// packageManager field's tool name.
func parsePackageJSON(data []byte) ([]string, string, error) {
	if len(data) > maxFileBytes {
		return nil, "", errors.New("projecttasks: package.json is too large")
	}
	var manifest struct {
		Scripts        json.RawMessage `json:"scripts"`
		PackageManager string          `json:"packageManager"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, "", err
	}
	manager, _, _ := strings.Cut(manifest.PackageManager, "@")
	if !slices.Contains([]string{"npm", "pnpm", "yarn", "bun"}, manager) {
		manager = ""
	}
	// Walk the object by token: a map would lose the file's order.
	decoder := json.NewDecoder(bytes.NewReader(manifest.Scripts))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, manager, nil // Absent or not an object: no scripts.
	}
	var names []string
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, "", err
		}
		var command any
		if err := decoder.Decode(&command); err != nil {
			return nil, "", err
		}
		name, _ := token.(string)
		if _, isString := command.(string); isString && protocol.ValidTaskName(name) && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names, manager, nil
}

// lockfileManager picks the tool from the lockfile in the folder. It wins
// over the packageManager field.
func lockfileManager(root *os.Root) string {
	for _, lock := range []struct{ file, manager string }{
		{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}, {"package-lock.json", "npm"},
	} {
		if _, err := root.Lstat(lock.file); err == nil {
			return lock.manager
		}
	}
	return ""
}

// yarnCommands are yarn built-ins that would shadow a script of the same
// name in "yarn <name>", so those scripts keep "run".
var yarnCommands = []string{
	"add", "audit", "autoclean", "bin", "cache", "check", "config", "create", "dedupe", "dlx", "exec", "explain",
	"generate-lock-entry", "global", "help", "import", "info", "init", "install", "licenses", "link", "list",
	"login", "logout", "node", "npm", "outdated", "owner", "pack", "patch", "plugin", "policies", "publish",
	"rebuild", "remove", "run", "set", "tag", "team", "unlink", "unplug", "up", "upgrade", "upgrade-interactive",
	"version", "versions", "why", "workspace", "workspaces",
}

func runScript(manager, name string) string {
	switch manager {
	case "pnpm", "bun":
		return manager + " run " + name
	case "yarn":
		if slices.Contains(yarnCommands, name) {
			return "yarn run " + name
		}
		return "yarn " + name
	default:
		return "npm run " + name
	}
}

// makeTargets returns explicit target names in file order: lines "name:" or
// "a b: deps" outside recipes and define blocks. Variable assignments (":=",
// "="), pattern rules, special and hidden targets (".PHONY"), target-specific
// variables and unsafe names are skipped.
func makeTargets(data []byte) []string {
	if len(data) > maxFileBytes {
		data = data[:bytes.LastIndexByte(data[:maxFileBytes], '\n')+1] // Whole lines only.
	}
	var targets []string
	inDefine, continued := false, false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64<<10), maxFileBytes)
	for scanner.Scan() {
		line := scanner.Text()
		wasContinued := continued
		continued = strings.HasSuffix(line, "\\")
		first, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch {
		case first == "endef":
			inDefine = false
			continue
		case inDefine || wasContinued || line == "" || line[0] == '\t' || line[0] == ' ' || line[0] == '#':
			continue
		case first == "define" || strings.HasPrefix(line, "define\t"):
			inDefine = true
			continue
		}
		head, rest, ok := strings.Cut(line, ":")
		rest = strings.TrimPrefix(rest, ":") // "name::" double-colon rule.
		if !ok || strings.ContainsAny(head, "=%$") || strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, ":=") {
			continue
		}
		prerequisites, _, _ := strings.Cut(rest, ";")
		prerequisites, _, _ = strings.Cut(prerequisites, "#")
		if strings.Contains(prerequisites, "=") {
			continue // Target-specific variable, "name: VAR = value".
		}
		names := strings.Fields(head)
		if len(names) == 0 || slices.ContainsFunc(names, func(name string) bool { return !protocol.ValidTaskName(name) }) {
			continue
		}
		for _, name := range names {
			if name[0] != '.' && !slices.Contains(targets, name) {
				targets = append(targets, name)
			}
		}
	}
	return targets
}

// kindOf sorts a task name into a kind. The head is the part before the
// first ":" or "-", so "dev:web" and "start-api" are dev.
func kindOf(name string) protocol.TaskKind {
	name = strings.ToLower(name)
	head := name
	if index := strings.IndexAny(name, ":-"); index >= 0 {
		head = name[:index]
	}
	switch {
	case head == "test" || strings.Contains(name, "spec") || strings.Contains(name, "e2e"):
		return protocol.Test
	case slices.Contains([]string{"dev", "start", "serve", "watch", "preview"}, head):
		return protocol.Dev
	case strings.HasPrefix(name, "build") || head == "compile" || head == "bundle":
		return protocol.Build
	case strings.HasPrefix(name, "lint") || strings.HasPrefix(name, "format") ||
		slices.Contains([]string{"fmt", "typecheck", "check"}, head):
		return protocol.Lint
	default:
		return protocol.TaskKindOther
	}
}
