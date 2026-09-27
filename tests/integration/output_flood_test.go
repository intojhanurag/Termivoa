//go:build (darwin || windows) && stressprobe

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/intojhanurag/phone-terminal/internal/broker"
	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const (
	floodMarker     = "pterm-output-flood-fixture"
	floodRows       = 32768
	floodRowBytes   = 80
	floodGeneration = "d2222222-2222-4222-8222-222222222222"
)

// The fixed-size synthetic stream is deliberately larger than retained history.
// This is a correctness probe, not the ten-minute throughput or 24-hour soak gate.
func TestOutputFloodFixture(t *testing.T) {
	index := slices.Index(os.Args, floodMarker)
	if index < 0 {
		t.Skip("helper subprocess only")
	}
	if index+1 >= len(os.Args) {
		os.Exit(2)
	}
	if err := runOutputFloodFixture(os.Args[index+1]); err != nil {
		fmt.Fprintln(os.Stderr, "SYNTHETIC_FLOOD_ERROR:", err)
		os.Exit(7)
	}
	os.Exit(0)
}

func runOutputFloodFixture(startFile string) error {
	state, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		return err
	}
	defer term.Restore(os.Stdin.Fd(), state) //nolint:errcheck
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(startFile); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if !time.Now().Before(deadline) {
			return errors.New("fixture start barrier expired")
		}
		time.Sleep(5 * time.Millisecond)
	}
	row := append(bytes.Repeat([]byte("x"), floodRowBytes-2), '\r', '\n')
	chunk := bytes.Repeat(row, 256)
	for writtenRows := 0; writtenRows < floodRows; writtenRows += 256 {
		if n, err := os.Stdout.Write(chunk); err != nil {
			return err
		} else if n != len(chunk) {
			return io.ErrShortWrite
		}
	}
	if _, err := fmt.Fprintf(os.Stdout, "\x1b[2J\x1b[HFLOOD_DONE bytes=%d pid=%d\x1b[3;7H\x1b[6n", floodRows*floodRowBytes, os.Getpid()); err != nil {
		return err
	}
	response := make([]byte, len("\x1b[3;7R"))
	if _, err := io.ReadFull(os.Stdin, response); err != nil {
		return err
	}
	if string(response) != "\x1b[3;7R" {
		return errors.New("post-flood cursor reply was incorrect")
	}
	if _, err := io.WriteString(os.Stdout, "\x1b[4;1HFLOOD_QUERY_OK"); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, os.Stdin)
	return err
}

type stalledFloodSink struct{ started chan struct{} }

