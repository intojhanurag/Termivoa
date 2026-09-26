package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/host"
)

const adminTestID = "20000000-0000-4000-8000-000000000001"

func TestAdminCommandParsing(t *testing.T) {
	production, err := host.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(t.TempDir(), "runtime")
	for _, test := range []struct {
		command, action string
		args            []string
		arguments       []string
		origin, name    string
	}{
		{command: "pair", action: "pair"},
		{command: "pair", action: "pair", args: []string{"--origin", "https://desk.example.ts.net"}, origin: "https://desk.example.ts.net"},
		{command: "pair", action: "pair pending", args: []string{"pending"}},
		{command: "pair", action: "pair approve", args: []string{"approve", adminTestID}, arguments: []string{adminTestID}},
		{command: "devices", action: "devices"},
		{command: "revoke", action: "revoke", args: []string{adminTestID}, arguments: []string{adminTestID}},
		{command: "project", action: "project add", args: []string{"add", "--name", "Web", "."}, arguments: []string{"."}, name: "Web"},
		{command: "project", action: "project add", args: []string{"add", "some dir"}, arguments: []string{"some dir"}},
		{command: "project", action: "project list", args: []string{"list"}},
		{command: "profile", action: "profile list", args: []string{"list"}},
		{command: "profile", action: "profile detect", args: []string{"detect"}},
		{command: "config", action: "config origin", args: []string{"origin"}},
		{command: "config", action: "config origin", args: []string{"origin", "http://127.0.0.1:8788"}, arguments: []string{"http://127.0.0.1:8788"}},
	} {
		config, err := parseAdminCommand(test.command, test.args)
		if err != nil {
			t.Fatalf("%s %v: %v", test.command, test.args, err)
		}
		name := ""
		if config.name != nil {
			name = *config.name
		}
		if config.action != test.action || config.runtimeDir != production.RuntimeDir || config.origin != test.origin ||
			name != test.name || strings.Join(config.arguments, "\x00") != strings.Join(test.arguments, "\x00") {
			t.Fatalf("%s %v parsed as %#v", test.command, test.args, config)
		}
	}
	config, err := parseAdminCommand("pair", []string{"approve", "--dev", "--runtime-dir", custom, adminTestID})
	if err != nil || config.runtimeDir != custom || config.arguments[0] != adminTestID {
		t.Fatalf("development admin config = %#v, %v", config, err)
	}
	if _, err := os.Stat(custom); !os.IsNotExist(err) {
		t.Fatalf("parsing created a runtime directory: %v", err)
	}
}

func TestAdminCommandUsageDoesNotContactBroker(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "runtime")
	for _, args := range [][]string{
		{"pair", "unexpected"},
		{"pair", "--origin", "http://example.com"},
		{"pair", "--origin", "https://host/path"},
		{"pair", "pending", "extra"},
		{"pair", "approve"},
		{"pair", "approve", adminTestID, adminTestID},
		{"pair", "approve", "--origin", "https://desk.example.ts.net", adminTestID},
		{"devices", "extra"},
		{"devices", "--unknown"},
		{"devices", "--runtime-dir", custom},
		{"revoke"},
		{"revoke", adminTestID, "extra"},
		{"project"},
		{"project", "remove", "x"},
		{"project", "add"},
		{"project", "add", "--name", "", "."},
		{"project", "add", "one", "two"},
		{"project", "list", "extra"},
		{"profile"},
		{"profile", "add"},
		{"profile", "list", "extra"},
		{"profile", "detect", "extra"},
		{"config"},
		{"config", "cookie"},
		{"config", "origin", "http://example.com"},
		{"config", "origin", "https://a.example", "https://b.example"},
	} {
		var output bytes.Buffer
		err := runWithInput(context.Background(), args, nil, &output)
		if err == nil || strings.Contains(err.Error(), "cannot connect") {
			t.Fatalf("invalid arguments %v reached broker or succeeded: %v", args, err)
		}
		if output.Len() != 0 {
			t.Fatalf("invalid arguments %v printed %q", args, output.String())
		}
	}
	for _, args := range [][]string{
		{"pair", "--help"}, {"pair", "approve", "--help"}, {"devices", "-h"}, {"revoke", "--help"},
		{"project", "--help"}, {"project", "add", "--help"}, {"profile", "-h"}, {"config", "origin", "--help"},
	} {
		var output bytes.Buffer
		if err := runWithInput(context.Background(), args, nil, &output); err != nil {
			t.Fatalf("help %v: %v", args, err)
		}
		if output.String() != commandHelp {
			t.Fatalf("help %v returned %q", args, output.String())
		}
	}
}

func TestAdminUsageErrorsNameTheCommand(t *testing.T) {
	_, err := parseAdminCommand("project", []string{"add"})
	if err == nil || !strings.Contains(err.Error(), "pterm project add [--dev] [--runtime-dir <directory>] [--name <name>] <path>") {
		t.Fatalf("unexpected usage error: %v", err)
	}
	_, err = parseAdminCommand("profile", nil)
	if err == nil || !strings.Contains(err.Error(), "pterm profile <list|detect>") {
		t.Fatalf("unexpected subcommand error: %v", err)
	}
}
