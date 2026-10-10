//go:build !windows

// Command fakeagent is a fake Claude Code for the Chat view (docs/chat-view.md):
// started in a Termivoa session, it writes its own pid file and a synthetic
// transcript under $HOME/.claude at a set pace, so the broker links and
// follows it like the real agent. It never starts a real agent and refuses a
// HOME outside a temp folder, so it never writes to the owner's ~/.claude.
//
//	fakeagent [-turns 5] [-every 1s] [-stamp]
//
// With -stamp each prompt is "stamp <unix-nanoseconds>", so an e2e check can
// measure the time from a write to the phone.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/transcript/transcripttest"
)

func main() {
	turns := flag.Int("turns", 5, "turns to write")
	every := flag.Duration("every", time.Second, "pause between turns")
	stamp := flag.Bool("stamp", false, "prompts carry the write time")
	flag.Parse()
	if err := run(*turns, *every, *stamp); err != nil {
		fmt.Fprintln(os.Stderr, "fakeagent:", err)
		os.Exit(1)
	}
}

func run(turns int, every time.Duration, stamp bool) error {
	started := time.Now()
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if !insideTemp(home) {
		return fmt.Errorf("HOME %q is not a temp folder; fakeagent writes only to a throwaway HOME", home)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	session, err := transcripttest.New(filepath.Join(home, ".claude"), cwd)
	if err != nil {
		return err
	}
	defer session.Close()
	session.Now, session.Step = time.Now().UTC(), 0
	if err := session.PIDFile(os.Getpid(), started); err != nil {
		return err
	}
	defer os.Remove(filepath.Join(home, ".claude", "sessions", fmt.Sprintf("%d.json", os.Getpid())))
	for turn := range turns {
		session.Now = time.Now().UTC()
		prompt := fmt.Sprintf("turn %d", turn+1)
		if stamp {
			prompt = fmt.Sprintf("stamp %d", time.Now().UnixNano())
		}
		session.Turn(prompt)
		fmt.Printf("wrote %s\n", prompt)
		time.Sleep(every)
	}
	return nil
}

func insideTemp(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	for _, root := range []string{os.TempDir(), "/tmp"} {
		if root, err := filepath.EvalSymlinks(root); err == nil && strings.HasPrefix(resolved, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
