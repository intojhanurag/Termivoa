package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestNewCommandRequiresSavedConfigurationAndNoPositionalArguments(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--project", "id"}, {"--shell", "id"},
		{"--project", "id", "--shell", "id", "extra"}, {"--control"},
	} {
		if _, err := parseLocalCommand("new", args); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	config, err := parseLocalCommand("new", []string{"--project", "project-id", "--shell", "shell-id"})
	if err != nil || config.projectID != "project-id" || config.profileID != "shell-id" {
		t.Fatalf("config=%+v err=%v", config, err)
	}
	// CLI.App performs canonical UUID validation before any create request.
	if _, err := parseLocalCommand("list", []string{"--project", "id"}); err == nil {
		t.Fatal("project flag escaped new command")
	}
}

func TestNewCommandHelpDoesNotNeedTerminalOrBroker(t *testing.T) {
	var output bytes.Buffer
	if err := runWithInput(context.Background(), []string{"new", "--help"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Create and take control of a new session") {
		t.Fatalf("missing explicit creation/control help: %q", output.String())
	}
}

func TestNewCommandRejectsRedirectedOutputBeforeBrokerAccess(t *testing.T) {
	var output bytes.Buffer
	err := runWithInput(context.Background(), []string{"new", "--project", "id", "--shell", "id"}, nil, &output)
	if err == nil || !strings.Contains(err.Error(), "no session was created") || strings.Contains(err.Error(), "connect") {
		t.Fatalf("unexpected error: %v", err)
	}
}
