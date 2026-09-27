package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestAttachUsageAndExplicitControl(t *testing.T) {
	for _, args := range [][]string{{"attach"}, {"attach", "one", "two"}, {"attach", "--unknown"}} {
		var output bytes.Buffer
		err := runWithInput(context.Background(), args, nil, &output)
		if err == nil || strings.Contains(err.Error(), "connect") {
			t.Fatalf("invalid attach args reached broker: %v", err)
		}
	}
	var output bytes.Buffer
	if err := runWithInput(context.Background(), []string{"attach", "--help"}, nil, &output); err != nil || !strings.Contains(output.String(), "Ctrl+]") {
		t.Fatalf("help = %q %v", output.String(), err)
	}
	for _, control := range []bool{false, true} {
		args := []string{"session-id"}
		if control {
			args = append([]string{"--control"}, args...)
		}
		config, err := parseLocalCommand("attach", args)
		if err != nil || config.control != control {
			t.Fatalf("control=%v config=%+v err=%v", control, config, err)
		}
	}
	if _, err := parseLocalCommand("list", []string{"--control"}); err == nil {
		t.Fatal("control flag escaped attach scope")
	}
}
