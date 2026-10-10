//go:build darwin || linux

package transcript_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/transcript"
	"github.com/intojhanurag/phone-terminal/internal/transcript/transcripttest"
)

func TestBindStates(t *testing.T) {
	for name, test := range map[string]struct {
		change func(f *fixture)
		state  string
		reason string
	}{
		"linked": {func(f *fixture) { f.session.Prompt("hi") }, protocol.ChatLinkLinked, ""},
		"no agent": {func(f *fixture) {
			f.procs = f.procs[:1]
		}, protocol.ChatLinkUnsupported, ""},
		"stale pid file": {func(f *fixture) {
			f.session.Prompt("hi")
			f.procs[1].Start = agentStart.Add(-time.Minute) // the pid was reused
		}, protocol.ChatLinkUnsupported, ""},
		"fake pid file": {func(f *fixture) {
			f.session.Prompt("hi")
			write(t, filepath.Join(f.root, "sessions", "4242.json"), `{"pid":1,"sessionId":"`+f.session.ID+`","startedAt":0}`)
		}, protocol.ChatLinkUnsupported, ""},
		"two agents": {func(f *fixture) {
			other, _ := transcripttest.New(f.root, f.cwd)
			_ = other.PIDFile(5000, agentStart)
			f.procs = append(f.procs, transcript.Process{PID: 5000, Start: agentStart})
		}, protocol.ChatLinkNone, protocol.ChatReasonSeveralAgents},
		"custom root": {func(f *fixture) {
			f.config.CustomRoot, f.procs = true, f.procs[:1]
		}, protocol.ChatLinkNone, protocol.ChatReasonCustomRoot},
		"no transcript yet": {func(f *fixture) {
			_ = os.Remove(f.session.Path)
		}, protocol.ChatLinkSearching, ""},
		"empty transcript": {func(*fixture) {}, protocol.ChatLinkSearching, ""},
		"other folder": {func(f *fixture) {
			f.session.Prompt("hi")
			f.config.Dir = func(context.Context, int) (string, error) { return "/somewhere/else", nil }
		}, protocol.ChatLinkNone, protocol.ChatReasonNoFile},
		"header names another folder": {func(f *fixture) {
			f.session.Cwd = "/other"
			f.session.Prompt("hi")
		}, protocol.ChatLinkNone, protocol.ChatReasonNoFile},
		"same session in two folders": {func(f *fixture) {
			f.session.Prompt("hi")
			folder := filepath.Join(f.root, "projects", "-elsewhere")
			_ = os.MkdirAll(folder, 0o700)
			write(t, filepath.Join(folder, f.session.ID+".jsonl"), "")
		}, protocol.ChatLinkNone, protocol.ChatReasonNoFile},
		"symlink": {func(f *fixture) {
			f.session.Prompt("hi")
			real := filepath.Join(filepath.Dir(f.root), "real.jsonl")
			_ = os.Rename(f.session.Path, real)
			_ = os.Symlink(real, f.session.Path)
		}, protocol.ChatLinkNone, protocol.ChatReasonFileRefused},
		"symlinked folder": {func(f *fixture) {
			f.session.Prompt("hi")
			folder := filepath.Dir(f.session.Path)
			real := filepath.Join(f.root, "projects", "real")
			_ = os.Rename(folder, real)
			_ = os.Symlink(real, folder)
		}, protocol.ChatLinkNone, protocol.ChatReasonNoFile},
		"hard link": {func(f *fixture) {
			f.session.Prompt("hi")
			_ = os.Link(f.session.Path, filepath.Join(filepath.Dir(f.root), "copy.jsonl"))
		}, protocol.ChatLinkNone, protocol.ChatReasonFileRefused},
		"group writable": {func(f *fixture) {
			f.session.Prompt("hi")
			_ = os.Chmod(f.session.Path, 0o620)
		}, protocol.ChatLinkNone, protocol.ChatReasonFileRefused},
		"fifo": {func(f *fixture) {
			_ = os.Remove(f.session.Path)
			if err := syscall.Mkfifo(f.session.Path, 0o600); err != nil {
				t.Skip(err)
			}
		}, protocol.ChatLinkNone, protocol.ChatReasonFileRefused},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			test.change(f)
			link := f.bind()
			if link.State != test.state || link.Reason != test.reason {
				t.Fatalf("link = %s %s, want %s %s", link.State, link.Reason, test.state, test.reason)
			}
		})
	}
}

func write(t *testing.T, name, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRecheck(t *testing.T) {
	f := newFixture(t)
	f.session.Prompt("hi")
	tail := f.linked()
	if got := tail.Recheck(f.config, f.procs, true); got != transcript.RecheckOK {
		t.Fatalf("recheck = %q", got)
	}
	if got := tail.Recheck(f.config, f.procs[:1], false); got != transcript.RecheckEnded {
		t.Fatalf("agent gone: %q", got)
	}
	// /clear: a newer transcript in the same folder that no pid file names.
	time.Sleep(20 * time.Millisecond)
	cleared, _ := transcripttest.New(f.root, f.cwd)
	cleared.Prompt("after clear")
	if got := tail.Recheck(f.config, f.procs, false); got != transcript.RecheckOK {
		t.Fatalf("while the file grows there is no clear check: %q", got)
	}
	// Another agent in the same folder names its own file: not a clear.
	_ = cleared.PIDFile(7000, agentStart)
	if got := tail.Recheck(f.config, f.procs, true); got != transcript.RecheckOK {
		t.Fatalf("claimed file: %q", got)
	}
	_ = os.Remove(filepath.Join(f.root, "sessions", "7000.json"))
	// Another claude (or claude -p) that ran and left: not our /clear (C-2).
	if got := tail.Recheck(f.config, f.procs, true); got != transcript.RecheckOK {
		t.Fatalf("an unrelated newer file: %q", got)
	}
	// Our own pid file written after our transcript stopped ties it to us.
	later := time.Now().Add(time.Second)
	_ = os.Chtimes(filepath.Join(f.root, "sessions", "4242.json"), later, later)
	if got := tail.Recheck(f.config, f.procs, true); got != transcript.RecheckCleared {
		t.Fatalf("cleared: %q", got)
	}
	// Linux start times move with btime: 1.5 s apart is the same process (C-4).
	moved := []transcript.Process{{PID: agentPID, Start: agentStart.Add(1500 * time.Millisecond)}}
	if got := tail.Recheck(f.config, moved, false); got == transcript.RecheckEnded {
		t.Fatal("a 1.5 s start jitter ended the agent")
	}
	// The path now names another file: re-bind.
	_ = os.Rename(f.session.Path, f.session.Path+".superseded-1")
	write(t, f.session.Path, "")
	if got := tail.Recheck(f.config, f.procs, false); got != transcript.RecheckRebind {
		t.Fatalf("replaced: %q", got)
	}
}

func TestGuardRefusesRealFolders(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a test must not name a folder outside temp")
		}
	}()
	home, _ := os.UserHomeDir()
	transcript.Bind(context.Background(), transcript.Config{Claude: filepath.Join(home, ".claude")}, nil)
}

func TestBindFailsClosedOverTheFolderLimit(t *testing.T) {
	f := newFixture(t)
	f.session.Prompt("hi")
	defer transcript.SetMaxProjectDirs(3)()
	for index := range 3 {
		_ = os.MkdirAll(filepath.Join(f.root, "projects", fmt.Sprintf("-other-%d", index)), 0o700)
	}
	if link := f.bind(); link.State != protocol.ChatLinkNone || link.Reason != protocol.ChatReasonNoFile {
		t.Fatalf("link = %s %s", link.State, link.Reason)
	}
}
