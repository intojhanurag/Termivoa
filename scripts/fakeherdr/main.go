//go:build !windows

// Command fakeherdr is a fake herdr for Go tests, the e2e harness and manual
// runs (docs/herdr-provider.md, section 15). It reads only its own folder:
// $HERDR_SOCKET_PATH's folder, else $HOME/.config/herdr (or
// sessions/<name> with --session). It never touches a real herdr.
//
//	fakeherdr server                  serve herdr.sock from scenario.json, plus control.sock
//	fakeherdr status server --json    herdr's status shape
//	fakeherdr terminal attach --help  exit 0 only when the scenario sets attachAny
//	fakeherdr agent start --help      the scenario's agent kinds
//	fakeherdr agent attach <pane>     a TTY client: marker, size, echo, ctrl+b q
//	fakeherdr terminal attach <term>  the same, by terminal ID (attachAny only)
//
// The scenario is $FAKE_HERDR_SCENARIO or <folder>/scenario.json:
//
//	{"version":"0.8.2","protocol":20,"attachAny":false,"kinds":["claude"],
//	 "agentStartDelayMs":0,"snapshot":{...herdr's session.snapshot...}}
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
	Kinds             []string       `json:"kinds"`
	AgentStartDelayMs int            `json:"agentStartDelayMs"`
	Snapshot          map[string]any `json:"snapshot"`
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
		fmt.Println("Attach directly to a terminal stream")
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
	data, err := os.ReadFile(path)
	if err != nil {
		fail(folder, "scenario: %v", err)
	}
	var loaded scenario
	if err := json.Unmarshal(data, &loaded); err != nil {
		fail(folder, "scenario: %v", err)
	}
	if loaded.Version == "" {
		loaded.Version, loaded.Protocol = "0.8.2", 20
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
	server, err := herdrtest.Listen(filepath.Join(folder, "herdr.sock"))
	if err != nil {
		return err
	}
	live := &world{folder: folder, scenario: loaded, server: server}
	server.Version(loaded.Version, loaded.Protocol)
	live.publish()
	server.Handle("worktree.create", live.worktreeCreate)
	server.Handle("agent.start", live.agentStart)
	server.Handle("agent.prompt", live.agentPrompt)
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

// worktreeCreate adds a linked worktree workspace to the repository of
// workspace_id, with one tab and one shell pane, like herdr.
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
	source := live.find("workspaces", "workspace_id", request.WorkspaceID)
	tree, _ := source["worktree"].(map[string]any)
	if source == nil || tree == nil {
		return nil, &herdrtest.Error{Code: "not_git_worktree", Message: "Herdr worktree actions require a workspace inside a Git work tree"}
	}
	path := filepath.Join(live.folder, "worktrees", fmt.Sprint(tree["repo_name"]), request.Branch)
	if live.find("workspaces", "label", request.Branch) != nil {
		return nil, &herdrtest.Error{Code: "worktree_create_failed", Message: "fatal: '" + path + "' already exists"}
	}
	number := len(live.list("workspaces")) + 1
	for live.find("workspaces", "workspace_id", fmt.Sprintf("w%d", number)) != nil {
		number++
	}
	workspace, tab, pane := fmt.Sprintf("w%d", number), fmt.Sprintf("w%d:t1", number), fmt.Sprintf("w%d:p1", number)
	live.terminal++
	terminal := fmt.Sprintf("term_fake%06d", live.terminal)
	entry := map[string]any{"workspace_id": workspace, "label": request.Branch, "number": number, "active_tab_id": tab,
		"agent_status": "unknown", "worktree": map[string]any{"repo_key": tree["repo_key"], "repo_name": tree["repo_name"],
			"repo_root": tree["repo_root"], "checkout_path": path, "is_linked_worktree": true}}
	paneEntry := map[string]any{"pane_id": pane, "tab_id": tab, "workspace_id": workspace, "terminal_id": terminal, "agent_status": "unknown"}
	live.scenario.Snapshot["workspaces"] = append(live.list("workspaces"), entry)
	live.scenario.Snapshot["tabs"] = append(live.list("tabs"), map[string]any{"tab_id": tab, "workspace_id": workspace, "label": "1", "number": 1})
	live.scenario.Snapshot["panes"] = append(live.list("panes"), paneEntry)
	live.scenario.Snapshot["layouts"] = append(live.list("layouts"), map[string]any{"tab_id": tab, "workspace_id": workspace, "focused_pane_id": pane})
	live.publish()
	logf(live.folder, "worktree.create %s %s", request.WorkspaceID, request.Branch)
	return map[string]any{"type": "worktree_created", "workspace": entry, "root_pane": paneEntry}, nil
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
	pane := live.find("panes", "pane_id", request.PaneID)
	if pane == nil || !slices.Contains(live.scenario.Kinds, request.Kind) {
		return nil, &herdrtest.Error{Code: "agent_start_failed", Message: "cannot start " + request.Kind}
	}
	pane["agent_status"] = "idle"
	live.scenario.Snapshot["agents"] = append(live.list("agents"), map[string]any{
		"pane_id": request.PaneID, "tab_id": pane["tab_id"], "workspace_id": pane["workspace_id"],
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
		fmt.Printf("\x1b[2J\x1b[HFAKE-HERDR %s %dx%d\r\n", pane, columns, rows)
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
