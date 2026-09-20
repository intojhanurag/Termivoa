// ptychild is a deterministic child process used by PTY integration tests.
// It deliberately has no dependencies outside the Go standard library.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/charmbracelet/x/term"
)

func main() {
	mode := os.Getenv("PTERM_PTY_FIXTURE_MODE")
	if mode == "" && len(os.Args) == 2 && os.Args[1] == "counter" {
		mode = "counter"
	}
	switch mode {
	case "echo":
		echo()
	case "args":
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Args []string `json:"args"`
		}{Args: os.Args[1:]})
	case "session":
		info, err := currentSessionInfo()
		if err != nil {
			fmt.Fprintf(os.Stderr, "session info: %v\n", err)
			os.Exit(5)
		}
		_ = json.NewEncoder(os.Stdout).Encode(info)
	case "exit":
		code, _ := strconv.Atoi(os.Getenv("PTERM_PTY_FIXTURE_EXIT"))
		os.Exit(code)
	case "sleep":
		fmt.Println("READY")
		time.Sleep(time.Minute)
	case "counter":
		counter()
	case "tree":
		spawnDescendant(true)
	case "orphan":
		spawnDescendant(false)
	case "query":
		queryTerminal()
	case "leaf":
		time.Sleep(time.Minute)
	default:
		fmt.Fprintln(os.Stderr, "unknown PTERM_PTY_FIXTURE_MODE")
		os.Exit(2)
	}
}

func counter() {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for value := 1; ; value++ {
		fmt.Printf("COUNTER pid=%d value=%08d\n", os.Getpid(), value)
		<-ticker.C
	}
}

func queryTerminal() {
	state, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		fmt.Fprintf(os.Stderr, "make terminal raw: %v\n", err)
		os.Exit(6)
	}
	defer term.Restore(os.Stdin.Fd(), state) //nolint:errcheck

	queries := []struct {
		request  []byte
		response []byte
	}{
		{request: []byte("\x1b[3;7H\x1b[6n"), response: []byte("\x1b[3;7R")},
		{request: []byte("\x1b[5n"), response: []byte("\x1b[0n")},
	}
	for index, query := range queries {
		if _, err := os.Stdout.Write(query.request); err != nil {
			fmt.Fprintf(os.Stderr, "write query %d: %v\n", index, err)
			os.Exit(7)
		}
		got := make([]byte, len(query.response))
		if _, err := io.ReadFull(os.Stdin, got); err != nil {
			fmt.Fprintf(os.Stderr, "read response %d: %v\n", index, err)
			os.Exit(8)
		}
		if !bytes.Equal(got, query.response) {
			fmt.Fprintf(os.Stderr, "response %d: got %x want %x\n", index, got, query.response)
			os.Exit(9)
		}
	}
	fmt.Fprint(os.Stdout, "QUERY_OK")
}

func echo() {
	fmt.Println("READY")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		os.Exit(3)
	}
	encoded, _ := json.Marshal(line)
	fmt.Printf("ECHO:%s\nECHO_OK\n", encoded)
}

func spawnDescendant(wait bool) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "PTERM_PTY_FIXTURE_MODE=leaf")
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "spawn descendant: %v\n", err)
		os.Exit(4)
	}
	fmt.Printf("DESCENDANT=%d\n", cmd.Process.Pid)
	if wait {
		time.Sleep(time.Minute)
	}
}
