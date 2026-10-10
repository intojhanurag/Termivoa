//go:build !windows

package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFakeAgentWritesOnlyUnderATempHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	if err := run(2, time.Millisecond, true); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("transcripts = %v", matches)
	}
	t.Setenv("HOME", "/")
	if err := run(1, 0, false); err == nil {
		t.Fatal("a HOME outside temp was accepted")
	}
}
