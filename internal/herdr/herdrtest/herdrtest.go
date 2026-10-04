// Package herdrtest is a fake herdr for tests and the e2e harness: a socket
// server that answers like herdr (one newline-delimited JSON request per
// connection), and a script that answers the CLI probes. It never touches
// a real herdr. See docs/herdr-provider.md, section 15.
package herdrtest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Error is herdr's error reply.
type Error struct{ Code, Message string }

// Handler answers one method: a result, or an error reply.
type Handler func(params json.RawMessage) (any, *Error)

// Server is a fake herdr socket.
type Server struct {
	Path string

	mu       sync.Mutex
	listener net.Listener
	handlers map[string]Handler
	hang     bool
	calls    []string
}

// Listen serves a fake herdr at path: the folder is made 0700 and the socket
// 0600, as herdr does. It answers ping as herdr 0.8.2 (protocol 20).
func Listen(path string) (*Server, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	server := &Server{Path: path, handlers: map[string]Handler{}}
	server.Handle("ping", func(json.RawMessage) (any, *Error) {
		return map[string]any{"type": "pong", "version": "0.8.2", "protocol": 20}, nil
	})
	return server, server.Start()
}

// Start listens again after Stop (a herdr restart).
func (server *Server) Start() error {
	_ = os.Remove(server.Path)
	listener, err := net.Listen("unix", server.Path)
	if err != nil {
		return err
	}
	if err := os.Chmod(server.Path, 0o600); err != nil {
		_ = listener.Close()
		return err
	}
	server.mu.Lock()
	server.listener = listener
	server.mu.Unlock()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	return nil
}

// Stop closes the socket and removes it (herdr stopped).
func (server *Server) Stop() {
	server.mu.Lock()
	listener := server.listener
	server.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
	_ = os.Remove(server.Path)
}

// Handle sets the handler of method.
func (server *Server) Handle(method string, handler Handler) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.handlers[method] = handler
}

// Version makes ping answer version and protocol.
func (server *Server) Version(version string, protocol int) {
	server.Handle("ping", func(json.RawMessage) (any, *Error) {
		return map[string]any{"type": "pong", "version": version, "protocol": protocol}, nil
	})
}

// Snapshot makes session.snapshot answer data (herdr's result.snapshot).
func (server *Server) Snapshot(data []byte) {
	snapshot := json.RawMessage(append([]byte(nil), data...))
	server.Handle("session.snapshot", func(json.RawMessage) (any, *Error) {
		return map[string]any{"type": "session_snapshot", "snapshot": snapshot}, nil
	})
}

// Hang makes the server accept and never answer.
func (server *Server) Hang(hang bool) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.hang = hang
}

// Calls returns the methods called so far and forgets them.
func (server *Server) Calls() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	calls := server.calls
	server.calls = nil
	return calls
}

func (server *Server) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var request struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(line, &request) != nil {
		return
	}
	server.mu.Lock()
	handler, hang := server.handlers[request.Method], server.hang
	server.calls = append(server.calls, request.Method)
	server.mu.Unlock()
	if hang {
		time.Sleep(3 * time.Second)
		return
	}
	reply := map[string]any{"id": request.ID}
	if handler == nil {
		reply["error"] = map[string]string{"code": "invalid_request", "message": "invalid request: unknown variant"}
	} else if result, failure := handler(request.Params); failure != nil {
		reply["error"] = map[string]string{"code": failure.Code, "message": failure.Message}
	} else {
		reply["result"] = result
	}
	data, _ := json.Marshal(reply)
	_, _ = conn.Write(append(data, '\n'))
}

// Script writes an executable herdr shell script in folder that answers the
// two probes: `terminal attach --help` (exit 0 only with attachAny) and
// `agent start --help` with kinds. It returns its path.
func Script(folder string, attachAny bool, kinds string) (string, error) {
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return "", err
	}
	attach := "exit 2"
	if attachAny {
		attach = "echo 'Attach directly to a terminal stream'; exit 0"
	}
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
"terminal attach --help") %s;;
"agent start --help") printf '      --kind <KIND>\n          [possible values: %s]\n'; exit 0;;
esac
exit 2
`, attach, kinds)
	path := filepath.Join(folder, "herdr")
	return path, os.WriteFile(path, []byte(script), 0o755)
}

// Build compiles scripts/fakeherdr to folder/herdr (named herdr, so it is
// found like the real one) and returns its path. Tests only.
func Build(folder string) (string, error) {
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return "", err
	}
	module, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	path := filepath.Join(folder, "herdr")
	build := exec.Command("go", "build", "-o", path, "./scripts/fakeherdr")
	build.Dir = filepath.Dir(strings.TrimSpace(string(module)))
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("herdrtest: build fakeherdr: %v\n%s", err, output)
	}
	return path, nil
}

// Scenario writes folder/scenario.json for fakeherdr from a herdr snapshot.
func Scenario(folder string, attachAny bool, kinds []string, snapshot []byte) error {
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{
		"version": "0.8.2", "protocol": 20, "attachAny": attachAny, "kinds": kinds, "snapshot": json.RawMessage(snapshot),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(folder, "scenario.json"), data, 0o600)
}

// Control sends one command to a running fakeherdr server's control.sock.
func Control(folder string, command map[string]any) error {
	conn, err := net.Dial("unix", filepath.Join(folder, "control.sock"))
	if err != nil {
		return err
	}
	defer conn.Close()
	data, _ := json.Marshal(command)
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return err
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return err
	}
	if reply != "ok\n" {
		return fmt.Errorf("herdrtest: control %v: %s", command, strings.TrimSpace(reply))
	}
	return nil
}
