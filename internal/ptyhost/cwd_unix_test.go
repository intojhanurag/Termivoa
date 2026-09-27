//go:build darwin || linux

package ptyhost

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A real interactive shell: WorkingDirectory follows cd (via /proc on Linux,
// lsof on macOS) and prefers the foreground job over the shell.
func TestWorkingDirectoryFollowsShellAndForegroundJob(t *testing.T) {
	start, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(start, "sub dir")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process, err := Start(ctx, Spec{Executable: "/bin/sh", Args: []string{"-i"}, Directory: start, Env: []string{"PS1=$ ", "ENV="}, Columns: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	go func() { _, _ = io.Copy(io.Discard, process) }()

	waitDirectory := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			got, err := process.WorkingDirectory(context.Background())
			if err == nil && got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("WorkingDirectory = %q, %v; want %q", got, err, want)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitDirectory(start)
	if _, err := process.Write([]byte("cd 'sub dir'\n")); err != nil {
		t.Fatal(err)
	}
	waitDirectory(sub)
	if _, err := process.Write([]byte("sh -c 'cd /; exec sleep 30'\n")); err != nil {
		t.Fatal(err)
	}
	waitDirectory("/")

	cancel()
	if err := process.Wait(context.Background()); err == nil {
		t.Log("shell exited cleanly")
	}
	if got, err := process.WorkingDirectory(context.Background()); got != "" || err != nil {
		t.Fatalf("after exit: %q, %v; want nothing", got, err)
	}
}
