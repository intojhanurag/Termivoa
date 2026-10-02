package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/platform"
)

func TestLocalCommandProfileIsolation(t *testing.T) {
	production, err := platform.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	development, err := platform.DevelopmentPaths()
	if err != nil {
		t.Fatal(err)
	}
	config, err := parseLocalCommand("list", nil)
	if err != nil || config.runtimeDir != production.RuntimeDir {
		t.Fatalf("production config = %#v, %v", config, err)
	}
	config, err = parseLocalCommand("list", []string{"--dev"})
	if err != nil || config.runtimeDir != development.RuntimeDir {
		t.Fatalf("development config = %#v, %v", config, err)
	}
	custom := filepath.Join(t.TempDir(), "runtime")
	config, err = parseLocalCommand("terminate", []string{"--dev", "--runtime-dir", custom, "session-id"})
	if err != nil || config.runtimeDir != custom || len(config.arguments) != 1 {
		t.Fatalf("explicit development config = %#v, %v", config, err)
	}
	for _, args := range [][]string{
		{"--runtime-dir", custom},
		{"--dev", "--runtime-dir", production.RuntimeDir},
		{"--dev", "--runtime-dir", filepath.Join(production.RuntimeDir, "child")},
	} {
		if _, err := parseLocalCommand("list", args); err == nil {
			t.Fatalf("unsafe profile accepted: %v", args)
		}
	}
	if _, err := os.Stat(custom); !os.IsNotExist(err) {
		t.Fatalf("read-only configuration created a runtime directory: %v", err)
	}
}

func TestLocalCommandUsageDoesNotContactBroker(t *testing.T) {
	for _, args := range [][]string{
		{"list", "unexpected"}, {"list", "--unknown"}, {"terminate"}, {"terminate", "one", "two"},
	} {
		var output bytes.Buffer
		err := runWithInput(context.Background(), args, nil, &output)
		if err == nil || strings.Contains(err.Error(), "cannot connect") {
			t.Fatalf("invalid arguments %v reached broker or succeeded: %v", args, err)
		}
	}
	for _, args := range [][]string{{"help"}, {"--help"}, {"list", "--help"}, {"terminate", "--help"}} {
		var output bytes.Buffer
		if err := runWithInput(context.Background(), args, nil, &output); err != nil {
			t.Fatalf("help %v: %v", args, err)
		}
		if output.String() != commandHelp {
			t.Fatalf("help %v returned %q", args, output.String())
		}
	}
}
