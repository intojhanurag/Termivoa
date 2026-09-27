//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/intojhanurag/phone-terminal/internal/cli"
	"github.com/intojhanurag/phone-terminal/internal/liveclient"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestNewCommandCreatesControlsAndDetachesWithoutTerminating(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "detach"
		if cancelled {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			runtimeDir, management := startCommandBroker(t)
			master, slave := newCommandPTY(t, 81, 25)
			before, err := term.GetState(slave.Fd())
			if err != nil {
				t.Fatal(err)
			}
			capture := &newCommandCapture{}
			drained := make(chan struct{})
			go func() { defer close(drained); _, _ = io.Copy(capture, master) }()
			t.Cleanup(func() {
				_ = slave.Close()
				_ = master.Close()
				select {
				case <-drained:
				case <-time.After(time.Second):
					t.Error("synthetic terminal capture did not stop")
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- runWithInput(ctx, []string{"new", "--dev", "--runtime-dir", runtimeDir, "--project", commandProjectID, "--shell", commandProfileID}, slave, slave)
			}()
			joined := false
			defer func() {
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(10 * time.Second):
						t.Error("new command failed to join")
					}
				}
			}()
			waitContext, stopWait := context.WithTimeout(context.Background(), 10*time.Second)
			defer stopWait()
			var record protocol.BrokerRPCSessionRecord
			for {
				page, err := management.ListPage(waitContext, nil, 2)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Sessions) > 1 {
					t.Fatal("one new command created multiple sessions")
				}
				if len(page.Sessions) == 1 {
					record = page.Sessions[0]
					if capture.contains("Control |") {
						break
					}
				}
				select {
				case err := <-done:
					joined = true
					t.Fatalf("new stopped before controlling: %v", err)
				case <-waitContext.Done():
					t.Fatal("new session did not reach control and measured dimensions")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if record.ProjectID != commandProjectID || record.ShellProfileID != commandProfileID || !capture.contains("Created session "+record.ID) {
				t.Fatal("creation acknowledgement did not identify saved configuration/session")
			}
			// Management records are persisted metadata, not a live resize
			// subscription. Check dimensions through canonical observer frames.
			dimensions := make(chan [2]int64, 1)
			observer, err := liveclient.Dial(waitContext, liveclient.Config{
				RuntimeDir: runtimeDir, ClientBuild: "new-command-test", SessionID: record.ID, Generation: record.Generation,
				Output: newCommandDiscard{}, ValidateScreen: func(screen protocol.ScreenMetadata) error {
					if screen.Columns == 81 && screen.Rows == 24 {
						select {
						case dimensions <- [2]int64{screen.Columns, screen.Rows}:
						default:
						}
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			select {
			case <-dimensions:
			case <-waitContext.Done():
				t.Fatal("controller did not resize canonical screen to measured grid")
			}
			if err := observer.Close(); err != nil {
				t.Fatal(err)
			}
			if cancelled {
				cancel()
			} else if _, err := master.Write([]byte{0x1d}); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				joined = true
				if cancelled {
					if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), record.ID) {
						t.Fatalf("cancel did not preserve session recovery guidance: %v", err)
					}
				} else if err != nil {
					t.Fatalf("detach failed: %v", err)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("new did not return after detach/cancel")
			}
			after, err := term.GetState(slave.Fd())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("new did not restore outer terminal: %v", err)
			}
			status, err := management.Status(waitContext, localclient.SessionRef{ID: record.ID, Generation: record.Generation})
			if err != nil || status.State != protocol.Running {
				t.Fatalf("detach/cancel ended managed child: %s %v", status.State, err)
			}
			if _, err := slave.Write([]byte("caller still owns terminal\r\n")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNewCommandPreflightHasNoProcessSideEffects(t *testing.T) {
	runtimeDir, management := startCommandBroker(t)
	for _, dims := range [][2]uint16{{79, 24}, {80, 23}} {
		master, slave := newCommandPTY(t, dims[0], dims[1])
		before, err := term.GetState(slave.Fd())
		if err != nil {
			t.Fatal(err)
		}
		err = runWithInput(context.Background(), []string{"new", "--dev", "--runtime-dir", runtimeDir, "--project", commandProjectID, "--shell", commandProfileID}, slave, slave)
		if err == nil || !strings.Contains(err.Error(), "no session was created") {
			t.Fatalf("preflight error=%v", err)
		}
		after, err := term.GetState(slave.Fd())
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("preflight changed terminal: %v", err)
		}
		_ = slave.Close()
		_ = master.Close()
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	master, slave := newCommandPTY(t, 80, 24)
	defer master.Close()
	defer slave.Close()
	if _, _, err := cli.ValidateDesktopFiles(reader, slave); err == nil {
		t.Fatal("redirected stdin accepted")
	}
	if _, _, err := cli.ValidateDesktopFiles(slave, writer); err == nil {
		t.Fatal("redirected stdout accepted")
	}
	page, err := management.ListPage(context.Background(), nil, 2)
	if err != nil || len(page.Sessions) != 0 {
		t.Fatalf("preflight created a session: count=%d err=%v", len(page.Sessions), err)
	}
}

func newCommandPTY(t *testing.T, columns, rows uint16) (*os.File, *os.File) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close(); _ = master.Close() })
	if err := pty.Setsize(master, &pty.Winsize{Cols: columns, Rows: rows}); err != nil {
		t.Fatal(err)
	}
	return master, slave
}

// Capture only a bounded synthetic test display; never a user terminal.
type newCommandCapture struct {
	mu   sync.Mutex
	text string
}

type newCommandDiscard struct{}

func (newCommandDiscard) WriteContext(ctx context.Context, data []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (capture *newCommandCapture) Write(data []byte) (int, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.text += string(data)
	const maxCaptureBytes = 128 * 1024
	if len(capture.text) > maxCaptureBytes {
		capture.text = capture.text[len(capture.text)-maxCaptureBytes:]
	}
	return len(data), nil
}
func (capture *newCommandCapture) contains(value string) bool {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return strings.Contains(capture.text, value)
}
