//go:build !windows

// Command fakeherdr is a fake herdr for Go tests, the e2e harness and manual
// runs (docs/herdr-provider.md, section 15). It reads only its own folder:
// $HERDR_SOCKET_PATH's folder, else $HOME/.config/herdr (or
// sessions/<name> with --session). It never touches a real herdr.
//
//	fakeherdr server                  serve herdr.sock from scenario.json, plus control.sock;
//	                                  with no scenario.json it starts empty, like a new herdr
//	fakeherdr status server --json    herdr's status shape
//	fakeherdr terminal attach --help  its usage line only when the scenario sets attachAny
//	fakeherdr agent start --help      the scenario's agent kinds
//	fakeherdr agent attach <pane>     a TTY client: marker, size, echo, ctrl+b q
//	fakeherdr terminal attach <term>  the same, by terminal ID (attachAny only)
//
// With "noTerminal" in the scenario it is a herdr without the terminal
// command: any `terminal …` runs herdr's default command, which starts the
// UI (logged "ui started") when it has a terminal, else prints the general
// help with exit 0, as herdr 0.8.2 answers `--help` after an unknown command.
//
// The scenario is $FAKE_HERDR_SCENARIO or <folder>/scenario.json:
//
//	{"version":"0.8.2","protocol":20,"attachAny":false,"noTerminal":false,"kinds":["claude"],
//	 "agentStartDelayMs":0,"snapshot":{...herdr's session.snapshot...},
//	 "screens":{"w1:p1":"text an attach of w1:p1 draws in place of its marker"},
//	 "dirty":["w3"],"groupCloseRequired":false,
//	 "closed":[{"repo_key":"/home/demo/shop/.git","branch":"old-idea"}]}
//
// The socket also answers what docs/herdr-manage.md uses: workspace.close,
// worktree.remove (never with force for a workspace in "dirty"), tab.create
// and workspace.create. "groupCloseRequired" makes workspace.close answer
// like herdr 0.9.3 for a main checkout whose linked worktrees are open; by
// default it closes them too, like 0.8.2. worktree.create on a workspace
// without "worktree" works when its first pane's "cwd" is in a git
// repository, and gives that workspace a worktree record, like herdr.
//
// worktree.list and worktree.open are docs/herdr-manage.md, section 14: the
// list has every open workspace of the repository and its closed worktrees.
// A linked worktree whose workspace is closed (workspace.close, or tab.close
// on its last tab) stays in the list as closed, and so does each entry of the
// scenario's "closed" ("path" is optional); worktree.open opens one again, and
// worktree.remove takes the worktree out for good. agent.start refuses a name
// that another agent has (agent_name_taken), like herdr.
//
// control.sock takes one JSON command per connection: {"op":"status",
// "pane":"w1:p1","status":"blocked"}, {"op":"close","pane":"w1:p1"},
// {"op":"hang","on":true}, {"op":"stop"}, {"op":"start"}. The server
// writes the live snapshot to <folder>/state.json and every event to
// $FAKE_HERDR_LOG or <folder>/fake.log.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/intojhanurag/phone-terminal/internal/herdr/herdrtest"
)

type scenario struct {
	Version           string         `json:"version"`
	Protocol          int            `json:"protocol"`
	AttachAny         bool           `json:"attachAny"`
	NoTerminal        bool           `json:"noTerminal"`
	Kinds             []string       `json:"kinds"`
	AgentStartDelayMs int            `json:"agentStartDelayMs"`
	Snapshot          map[string]any `json:"snapshot"`
	// Screens, by pane ID, replace the marker an attach draws (docs
	// screenshots: a demo agent screen). Lines end in \n.
	Screens map[string]string `json:"screens"`
	// Dirty lists workspace IDs whose worktree has uncommitted changes.
	Dirty []string `json:"dirty"`
	// GroupCloseRequired is herdr 0.9.3's workspace.close rule.
	GroupCloseRequired bool `json:"groupCloseRequired"`
	// Closed lists linked worktrees that have no workspace.
	Closed []closedTree `json:"closed"`
}

// closedTree is a linked worktree without a workspace: worktree.list shows
// it and worktree.open opens it again.
type closedTree struct {
	RepoKey string `json:"repo_key"`
	Branch  string `json:"branch"`
	Path    string `json:"path"`
}