func (sink stalledFloodSink) Send(ctx context.Context, _ []byte, _ func()) error {
	select {
	case sink.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err() // No retained copy; no drained callback is owed.
}

func TestNativeOutputFloodPreservesCanonicalStateWithoutConsumingViewer(t *testing.T) {
	for _, stalled := range []bool{false, true} {
		t.Run(fmt.Sprintf("stalled=%t", stalled), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			startFile := filepath.Join(directory, "start-synthetic-output")
			session, err := broker.StartSession(ctx, broker.SessionConfig{
				ID: "d1111111-1111-4111-8111-111111111111", Generation: floodGeneration,
				Process: ptyhost.Spec{Executable: executable,
					Args:      []string{"-test.run=^TestOutputFloodFixture$", "--", floodMarker, startFile},
					Directory: directory, Columns: 80, Rows: 24,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := session.Close(); err != nil {
					t.Errorf("close owned flood session: %v", err)
				}
			}()
			var viewer *broker.ViewerSubscription
			if stalled {
				sink := stalledFloodSink{started: make(chan struct{}, 1)}
				viewer, err = session.AttachViewer(ctx, broker.ViewerConfig{
					ConnectionID: "d3333333-3333-4333-8333-333333333333",
					DeviceID:     "d4444444-4444-4444-8444-444444444444", Generation: floodGeneration,
					Sink: sink, SendTimeout: 250 * time.Millisecond,
				})
				if err != nil {
					t.Fatal(err)
				}
				defer viewer.Close()
				select {
				case <-sink.started:
				case <-ctx.Done():
					t.Fatal("stalled sink never received a delivery")
				}
			}
			if err := os.WriteFile(startFile, []byte("start"), 0o600); err != nil {
				t.Fatal(err)
			}
			began := time.Now()
			defer func() {
				if !t.Failed() {
					return
				}
				probeCtx, stopProbe := context.WithTimeout(context.Background(), time.Second)
				defer stopProbe()
				frame, frameErr := session.Snapshot(probeCtx)
				status, statusErr := session.Status(probeCtx)
				t.Logf("flood failure state: elapsed=%s pid=%d state=%s revision=%d historyRows=%d bytes=%d completedMarker=%t queryMarker=%t frameErr=%v statusErr=%v", time.Since(began), status.PID, status.State, frame.Revision, frame.HistoryRows, frame.HistoryBytes, strings.Contains(liveScreenText(frame), "FLOOD_DONE"), strings.Contains(liveScreenText(frame), "FLOOD_QUERY_OK"), frameErr, statusErr)
			}()
			// This bulk parsing probe uses its declared 30s fixture deadline,
			// not the five-second interactive-keystroke helper's deadline.
			waitFloodCompletion(t, ctx, session)
			frame, err := session.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			status, err := session.Status(ctx)
			if err != nil || status.State != broker.SessionRunning {
				t.Fatalf("flood child stopped unexpectedly: %#v, %v", status, err)
			}
			expected := fmt.Sprintf("FLOOD_DONE bytes=%d pid=%d", floodRows*floodRowBytes, status.PID)
			if !strings.Contains(liveScreenText(frame), expected) {
				t.Fatal("canonical screen lost completed stream identity")
			}
			if frame.Columns != 80 || frame.Rows != 24 || frame.Alternate || frame.ContentClipped {
				t.Fatalf("invalid post-flood metadata: grid=%dx%d alternate=%t clipped=%t", frame.Columns, frame.Rows, frame.Alternate, frame.ContentClipped)
			}
			if frame.HistoryRows <= 0 || frame.HistoryRows > terminal.MaxHistoryRows || frame.HistoryBytes <= 0 || frame.HistoryBytes > terminal.MaxHistoryBytes {
				t.Fatalf("history bounds: rows=%d bytes=%d", frame.HistoryRows, frame.HistoryBytes)
			}
			page, err := session.ReadHistory(ctx, floodGeneration, terminal.HistoryReadRequest{
				Epoch: frame.HistoryEpoch, Revision: frame.HistoryRevision, Limit: terminal.MaxHistoryPageRows,
			})
			if err != nil || page.Changed || len(page.Rows) != terminal.MaxHistoryPageRows || page.RetainedRows != frame.HistoryRows || page.RetainedBytes != frame.HistoryBytes {
				t.Fatalf("post-flood bounded history page: changed=%t rows=%d retainedRows=%d retainedBytes=%d error=%v", page.Changed, len(page.Rows), page.RetainedRows, page.RetainedBytes, err)
			}
			if viewer != nil {
				select {
				case <-viewer.Done():
					if !errors.Is(viewer.Err(), broker.ErrViewerSlow) {
						t.Fatalf("stalled viewer exit: %v", viewer.Err())
					}
				case <-ctx.Done():
					t.Fatal("stalled viewer was not disconnected")
				}
			}
			assertFloodReattachment(t, ctx, session, frame)
			t.Logf("synthetic bytes=%d elapsed=%s historyRows=%d historyBytes=%d; not a throughput/RSS acceptance measurement", floodRows*floodRowBytes, time.Since(began), frame.HistoryRows, frame.HistoryBytes)
		})
	}
}

func waitFloodCompletion(t *testing.T, ctx context.Context, session *broker.Session) {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		frame, err := session.Snapshot(ctx)
		if err != nil {
			t.Fatal("flood canonical snapshot: ", err)
		}
		if strings.Contains(liveScreenText(frame), "FLOOD_QUERY_OK") {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("flood completion: ", ctx.Err())
		}
	}
}

type floodDelivery struct {
	data    []byte
	drained func()
}

type floodCaptureSink struct{ messages chan floodDelivery }

func (sink floodCaptureSink) Send(ctx context.Context, data []byte, drained func()) error {
	select {
	case sink.messages <- floodDelivery{data: data, drained: drained}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Reattach only after the child has completed and answered its own query.
// Compare the actual chunked transport snapshot, not a fabricated raw replay.
func assertFloodReattachment(t *testing.T, ctx context.Context, session *broker.Session, expected terminal.Frame) {
	t.Helper()
	sink := floodCaptureSink{messages: make(chan floodDelivery)}
	viewer, err := session.AttachViewer(ctx, broker.ViewerConfig{
		ConnectionID: "d5555555-5555-4555-8555-555555555555",
		DeviceID:     "d6666666-6666-4666-8666-666666666666", Generation: floodGeneration, Sink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	renderer, err := desktop.NewRenderer(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	client, err := desktop.NewFrameClient(renderer)
	if err != nil {
		t.Fatal(err)
	}
	expectedScreen, expectedRows, err := protocol.Snapshot(expected, expected.HistoryEpoch, expected.HistoryRevision)
	if err != nil {
		t.Fatal(err)
	}
	var rows []protocol.TerminalRow
	for {
		select {
		case delivery := <-sink.messages:
			message, err := protocol.DecodeHost(delivery.data)
			delivery.drained()
			if err != nil || message.Generation == nil || *message.Generation != floodGeneration {
				t.Fatalf("post-flood message validation: %v", err)
			}
			switch message.Type {
			case protocol.SnapshotBegin:
				payload, _ := protocol.DecodePayload[protocol.SnapshotBeginPayload](message)
				if !reflect.DeepEqual(payload.Screen, expectedScreen) {
					t.Fatal("post-flood snapshot metadata differs from canonical state")
				}
				err = client.BeginSnapshot(floodGeneration, payload)
			case protocol.SnapshotRows:
				payload, _ := protocol.DecodePayload[protocol.SnapshotRowsPayload](message)
				if payload.StartRow != int64(len(rows)) {
					t.Fatal("post-flood rows are not contiguous")
				}
				rows = append(rows, payload.Rows...)
				err = client.AddSnapshotRows(floodGeneration, payload)
			case protocol.SnapshotEnd:
				payload, _ := protocol.DecodePayload[protocol.SnapshotEndPayload](message)
				if err := client.EndSnapshot(floodGeneration, payload); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(rows, expectedRows) || client.DisplayedRevision() != strconv.FormatUint(expected.Revision, 10) {
					t.Fatal("reattachment did not restore the complete canonical screen/revision")
				}
				if err := viewer.SnapshotApplied(ctx, payload.SnapshotID, expected.Revision); err != nil {
					t.Fatal(err)
				}
				return
			case protocol.ControlChanged, protocol.SessionState:
			default:
				t.Fatalf("unexpected post-flood message %s", message.Type)
			}
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("post-flood reattachment: ", ctx.Err())
		}
	}
}
