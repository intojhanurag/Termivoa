//go:build (darwin || windows) && stressprobe

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/intojhanurag/phone-terminal/internal/broker"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
)

const activeOutputMarker = "pterm-active-output-shutdown"

// This owned child continuously writes synthetic bytes until explicitly
// terminated. Unlike a finite benchmark, it cannot finish before the parent
// reaches the shutdown barrier. It does not use shell files or provider accounts.
func TestActiveOutputShutdownFixture(t *testing.T) {
	if !slices.Contains(os.Args, activeOutputMarker) {
		t.Skip("helper subprocess only")
	}
	state, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		os.Exit(7)
	}
	defer term.Restore(os.Stdin.Fd(), state) //nolint:errcheck
	row := append(bytes.Repeat([]byte("x"), 78), '\r', '\n')
	chunk := bytes.Repeat(row, 256)
	for {
		if n, err := os.Stdout.Write(chunk); err != nil || n != len(chunk) {
			os.Exit(8)
		}
	}
}

func TestNativeSessionShutdownDrainsActiveOutput(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit-terminate=%t", explicit), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			session, err := broker.StartSession(ctx, broker.SessionConfig{
				ID: "e1111111-1111-4111-8111-111111111111", Generation: floodGeneration,
				Process: ptyhost.Spec{Executable: executable,
					Args:      []string{"-test.run=^TestActiveOutputShutdownFixture$", "--", activeOutputMarker},
					Directory: t.TempDir(), Columns: 80, Rows: 24},
				PTYOutputQueue: 1, // Exercise bounded drain, never a disk/raw-output spool.
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = session.Close()
				select {
				case <-session.Done():
				case <-time.After(5 * time.Second):
					t.Error("owned active-output child/runtime did not join")
				}
			}()
			status, err := session.Status(ctx)
			if err != nil || status.PID <= 0 || status.State != broker.SessionRunning {
				t.Fatalf("active-output child did not start: %+v %v", status, err)
			}
			for {
				frame, err := session.Snapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if frame.HistoryRows >= 200 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("active-output barrier: ", ctx.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}
			began := time.Now()
			if explicit {
				if err := session.Terminate(ctx); err != nil {
					t.Fatalf("terminate while output is active: %v", err)
				}
				select {
				case <-session.Ended():
				case <-ctx.Done():
					t.Fatal("explicitly terminated child did not finish")
				}
			}
			if err := session.Close(); err != nil {
				t.Fatalf("close while output is active: %v", err)
			}
			select {
			case <-session.Done():
			default:
				t.Fatal("successful Close did not join the runtime")
			}
			final, ok := session.FinalStatus()
			if !ok || final.PID != status.PID || final.Generation != status.Generation {
				t.Fatal("shutdown changed the managed child identity")
			}
			expected := broker.SessionLost
			if explicit {
				expected = broker.SessionExited
			}
			if final.State != expected {
				t.Fatalf("shutdown state=%s, want %s", final.State, expected)
			}
			// Final state is available without reviving or polling a dead actor.
			if _, err := session.Status(ctx); !errors.Is(err, broker.ErrSessionClosed) {
				t.Fatalf("closed session still accepted a request: %v", err)
			}
			t.Logf("owned child pid=%d explicit=%t clean termination/drain/join=%s; correctness only, not a throughput or soak measurement", status.PID, explicit, time.Since(began))
		})
	}
}