var agentName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func main() {
	args := os.Args[1:]
	session := ""
	if len(args) >= 2 && args[0] == "--session" {
		session, args = args[1], args[2:]
	}
	folder := herdrFolder(session)
	command := strings.Join(args, " ")
	if len(args) > 0 && args[0] == "terminal" && load(folder).NoTerminal {
		defaultCommand(folder)
		return
	}
	switch {
	case command == "server":
		if err := serve(folder); err != nil {
			fail(folder, "server: %v", err)
		}
	case command == "status server --json":
		status(folder, session)
	case command == "terminal attach --help":
		if !load(folder).AttachAny {
			os.Exit(2)
		}
		fmt.Print("Attach directly to a terminal stream\n\nUsage: herdr terminal attach [OPTIONS] <TERMINAL_ID>\n")
	case command == "agent start --help":
		fmt.Printf("      --kind <KIND>\n          [possible values: %s]\n", strings.Join(load(folder).Kinds, ", "))
	case len(args) == 3 && args[0] == "agent" && args[1] == "attach":
		attach(folder, args[2], "", true)
	case len(args) == 3 && args[0] == "terminal" && args[1] == "attach":
		if !load(folder).AttachAny {
			os.Exit(2)
		}
		attach(folder, "", args[2], false)
	default:
		fmt.Fprintf(os.Stderr, "fakeherdr: unknown command %q\n", command)
		os.Exit(2)
	}
}

// defaultCommand is herdr's `herdr [options]`: with a terminal it starts
// the UI on the session; without one it prints the general help.
func defaultCommand(folder string) {
	if tty, err := os.Open("/dev/tty"); err == nil || term.IsTerminal(os.Stdin.Fd()) {
		if tty != nil {
			_ = tty.Close()
		}
		logf(folder, "ui started")
		return
	}
	fmt.Print("herdr — terminal workspace manager for AI coding agents\n\nUsage: herdr [options]\n")
}

func herdrFolder(session string) string {
	if socket := os.Getenv("HERDR_SOCKET_PATH"); socket != "" {
		return filepath.Dir(socket)
	}
	folder := filepath.Join(os.Getenv("HOME"), ".config", "herdr")
	if session != "" {
		folder = filepath.Join(folder, "sessions", session)
	}
	return folder
}

func load(folder string) scenario {
	path := os.Getenv("FAKE_HERDR_SCENARIO")
	if path == "" {
		path = filepath.Join(folder, "scenario.json")
	}
	var loaded scenario
	data, err := os.ReadFile(path)
	// A herdr that was never started has no scenario: an empty server (the start check).
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fail(folder, "scenario: %v", err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &loaded); err != nil {
			fail(folder, "scenario: %v", err)
		}
	}
	if loaded.Version == "" {
		loaded.Version, loaded.Protocol = "0.8.2", 20
	}
	if loaded.Snapshot == nil {
		loaded.Snapshot = map[string]any{"version": loaded.Version, "protocol": loaded.Protocol,
			"workspaces": []any{}, "tabs": []any{}, "panes": []any{}, "layouts": []any{}, "agents": []any{}}
	}
	return loaded
}

func logf(folder, format string, values ...any) {
	path := os.Getenv("FAKE_HERDR_LOG")
	if path == "" {
		path = filepath.Join(folder, "fake.log")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, format+"\n", values...)
}

func fail(folder, format string, values ...any) {
	logf(folder, "error "+format, values...)
	fmt.Fprintf(os.Stderr, "fakeherdr: "+format+"\n", values...)
	os.Exit(1)
}

func status(folder, session string) {
	socket := filepath.Join(folder, "herdr.sock")
	_, err := os.Stat(filepath.Join(folder, "state.json"))
	running := err == nil
	result := map[string]any{"status": "not_running", "running": false, "version": nil, "protocol": nil,
		"compatible": nil, "socket": socket, "session": nil, "restart_needed": false}
	if running {
		loaded := load(folder)
		result["status"], result["running"], result["version"], result["protocol"], result["compatible"] =
			"running", true, loaded.Version, loaded.Protocol, true
	}
	if session != "" {
		result["session"] = session
	}
	data, _ := json.Marshal(result)
	fmt.Println(string(data))
}

// world is the server's mutable snapshot.
type world struct {
	mu       sync.Mutex
	folder   string
	scenario scenario
	server   *herdrtest.Server
	terminal int
}

