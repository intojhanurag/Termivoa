package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCommandHelpCancelsSaturatedOutput(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"attach", "--help"}, {"new", "--help"}} {
		t.Run("pterm "+strings.Join(args, " "), func(t *testing.T) {
			reader, output, _ := saturatedManagementPipe(t)
			flags := managementOutputFlags(t, output)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- runWithInput(ctx, args, nil, output) }()
			select {
			case err := <-result:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("blocked help returned %v", err)
				}
			case <-time.After(time.Second):
				_ = reader.Close()
				select {
				case <-result:
				case <-time.After(time.Second):
					t.Fatal("help failed to join after releasing output")
				}
				t.Fatal("help did not honor cancellation")
			}
			if _, err := output.Stat(); err != nil {
				t.Fatalf("help closed caller output: %v", err)
			}
			if got := managementOutputFlags(t, output); got != flags {
				t.Fatalf("help changed output flags: %x to %x", flags, got)
			}
		})
	}
}