func serve(folder string) error {
	loaded := load(folder)
	// herdr refuses a second server for a running session.
	if conn, err := net.Dial("unix", filepath.Join(folder, "herdr.sock")); err == nil {
		_ = conn.Close()
		return errors.New("herdr server is already running")
	}
	logf(folder, "server start pid=%d PATH=%s TERM=%s", os.Getpid(), os.Getenv("PATH"), os.Getenv("TERM"))
	server, err := herdrtest.Listen(filepath.Join(folder, "herdr.sock"))
	if err != nil {
		return err
	}
	live := &world{folder: folder, scenario: loaded, server: server}
	server.Version(loaded.Version, loaded.Protocol)
	live.publish()
	server.Handle("worktree.create", live.worktreeCreate)
	server.Handle("worktree.list", live.worktreeList)
	server.Handle("worktree.open", live.worktreeOpen)
	server.Handle("agent.start", live.agentStart)
	server.Handle("agent.prompt", live.agentPrompt)
	server.Handle("workspace.close", live.workspaceClose)
	server.Handle("worktree.remove", live.worktreeRemove)
	server.Handle("tab.create", live.tabCreate)
	server.Handle("tab.close", live.tabClose)
	server.Handle("workspace.create", live.workspaceCreate)
	control := filepath.Join(folder, "control.sock")
	_ = os.Remove(control)
	listener, err := net.Listen("unix", control)
	if err != nil {
		return err
	}
	if err := os.Chmod(control, 0o600); err != nil {
		return err
	}
	logf(folder, "server %s protocol %d", loaded.Version, loaded.Protocol)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go live.control(conn)
		}
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	server.Stop()
	_ = listener.Close()
	_ = os.Remove(control)
	_ = os.Remove(filepath.Join(folder, "state.json"))
	return nil
}

// publish serves the current snapshot and writes it to state.json, which
// attach clients read to notice a closed pane. The caller holds mu or owns live.
func (live *world) publish() {
	data, _ := json.Marshal(live.scenario.Snapshot)
	live.server.Snapshot(data)
	_ = os.WriteFile(filepath.Join(live.folder, "state.json.tmp"), data, 0o600)
	_ = os.Rename(filepath.Join(live.folder, "state.json.tmp"), filepath.Join(live.folder, "state.json"))
}

func (live *world) list(name string) []any {
	list, _ := live.scenario.Snapshot[name].([]any)
	return list
}

func (live *world) find(name, key, value string) map[string]any {
	for _, record := range live.list(name) {
		if entry, ok := record.(map[string]any); ok && entry[key] == value {
			return entry
		}
	}
	return nil
}

func (live *world) remove(name, key, value string) {
	live.scenario.Snapshot[name] = slices.DeleteFunc(live.list(name), func(record any) bool {
		entry, ok := record.(map[string]any)
		return ok && entry[key] == value
	})
}

func (live *world) control(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var command struct {
		Op, Pane, Status string
		On               bool
	}
	if err := json.Unmarshal(line, &command); err != nil {
		_, _ = conn.Write([]byte("error\n"))
		return
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	logf(live.folder, "control %s %s %s", command.Op, command.Pane, command.Status)
	switch command.Op {
	case "status":
		pane := live.find("panes", "pane_id", command.Pane)
		if pane == nil {
			_, _ = conn.Write([]byte("error no pane\n"))
			return
		}
		pane["agent_status"] = command.Status
		if agent := live.find("agents", "pane_id", command.Pane); agent != nil {
			agent["agent_status"] = command.Status
			seq, _ := agent["state_change_seq"].(float64)
			agent["state_change_seq"] = seq + 1
		}
	case "close":
		pane := live.find("panes", "pane_id", command.Pane)
		live.remove("panes", "pane_id", command.Pane)
		live.remove("agents", "pane_id", command.Pane)
		if pane != nil {
			tab, _ := pane["tab_id"].(string)
			if live.find("panes", "tab_id", tab) == nil {
				live.remove("tabs", "tab_id", tab)
				live.remove("layouts", "tab_id", tab)
			}
		}
	case "hang":
		live.server.Hang(command.On)
	case "stop":
		live.server.Stop()
		_ = os.Remove(filepath.Join(live.folder, "state.json"))
		_, _ = conn.Write([]byte("ok\n"))
		return
	case "start":
		if err := live.server.Start(); err != nil {
			_, _ = conn.Write([]byte("error\n"))
			return
		}
	default:
		_, _ = conn.Write([]byte("error unknown op\n"))
		return
	}
	live.publish()
	_, _ = conn.Write([]byte("ok\n"))
}

func decode[T any](params json.RawMessage) (T, *herdrtest.Error) {
	var value T
	if err := json.Unmarshal(params, &value); err != nil {
		return value, &herdrtest.Error{Code: "invalid_request", Message: "invalid request: " + err.Error()}
	}
	return value, nil
}

// repository walks up from folder to a .git entry and returns its folder.
func repository(folder string) string {
	for filepath.IsAbs(folder) {
		if _, err := os.Lstat(filepath.Join(folder, ".git")); err == nil {
			return folder
		}
		if filepath.Dir(folder) == folder {
			break
		}
		folder = filepath.Dir(folder)
	}
	return ""
}

// tree returns a workspace and its worktree record. A workspace without a
// record gets a new one when its first pane's folder is in git; the caller
// stores it when herdr would. A nil tree: no such workspace, or not in git.
func (live *world) tree(id string) (source, tree map[string]any) {
	source = live.find("workspaces", "workspace_id", id)
	tree, _ = source["worktree"].(map[string]any)
	if source != nil && tree == nil {
		pane := live.find("panes", "workspace_id", id)
		cwd, _ := pane["cwd"].(string)
		if root := repository(cwd); root != "" {
			tree = map[string]any{"repo_key": filepath.Join(root, ".git"), "repo_name": filepath.Base(root),
				"repo_root": root, "checkout_path": root, "is_linked_worktree": false}
		}
	}
	return source, tree
}

var errNotGit = &herdrtest.Error{Code: "not_git_worktree", Message: "Herdr worktree actions require a workspace inside a Git work tree"}

// closedIndex finds a closed worktree of a repository by its branch; -1 when there is none.
func (live *world) closedIndex(tree map[string]any, branch string) int {
	return slices.IndexFunc(live.scenario.Closed, func(closed closedTree) bool {
		return closed.RepoKey == tree["repo_key"] && closed.Branch == branch
	})
}

// worktreeCreate adds a linked worktree workspace to the repository of
// workspace_id, with one tab and one shell pane, like herdr. A workspace
// without a worktree record gets one when its first pane's folder is in git.
func (live *world) worktreeCreate(params json.RawMessage) (any, *herdrtest.Error) {
	request, failure := decode[struct {
		WorkspaceID string `json:"workspace_id"`
		Branch      string `json:"branch"`
	}](params)
	if failure != nil {
		return nil, failure
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	source, tree := live.tree(request.WorkspaceID)
	if tree == nil {
		return nil, errNotGit
	}
	source["worktree"] = tree
	path := filepath.Join(live.folder, "worktrees", fmt.Sprint(tree["repo_name"]), request.Branch)
	if live.find("workspaces", "label", request.Branch) != nil || live.closedIndex(tree, request.Branch) >= 0 {
		return nil, &herdrtest.Error{Code: "worktree_create_failed", Message: "fatal: '" + path + "' already exists"}
	}
	entry, pane := live.addWorkspace(request.Branch, path)
	entry["worktree"] = linkedTree(tree, path)
	live.publish()
	logf(live.folder, "worktree.create %s %s", request.WorkspaceID, request.Branch)
	return map[string]any{"type": "worktree_created", "workspace": entry, "root_pane": pane}, nil
}

// linkedTree is the worktree record of a linked worktree at path in tree's repository.
func linkedTree(tree map[string]any, path string) map[string]any {
	return map[string]any{"repo_key": tree["repo_key"], "repo_name": tree["repo_name"],
		"repo_root": tree["repo_root"], "checkout_path": path, "is_linked_worktree": true}
}

// worktreeList answers the worktrees of workspace_id's repository: each open
// workspace with its ID, and each closed worktree without one. herdr names a
// linked worktree's workspace after its branch.
func (live *world) worktreeList(params json.RawMessage) (any, *herdrtest.Error) {
	request, failure := decode[workspaceRequest](params)
	if failure != nil {
		return nil, failure
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	source, tree := live.tree(request.WorkspaceID)
	if source == nil {
		return nil, &herdrtest.Error{Code: "workspace_not_found", Message: "workspace " + request.WorkspaceID + " not found"}
	}
	if tree == nil {
		return nil, errNotGit
	}
	info := func(path, branch any, linked bool) map[string]any {
		return map[string]any{"path": path, "branch": branch, "is_bare": false, "is_detached": false, "is_prunable": false,
			"is_linked_worktree": linked, "label": tree["repo_name"]}
	}
	trees := []any{}
	for _, record := range live.list("workspaces") {
		entry, _ := record.(map[string]any)
		if other, _ := entry["worktree"].(map[string]any); other != nil && other["repo_key"] == tree["repo_key"] {
			linked, branch := other["is_linked_worktree"] == true, entry["label"]
			if !linked {
				branch = "main"
			}
			open := info(other["checkout_path"], branch, linked)
			open["open_workspace_id"] = entry["workspace_id"]
			trees = append(trees, open)
		}
	}
	for _, closed := range live.scenario.Closed {
		if closed.RepoKey == tree["repo_key"] {
			trees = append(trees, info(closed.Path, closed.Branch, true))
		}
	}
	return map[string]any{"type": "worktree_list", "worktrees": trees}, nil
}

// worktreeOpen opens a workspace for a closed worktree of workspace_id's
// repository again, or answers the workspace that is open for that branch
// (already_open), like herdr.
func (live *world) worktreeOpen(params json.RawMessage) (any, *herdrtest.Error) {
	request, failure := decode[struct {
		WorkspaceID string `json:"workspace_id"`
		Branch      string `json:"branch"`
	}](params)
	if failure != nil {
		return nil, failure
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	source, tree := live.tree(request.WorkspaceID)
	if tree == nil {
		return nil, errNotGit
	}
	for _, record := range live.list("workspaces") {
		entry, _ := record.(map[string]any)
		other, _ := entry["worktree"].(map[string]any)
		if other != nil && other["repo_key"] == tree["repo_key"] && other["is_linked_worktree"] == true && entry["label"] == request.Branch {
			pane := live.find("panes", "workspace_id", fmt.Sprint(entry["workspace_id"]))
			return map[string]any{"type": "worktree_opened", "workspace": entry, "root_pane": pane, "already_open": true}, nil
		}
	}
	index := live.closedIndex(tree, request.Branch)
	if index < 0 {
		return nil, &herdrtest.Error{Code: "worktree_not_found", Message: "worktree branch not found"}
	}
	path := live.scenario.Closed[index].Path
	if path == "" {
		path = filepath.Join(live.folder, "worktrees", fmt.Sprint(tree["repo_name"]), request.Branch)
	}
	live.scenario.Closed = slices.Delete(live.scenario.Closed, index, index+1)
	source["worktree"] = tree
	entry, pane := live.addWorkspace(request.Branch, path)
	entry["worktree"] = linkedTree(tree, path)
	live.publish()
	logf(live.folder, "worktree.open %s %s", request.WorkspaceID, request.Branch)
	return map[string]any{"type": "worktree_opened", "workspace": entry, "root_pane": pane, "already_open": false}, nil
}

// addWorkspace adds a workspace with one tab and one shell pane in cwd. The
// caller holds mu and publishes.
func (live *world) addWorkspace(label, cwd string) (workspace, pane map[string]any) {
	number := len(live.list("workspaces")) + 1
	for live.find("workspaces", "workspace_id", fmt.Sprintf("w%d", number)) != nil {
		number++
	}
	id := fmt.Sprintf("w%d", number)
	workspace = map[string]any{"workspace_id": id, "label": label, "number": number, "active_tab_id": id + ":t1",
		"agent_status": "unknown", "worktree": nil}
	live.scenario.Snapshot["workspaces"] = append(live.list("workspaces"), workspace)
	return workspace, live.addTab(id, cwd)
}

// addTab adds a tab with one shell pane to a workspace. The caller holds mu
// and publishes.
func (live *world) addTab(workspace, cwd string) map[string]any {
	number := 1
	for live.find("tabs", "tab_id", fmt.Sprintf("%s:t%d", workspace, number)) != nil ||
		live.find("panes", "pane_id", fmt.Sprintf("%s:p%d", workspace, number)) != nil {
		number++
	}
	tab, pane := fmt.Sprintf("%s:t%d", workspace, number), fmt.Sprintf("%s:p%d", workspace, number)
	live.terminal++
	entry := map[string]any{"pane_id": pane, "tab_id": tab, "workspace_id": workspace,
		"terminal_id": fmt.Sprintf("term_fake%06d", live.terminal), "agent_status": "unknown", "cwd": cwd}
	live.scenario.Snapshot["tabs"] = append(live.list("tabs"), map[string]any{"tab_id": tab, "workspace_id": workspace, "label": fmt.Sprint(number), "number": number})
	live.scenario.Snapshot["panes"] = append(live.list("panes"), entry)
	live.scenario.Snapshot["layouts"] = append(live.list("layouts"), map[string]any{"tab_id": tab, "workspace_id": workspace, "focused_pane_id": pane})
	return entry
}

type workspaceRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Force       bool   `json:"force"`
	Cwd         string `json:"cwd"`
	Label       string `json:"label"`
}

// closeWorkspace removes a workspace and everything in it. With keep, a
// linked worktree stays as a closed one (its folder is still there). The
// caller holds mu.
func (live *world) closeWorkspace(id string, keep bool) {
	workspace := live.find("workspaces", "workspace_id", id)
	if tree, _ := workspace["worktree"].(map[string]any); keep && tree != nil && tree["is_linked_worktree"] == true {
		live.scenario.Closed = append(live.scenario.Closed, closedTree{
			RepoKey: fmt.Sprint(tree["repo_key"]), Branch: fmt.Sprint(workspace["label"]), Path: fmt.Sprint(tree["checkout_path"]),
		})
	}
	for _, name := range []string{"workspaces", "tabs", "panes", "layouts", "agents"} {
		live.remove(name, "workspace_id", id)
	}
}

// linkedOf lists the open linked-worktree workspaces of a main checkout.
func (live *world) linkedOf(workspace map[string]any) []string {
	tree, _ := workspace["worktree"].(map[string]any)
	var linked []string
	for _, record := range live.list("workspaces") {
		entry, _ := record.(map[string]any)
		other, _ := entry["worktree"].(map[string]any)
		if tree != nil && tree["is_linked_worktree"] != true && other != nil && other["is_linked_worktree"] == true && other["repo_key"] == tree["repo_key"] {
			linked = append(linked, fmt.Sprint(entry["workspace_id"]))
		}
	}
	return linked
}

// workspaceClose ends a workspace. A main checkout takes its linked
// worktrees' workspaces along (herdr 0.8.2), or is refused (0.9.3).
func (live *world) workspaceClose(params json.RawMessage) (any, *herdrtest.Error) {
	request, failure := decode[workspaceRequest](params)
	if failure != nil {
		return nil, failure
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	workspace := live.find("workspaces", "workspace_id", request.WorkspaceID)
	if workspace == nil {
		return nil, &herdrtest.Error{Code: "workspace_not_found", Message: "workspace " + request.WorkspaceID + " not found"}
	}
	linked := live.linkedOf(workspace)
	if len(linked) > 0 && live.scenario.GroupCloseRequired {
		return nil, &herdrtest.Error{Code: "workspace_group_close_required", Message: "close the linked worktree workspaces first"}
	}
	for _, id := range append(linked, request.WorkspaceID) {
		live.closeWorkspace(id, true)
	}
	live.publish()
	logf(live.folder, "workspace.close %s", request.WorkspaceID)
	return map[string]any{"type": "ok"}, nil
}

// worktreeRemove closes a linked worktree's workspace (the real one also
// deletes the folder). A workspace in the scenario's "dirty" needs force.
func (live *world) worktreeRemove(params json.RawMessage) (any, *herdrtest.Error) {
	request, failure := decode[workspaceRequest](params)
	if failure != nil {
		return nil, failure
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	workspace := live.find("workspaces", "workspace_id", request.WorkspaceID)
	if workspace == nil {
		return nil, &herdrtest.Error{Code: "workspace_not_found", Message: "workspace " + request.WorkspaceID + " not found"}
	}
	tree, _ := workspace["worktree"].(map[string]any)
	if tree == nil || tree["is_linked_worktree"] != true {
		return nil, &herdrtest.Error{Code: "not_linked_worktree", Message: "workspace is not a linked worktree"}
	}
	if slices.Contains(live.scenario.Dirty, request.WorkspaceID) && !request.Force {
		return nil, &herdrtest.Error{Code: "dirty_worktree_requires_force",
			Message: fmt.Sprintf("fatal: '%v' contains modified or untracked files, use --force to delete it", tree["checkout_path"])}
	}
	live.closeWorkspace(request.WorkspaceID, false)
	live.publish()
	logf(live.folder, "worktree.remove %s force=%t", request.WorkspaceID, request.Force)
	return map[string]any{"type": "worktree_removed"}, nil
}

func (live *world) tabCreate(params json.RawMessage) (any, *herdrtest.Error) {
	request, failure := decode[workspaceRequest](params)
	if failure != nil {
		return nil, failure
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	workspace := live.find("workspaces", "workspace_id", request.WorkspaceID)
	if workspace == nil {
		return nil, &herdrtest.Error{Code: "workspace_not_found", Message: "workspace " + request.WorkspaceID + " not found"}
	}
	cwd, _ := live.find("panes", "workspace_id", request.WorkspaceID)["cwd"].(string)
	pane := live.addTab(request.WorkspaceID, cwd)
	live.publish()
	logf(live.folder, "tab.create %s", request.WorkspaceID)
	return map[string]any{"type": "tab_created", "root_pane": pane}, nil
}

// tabClose ends a tab and its panes; with the last tab the workspace goes too
// (real herdr 0.8.2 and 0.9.3).
func (live *world) tabClose(params json.RawMessage) (any, *herdrtest.Error) {
	request, failure := decode[struct {
		TabID string `json:"tab_id"`
	}](params)
	if failure != nil {
		return nil, failure
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	tab := live.find("tabs", "tab_id", request.TabID)
	if tab == nil {
		return nil, &herdrtest.Error{Code: "tab_not_found", Message: "tab " + request.TabID + " not found"}
	}
	workspace := fmt.Sprint(tab["workspace_id"])
	for _, name := range []string{"tabs", "panes", "layouts", "agents"} {
		live.remove(name, "tab_id", request.TabID)
	}
	if live.find("tabs", "workspace_id", workspace) == nil {
		live.closeWorkspace(workspace, true)
	}
	live.publish()
	logf(live.folder, "tab.close %s", request.TabID)
	return map[string]any{"type": "ok"}, nil
}

func (live *world) workspaceCreate(params json.RawMessage) (any, *herdrtest.Error) {
	request, failure := decode[workspaceRequest](params)
	if failure != nil {
		return nil, failure
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	workspace, pane := live.addWorkspace(request.Label, request.Cwd)
	live.publish()
	logf(live.folder, "workspace.create %s %s", request.Label, request.Cwd)
	return map[string]any{"type": "workspace_created", "workspace": workspace, "root_pane": pane}, nil
}

func (live *world) agentStart(params json.RawMessage) (any, *herdrtest.Error) {
	request, failure := decode[struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		PaneID string `json:"pane_id"`
	}](params)
	if failure != nil {
		return nil, failure
	}
	if !agentName.MatchString(request.Name) {
		return nil, &herdrtest.Error{Code: "invalid_agent_name", Message: "agent name must start with a lowercase letter"}
	}
	time.Sleep(time.Duration(live.scenario.AgentStartDelayMs) * time.Millisecond)
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.find("agents", "name", request.Name) != nil {
		return nil, &herdrtest.Error{Code: "agent_name_taken", Message: "agent name " + request.Name + " is already used"}
	}
	pane := live.find("panes", "pane_id", request.PaneID)
	if pane == nil || !slices.Contains(live.scenario.Kinds, request.Kind) {
		return nil, &herdrtest.Error{Code: "agent_start_failed", Message: "cannot start " + request.Kind}
	}
	pane["agent_status"] = "idle"
	live.scenario.Snapshot["agents"] = append(live.list("agents"), map[string]any{
		"name": request.Name, "pane_id": request.PaneID, "tab_id": pane["tab_id"], "workspace_id": pane["workspace_id"],
		"terminal_id": pane["terminal_id"], "agent": request.Kind, "agent_status": "idle", "state_change_seq": 1,
	})
	live.publish()
	logf(live.folder, "agent.start %s %s %s", request.Kind, request.Name, request.PaneID)
	return map[string]any{"type": "agent_started", "pane_id": request.PaneID}, nil
}

func (live *world) agentPrompt(params json.RawMessage) (any, *herdrtest.Error) {
	request, failure := decode[struct {
		Target string `json:"target"`
		Text   string `json:"text"`
	}](params)
	if failure != nil {
		return nil, failure
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.find("agents", "pane_id", request.Target) == nil {
		return nil, &herdrtest.Error{Code: "agent_not_found", Message: "no agent in " + request.Target}
	}
	logf(live.folder, "agent.prompt %s %q", request.Target, request.Text)
	return map[string]any{"type": "agent_prompted"}, nil
}

// attach is the TTY client: it draws a marker and its size, echoes input,
// logs sizes, input and exit, and ends on ctrl+b q (exit 0), when its pane
// closes, on SIGHUP or SIGTERM (exit 1) or when another client holds the
// terminal (exit 1).
// ctrl+b ctrl+b passes one literal ctrl+b; ctrl+b and another key drops the
// ctrl+b, as herdr's attach client does.
func attach(folder, pane, terminalID string, byAgent bool) {
	state := readState(folder)
	for _, record := range list(state, "panes") {
		if pane != "" && record["pane_id"] == pane || terminalID != "" && record["terminal_id"] == terminalID {
			pane, terminalID = fmt.Sprint(record["pane_id"]), fmt.Sprint(record["terminal_id"])
		}
	}
	hasAgent := slices.ContainsFunc(list(state, "agents"), func(record map[string]any) bool { return record["pane_id"] == pane })
	if terminalID == "" || pane == "" || byAgent && !hasAgent {
		fail(folder, "agent_not_found: %s%s", pane, terminalID)
	}
	lock := filepath.Join(folder, "attach-"+terminalID+".lock")
	file, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Println("terminal " + terminalID + " already has an attached client; retry with --takeover")
		logf(folder, "attach refused %s", pane)
		os.Exit(1)
	}
	_ = file.Close()
	defer os.Remove(lock)
	saved, err := term.MakeRaw(os.Stdin.Fd())
	if err == nil {
		defer func() { _ = term.Restore(os.Stdin.Fd(), saved) }()
	}
	columns, rows, _ := term.GetSize(os.Stdout.Fd())
	draw := func() {
		columns, rows, _ = term.GetSize(os.Stdout.Fd())
		if screen, ok := load(folder).Screens[pane]; ok {
			fmt.Print("\x1b[2J\x1b[H" + strings.ReplaceAll(screen, "\n", "\r\n"))
		} else {
			fmt.Printf("\x1b[2J\x1b[HFAKE-HERDR %s %dx%d\r\n", pane, columns, rows)
		}
		logf(folder, "attach %s size %dx%d", pane, columns, rows)
	}
	fmt.Print("\x1b[?1049h")
	draw()
	resized, hangup := make(chan os.Signal, 1), make(chan os.Signal, 1)
	signal.Notify(resized, syscall.SIGWINCH)
	signal.Notify(hangup, syscall.SIGHUP, syscall.SIGTERM)
	input := make(chan []byte)
	go func() {
		buffer := make([]byte, 4096)
		for {
			count, err := os.Stdin.Read(buffer)
			if err != nil {
				close(input)
				return
			}
			input <- append([]byte(nil), buffer[:count]...)
		}
	}()
	end := func(code int, why string) {
		fmt.Print("\x1b[?1049l")
		logf(folder, "attach %s exit %d %s", pane, code, why)
		if saved != nil {
			_ = term.Restore(os.Stdin.Fd(), saved)
		}
		_ = os.Remove(lock)
		os.Exit(code)
	}
	prefix := false
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-resized:
			draw()
		case <-hangup:
			end(1, "hangup")
		case <-ticker.C:
			if !slices.ContainsFunc(list(readState(folder), "panes"), func(record map[string]any) bool { return record["pane_id"] == pane }) {
				end(1, "pane closed")
			}
		case data, ok := <-input:
			if !ok {
				end(1, "input closed")
			}
			var passed []byte
			for _, value := range data {
				switch {
				case prefix && value == 'q':
					end(0, "detach")
				case prefix && value == 0x02:
					passed, prefix = append(passed, value), false
				case prefix:
					passed, prefix = append(passed, value), false
				case value == 0x02:
					prefix = true
				default:
					passed = append(passed, value)
				}
			}
			if len(passed) > 0 {
				logf(folder, "input %s %x", pane, passed)
				fmt.Printf("%q\r\n", passed)
			}
		}
	}
}

func readState(folder string) map[string]any {
	var state map[string]any
	data, err := os.ReadFile(filepath.Join(folder, "state.json"))
	if err == nil {
		_ = json.Unmarshal(data, &state)
	}
	return state
}

func list(state map[string]any, name string) []map[string]any {
	raw, _ := state[name].([]any)
	var records []map[string]any
	for _, record := range raw {
		if entry, ok := record.(map[string]any); ok {
			records = append(records, entry)
		}
	}
	return records
}
